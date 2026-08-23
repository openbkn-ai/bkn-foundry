// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package seed centrally initialises authorization data at bkn-safe startup:
// roles, the resource-type/operation catalog, and role->permission grants.
//
// This replaces ISF's scattered initialisation (authorization service startup
// seed + each module's HTTP resource_type registration + DA InitPermission) with
// one idempotent seed in one service — no cross-service registration, no boot
// ordering. Role UUIDs are preserved (see data/roles.json).
package seed

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/audit"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/auth"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

// Built-in admin: a local login bound to super-admin via role-bindings.json
// (same UUID = the S2S fallback identity, so the human admin and internal
// service-to-service calls share one super-admin subject). Seeded ONLY if the
// row is absent, so a later password change / disable is never overwritten on
// restart. MustChangePassword forces the operator off the initial password.
const (
	adminUserID  = "266c6a42-6131-4d62-8f39-853e7093701c"
	adminAccount = "admin"
)

var deprecatedSeedRoleIDs = []string{
	"e63e1c88-ad03-11e8-aa06-000c29358ad6", // Organization administrator
	"f06ac18e-ad03-11e8-aa06-000c29358ad6", // Organization auditor
	"00990824-4bf7-11f0-8fa7-865d5643e61f", // Data administrator
	"3fb94948-5169-11f0-b662-3a7bdba2913f", // AI administrator
}

// AdminUserID is the built-in admin user's id, exported so callers can protect
// it — the user-admin API refuses to delete or disable it (deleting the only
// super-admin would lock everyone out). Same UUID as the S2S fallback identity.
const AdminUserID = adminUserID

//go:embed data/roles.json
var rolesJSON []byte

//go:embed data/catalog.json
var catalogJSON []byte

//go:embed data/grants.json
var grantsJSON []byte

//go:embed data/role-bindings.json
var roleBindingsJSON []byte

type roleSeed struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
}

type catalog struct {
	ResourceTypes []catalogResourceType `json:"resource_types"`
}

type catalogResourceType struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// ParentType declares the type this one hangs under ("resource" under
	// "catalog"). Empty for every standalone type (#800).
	ParentType string             `json:"parent_type"`
	Operations []catalogOperation `json:"operations"`
}

type catalogOperation struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// ParentOperation is the operation checked on the parent instance when this
	// one is not granted on the instance itself. Empty = no inheritance.
	ParentOperation string `json:"parent_operation"`
	// Implies lists operations on the SAME type that are granted along with this
	// one and cannot be taken away while it is held (#1121). Empty for almost
	// every operation; see model.Operation.ImpliedOperationIDs.
	Implies []string `json:"implies"`
}

type grantsFile struct {
	Grants []struct {
		RoleID       string   `json:"role_id"`
		ResourceType string   `json:"resource_type"`
		IDPattern    string   `json:"id_pattern"`
		Operations   []string `json:"operations"`
	} `json:"grants"`
}

type roleBindingsFile struct {
	Bindings []struct {
		AccessorID string `json:"accessor_id"`
		RoleID     string `json:"role_id"`
	} `json:"bindings"`
}

// Apply seeds roles + catalog (into GORM) and grants (into Casbin). Idempotent:
// safe to run on every startup. Returns the first error encountered.
func Apply(db *gorm.DB, enforcer *authz.Enforcer) error {
	if err := seedRoles(db); err != nil {
		return fmt.Errorf("seed roles: %w", err)
	}
	if err := seedCatalog(db); err != nil {
		return fmt.Errorf("seed catalog: %w", err)
	}
	if err := reconcileSeedRoles(db, enforcer); err != nil {
		return fmt.Errorf("reconcile seed roles: %w", err)
	}
	if err := renameLegacyOperations(enforcer); err != nil {
		return fmt.Errorf("rename legacy operations: %w", err)
	}
	if err := seedGrants(enforcer); err != nil {
		return fmt.Errorf("seed grants: %w", err)
	}
	if err := backfillImpliedOperations(db, enforcer); err != nil {
		return fmt.Errorf("backfill implied operations: %w", err)
	}
	if err := seedRoleBindings(enforcer); err != nil {
		return fmt.Errorf("seed role bindings: %w", err)
	}
	if err := seedAdminUser(db); err != nil {
		return fmt.Errorf("seed admin user: %w", err)
	}
	return nil
}

// seedAdminUser creates the built-in admin login the FIRST time only. If a row
// with adminUserID already exists it returns without touching it — preserving a
// changed password, cleared MustChangePassword flag, or disabled state across
// restarts. The super-admin role binding is seeded separately (role-bindings.json).
//
// The initial password is BKN_SAFE_INITIAL_PASSWORD when set (deploy generates
// one per install and passes it here); otherwise a random password is generated
// and logged ONCE — there is no baked-in default an attacker could try.
func seedAdminUser(db *gorm.DB) error {
	var count int64
	if err := db.Model(&model.User{}).Where("id = ?", adminUserID).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	pwd := auth.NewInitialPassword()
	hash, err := bcrypt.GenerateFromPassword([]byte(pwd), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if err := db.Create(&model.User{
		ID:                 adminUserID,
		Account:            adminAccount,
		Name:               "Administrator",
		Enabled:            true,
		Source:             model.SourceLocal,
		AccountType:        model.AccountTypeOther,
		PasswordHash:       string(hash),
		MustChangePassword: false,
	}).Error; err != nil {
		return err
	}
	if auth.InitialPasswordEnv == "" {
		// Only chance to learn the generated password: it is stored bcrypt-hashed.
		// A forced change on first login limits its lifetime.
		slog.Warn("seeded built-in admin with a GENERATED initial password (set BKN_SAFE_INITIAL_PASSWORD to control it)",
			"account", adminAccount, "initial_password", pwd)
	}
	return nil
}

func seedRoles(db *gorm.DB) error {
	var roles []roleSeed
	if err := json.Unmarshal(rolesJSON, &roles); err != nil {
		return err
	}
	rows := make([]model.Role, 0, len(roles))
	for _, r := range roles {
		rows = append(rows, model.Role{ID: r.ID, Name: r.Name, Description: r.Description, Source: r.Source})
	}
	// Upsert on primary key so re-seeding refreshes name/description without
	// duplicating, and never changes the (preserved) UUIDs.
	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"name", "description", "source"}),
	}).Create(&rows).Error
}

func reconcileSeedRoles(db *gorm.DB, enforcer *authz.Enforcer) error {
	var roles []roleSeed
	if err := json.Unmarshal(rolesJSON, &roles); err != nil {
		return err
	}

	for _, r := range roles {
		if err := enforcer.RemoveRolePermissions(r.ID); err != nil {
			return err
		}
	}

	for _, roleID := range deprecatedSeedRoleIDs {
		if err := enforcer.RemoveRoleCompletely(roleID); err != nil {
			return err
		}
		if err := db.Delete(&model.Role{}, "id = ?", roleID).Error; err != nil {
			return err
		}
	}
	return nil
}

func seedCatalog(db *gorm.DB) error {
	var c catalog
	if err := json.Unmarshal(catalogJSON, &c); err != nil {
		return err
	}
	// The hierarchy is validated BEFORE anything is written: a typo'd parent type
	// or a parent_operation the parent does not define would otherwise be stored
	// and then silently deny at enforce time, which reads as "the grant does not
	// work" rather than "the catalog is wrong".
	if err := validateImplications(c); err != nil {
		return err
	}
	if err := validateHierarchy(c); err != nil {
		return err
	}
	for _, rt := range c.ResourceTypes {
		rtRow := model.ResourceType{ID: rt.ID, Name: rt.Name, ParentTypeID: rt.ParentType}
		if err := db.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "id"}},
			DoUpdates: clause.AssignmentColumns([]string{"name", "parent_type_id"}),
		}).Create(&rtRow).Error; err != nil {
			return err
		}
		declared := make([]string, 0, len(rt.Operations))
		for _, op := range rt.Operations {
			declared = append(declared, op.ID)
			opRow := model.Operation{
				ResourceTypeID: rt.ID, ID: op.ID, Name: op.Name,
				ParentOperationID:   op.ParentOperation,
				ImpliedOperationIDs: strings.Join(op.Implies, ","),
			}
			if err := db.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "resource_type_id"}, {Name: "id"}},
				DoUpdates: clause.AssignmentColumns([]string{"name", "parent_operation_id", "implied_operation_ids"}),
			}).Create(&opRow).Error; err != nil {
				return err
			}
		}
		// Drop operations this type no longer declares. Upserting alone leaves a
		// withdrawn operation in the vocabulary of every UPGRADED deployment
		// forever — a verb nothing enforces, still offered by the grant console,
		// so an administrator can hand out a permission that decides nothing.
		// A fresh install never shows it, which is what makes it easy to miss.
		//
		// Safe to delete here because this table has exactly one writer: the
		// seed. Nothing self-registers into it, so "not in the file" and "no
		// longer exists" are the same statement.
		if err := deleteUndeclaredOperations(db, rt.ID, declared); err != nil {
			return err
		}
	}
	return nil
}

// deleteUndeclaredOperations removes the operation rows of one resource type
// that the seeded catalog no longer lists. Passing no declared operations
// deletes them all, which is what withdrawing a type's whole vocabulary means.
func deleteUndeclaredOperations(db *gorm.DB, resourceTypeID string, declared []string) error {
	q := db.Where("resource_type_id = ?", resourceTypeID)
	if len(declared) > 0 {
		q = q.Where("id NOT IN ?", declared)
	}
	return q.Delete(&model.Operation{}).Error
}

// validateHierarchy checks the type-level hierarchy declared in catalog.json:
// every parent_type resolves to a declared type, every parent_operation resolves
// to an operation the parent actually defines, an operation may not inherit from
// a type with no parent, and the parent chain is acyclic. All four are authoring
// mistakes in an embedded file, so failing the seed is the right response — the
// alternative is a grant that quietly never applies.
func validateHierarchy(c catalog) error {
	ops := make(map[string]map[string]bool, len(c.ResourceTypes))
	parent := make(map[string]string, len(c.ResourceTypes))
	for _, rt := range c.ResourceTypes {
		set := make(map[string]bool, len(rt.Operations))
		for _, op := range rt.Operations {
			set[op.ID] = true
		}
		ops[rt.ID] = set
		parent[rt.ID] = rt.ParentType
	}
	for _, rt := range c.ResourceTypes {
		if rt.ParentType != "" {
			if _, ok := ops[rt.ParentType]; !ok {
				return fmt.Errorf("resource type %q declares unknown parent_type %q", rt.ID, rt.ParentType)
			}
			if rt.ParentType == rt.ID {
				return fmt.Errorf("resource type %q is its own parent_type", rt.ID)
			}
		}
		for _, op := range rt.Operations {
			if op.ParentOperation == "" {
				continue
			}
			if rt.ParentType == "" {
				return fmt.Errorf("operation %s/%s declares parent_operation %q but %s has no parent_type",
					rt.ID, op.ID, op.ParentOperation, rt.ID)
			}
			if !ops[rt.ParentType][op.ParentOperation] {
				return fmt.Errorf("operation %s/%s maps to %s/%s, which is not a registered operation",
					rt.ID, op.ID, rt.ParentType, op.ParentOperation)
			}
		}
	}
	// Acyclic check: walk each chain with a step budget of the type count. A
	// cycle would make the enforce-time fallback loop forever.
	for id := range parent {
		seen := map[string]bool{id: true}
		for cur, steps := parent[id], 0; cur != ""; cur, steps = parent[cur], steps+1 {
			if seen[cur] || steps > len(parent) {
				return fmt.Errorf("resource type parent chain has a cycle at %q", cur)
			}
			seen[cur] = true
		}
	}
	return nil
}

// validateImplications checks the same-type implications declared in
// catalog.json: every implied operation is declared on the same type, no
// operation implies itself, and the implication graph is acyclic.
//
// The failure this guards against is silent. An implication naming an operation
// the type does not declare would expand a grant into an op no check ever asks
// about, and a cycle would make the expansion at grant time loop; both are
// authoring mistakes in an embedded file, so refusing to boot is the honest
// response.
func validateImplications(c catalog) error {
	for _, rt := range c.ResourceTypes {
		declared := make(map[string]bool, len(rt.Operations))
		implies := make(map[string][]string, len(rt.Operations))
		for _, op := range rt.Operations {
			declared[op.ID] = true
			implies[op.ID] = op.Implies
		}
		for _, op := range rt.Operations {
			for _, implied := range op.Implies {
				if implied == op.ID {
					return fmt.Errorf("operation %s/%s implies itself", rt.ID, op.ID)
				}
				if !declared[implied] {
					return fmt.Errorf("operation %s/%s implies %q, which %s does not declare",
						rt.ID, op.ID, implied, rt.ID)
				}
			}
		}
		// Walk every chain with a step budget of the operation count, the same
		// shape the parent-chain check uses.
		for start := range implies {
			seen := map[string]bool{}
			queue := []string{start}
			for steps := 0; len(queue) > 0; steps++ {
				if steps > len(implies) {
					return fmt.Errorf("operation implication chain has a cycle in %q", rt.ID)
				}
				cur := queue[0]
				queue = queue[1:]
				for _, next := range implies[cur] {
					if next == start {
						return fmt.Errorf("operation implication chain has a cycle at %s/%s", rt.ID, start)
					}
					if seen[next] {
						continue
					}
					seen[next] = true
					queue = append(queue, next)
				}
			}
		}
	}
	return nil
}

// backfillImpliedOperations repairs grants written before an implication was
// declared: every policy row holding an operation now marked as implying others
// gains the implied ones.
//
// The implication is applied when a grant is written, so without this the fix
// would reach new grants only. For catalog.resource_manage that would leave the
// reported defect standing for everyone who had already been granted it — the
// grant reaches nothing on its own, and nothing tells the administrator to
// re-save it. Runs after seedGrants so the rebuilt role matrix is in place, and
// it is idempotent, so a start with nothing to repair costs one filtered read
// per declared implication.
//
// Deliberately NOT symmetric with revocation: this only ever adds. A row that
// an administrator has since narrowed on purpose is repaired back to a usable
// shape rather than left as a permission that answers 403 everywhere.
func backfillImpliedOperations(db *gorm.DB, enforcer *authz.Enforcer) error {
	var c catalog
	if err := json.Unmarshal(catalogJSON, &c); err != nil {
		return err
	}
	trail := audit.New(db)
	for _, rt := range c.ResourceTypes {
		implies := make(map[string][]string, len(rt.Operations))
		for _, op := range rt.Operations {
			if len(op.Implies) > 0 {
				implies[op.ID] = op.Implies
			}
		}
		for holder := range implies {
			// Transitive: an operation implying one that implies another must
			// backfill both. validateImplications has already refused a cycle, and
			// the visited set keeps this terminating regardless.
			seen := map[string]bool{holder: true}
			queue := append([]string(nil), implies[holder]...)
			for len(queue) > 0 {
				implied := queue[0]
				queue = queue[1:]
				if seen[implied] {
					continue
				}
				seen[implied] = true
				queue = append(queue, implies[implied]...)

				added, err := enforcer.BackfillImpliedOperation(rt.ID, holder, implied)
				if err != nil {
					return err
				}
				if len(added) == 0 {
					continue
				}
				slog.Info("backfilled the operation an existing grant implies",
					"resource_type", rt.ID, "holder", holder, "implied", implied, "rows", len(added))
				recordBackfillAudit(trail, rt.ID, holder, implied, added)
			}
		}
	}
	return nil
}

// recordBackfillAudit writes one audit row per repaired grant, under the same
// resource/action the console shows for an operator-issued object grant. A
// permission that appears without anyone having clicked anything is exactly the
// change an auditor asking "why does this account see this catalog" needs to
// find, and a line in the pod log is not somewhere they can look.
//
// Failures are logged and swallowed: auditing must never be the reason a
// deployment fails to start.
func recordBackfillAudit(trail *audit.Store, resourceType, holder, implied string,
	grants []authz.BackfilledGrant) {

	for _, g := range grants {
		detail, err := json.Marshal(map[string]any{
			"accessor_id": g.AccessorID,
			"resource":    map[string]string{"type": resourceType, "id": g.ResourceID},
			"operations":  []string{implied},
			"_reason":     "implied by " + holder,
		})
		if err != nil {
			slog.Error("seed: could not encode backfill audit detail", "err", err)
			continue
		}
		if err := trail.Record(context.Background(), audit.Entry{
			ActorID:  "system:seed",
			Method:   "SYSTEM",
			Resource: "object-grants",
			Action:   "grant",
			TargetID: g.ResourceID,
			Detail:   string(detail),
			Status:   http.StatusOK,
		}); err != nil {
			slog.Error("seed: backfill audit record failed",
				"resource_type", resourceType, "resource_id", g.ResourceID, "err", err)
		}
	}
}

func seedGrants(enforcer *authz.Enforcer) error {
	var g grantsFile
	if err := json.Unmarshal(grantsJSON, &g); err != nil {
		return err
	}
	for _, gr := range g.Grants {
		// Build the object pattern. Empty resource_type => a pure wildcard
		// object (e.g. "*"), used for the super-admin "do everything" grant;
		// otherwise "type:idPattern".
		obj := gr.ResourceType + ":" + gr.IDPattern
		if gr.ResourceType == "" {
			obj = gr.IDPattern
		}
		for _, op := range gr.Operations {
			// AddPolicy is idempotent (no-op if the rule already exists).
			if err := enforcer.Grant(gr.RoleID, obj, op); err != nil {
				return err
			}
		}
	}
	return nil
}

// seedRoleBindings binds accessors (users/apps) to roles via Casbin's grouping
// policy. Notably binds the admin UUID — backend services' tokenless S2S
// fallback identity to the super administrator, so internal /in/v1 calls pass FilterResources
// (replicates ISF's super-admin grant). AssignRole is idempotent.
func seedRoleBindings(enforcer *authz.Enforcer) error {
	var rb roleBindingsFile
	if err := json.Unmarshal(roleBindingsJSON, &rb); err != nil {
		return err
	}
	for _, b := range rb.Bindings {
		if err := enforcer.AssignRole(b.AccessorID, b.RoleID); err != nil {
			return err
		}
	}
	return nil
}

// legacyOperationRenames are vocabulary spellings that were corrected after
// they had already shipped. Role grants need no migration — the seed wipes and
// rebuilds those every start — but a grant an administrator wrote on a single
// object keeps the old string in the policy store, and after the rename it
// matches nothing. That reads as "the permission I granted disappeared", so the
// rows are moved rather than left behind.
var legacyOperationRenames = []struct {
	resourceType string
	from, to     string
}{
	// Data query was spelled data_query on these two types and query_data on
	// catalog/resource. One vocabulary, one spelling.
	{"knowledge_network", "data_query", "query_data"},
	{"stream_data_pipeline", "data_query", "query_data"},
}

// renameLegacyOperations migrates object grants onto the corrected spellings.
// Idempotent: once nothing holds the old spelling it is a no-op, so it costs one
// filtered read per entry on every subsequent start.
func renameLegacyOperations(enforcer *authz.Enforcer) error {
	for _, r := range legacyOperationRenames {
		moved, err := enforcer.RenameOperation(r.resourceType, r.from, r.to)
		if err != nil {
			return err
		}
		if moved > 0 {
			slog.Info("migrated object grants onto the corrected operation name",
				"resource_type", r.resourceType, "from", r.from, "to", r.to, "rows", moved)
		}
	}
	return nil
}
