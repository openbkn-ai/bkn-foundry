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
	"errors"
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

	// BusinessProvenanceOwnerID is the non-login application identity that owns
	// the deployment-managed business provenance Agent in bkn-agent.
	BusinessProvenanceOwnerID      = "bdd59f76-19c3-58b0-bf5f-082c4c3cbddb"
	BusinessProvenanceOwnerAccount = "openbkn-business-provenance"
	BusinessProvenanceOwnerName    = "OpenBKN Business Provenance Service"
)

var deprecatedSeedRoleIDs = []string{
	"e63e1c88-ad03-11e8-aa06-000c29358ad6", // Organization administrator
	"f06ac18e-ad03-11e8-aa06-000c29358ad6", // Organization auditor
	"00990824-4bf7-11f0-8fa7-865d5643e61f", // Data administrator
	"3fb94948-5169-11f0-b662-3a7bdba2913f", // AI administrator
}

var withdrawnResourceTypes = []string{
	"stream_data_pipeline",
}

var withdrawnOperations = []struct {
	resourceType string
	operation    string
}{
	{"connector_type", "task_manage"},
}

const normalUserRoleID = "b5f9ac3e-992c-4bbd-8126-95e87e51c46e"

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
	// Requires lists direct prerequisites on the SAME type. They are enforced at
	// runtime and normalized into allow writes. The first version permits one
	// layer only; a required operation may not declare its own requirements.
	Requires []string `json:"requires"`
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
	if err := reconcileWithdrawnResourceTypes(enforcer); err != nil {
		return fmt.Errorf("reconcile withdrawn resource types: %w", err)
	}
	if err := reconcileWithdrawnOperations(enforcer); err != nil {
		return fmt.Errorf("reconcile withdrawn operations: %w", err)
	}
	if err := seedCatalog(db); err != nil {
		return fmt.Errorf("seed catalog: %w", err)
	}
	if err := reconcileDeprecatedSeedRoles(db, enforcer); err != nil {
		return fmt.Errorf("reconcile deprecated seed roles: %w", err)
	}
	if err := ReconcileWithdrawnNormalUserRole(db, enforcer); err != nil {
		return fmt.Errorf("reconcile withdrawn normal user role: %w", err)
	}
	if err := reconcileSeedRoles(enforcer); err != nil {
		return fmt.Errorf("reconcile seed roles: %w", err)
	}
	if err := renameLegacyOperations(enforcer); err != nil {
		return fmt.Errorf("rename legacy operations: %w", err)
	}
	if err := seedGrants(enforcer); err != nil {
		return fmt.Errorf("seed grants: %w", err)
	}
	if err := backfillRequiredOperations(db, enforcer); err != nil {
		return fmt.Errorf("backfill required operations: %w", err)
	}
	if err := seedRoleBindings(enforcer); err != nil {
		return fmt.Errorf("seed role bindings: %w", err)
	}
	if err := seedAdminUser(db); err != nil {
		return fmt.Errorf("seed admin user: %w", err)
	}
	if err := seedBusinessProvenanceOwner(db); err != nil {
		return fmt.Errorf("seed business provenance owner: %w", err)
	}
	return nil
}

// reconcileWithdrawnOperations removes stale grants before pruning their
// catalog rows. Leaving those grants behind would keep an invisible permission
// active in the policy engine on upgraded deployments.
func reconcileWithdrawnOperations(enforcer *authz.Enforcer) error {
	return enforcer.Transaction(context.Background(), func(tx *authz.PolicyTransaction) error {
		for _, withdrawn := range withdrawnOperations {
			removed, err := tx.RemovePoliciesForOperation(withdrawn.resourceType, withdrawn.operation)
			if err != nil {
				return err
			}
			if removed > 0 {
				slog.Info("removed policies for withdrawn operation",
					"resource_type", withdrawn.resourceType,
					"operation", withdrawn.operation,
					"projections", removed,
				)
			}
		}
		return nil
	})
}

// reconcileWithdrawnResourceTypes removes resource types that are no longer
// supported, including every persisted role/object grant that could otherwise
// survive an upgrade without a matching catalog entry. Audit records are kept.
func reconcileWithdrawnResourceTypes(enforcer *authz.Enforcer) error {
	return enforcer.Transaction(context.Background(), func(tx *authz.PolicyTransaction) error {
		removedPolicies, err := tx.RemovePoliciesForResourceTypes(withdrawnResourceTypes...)
		if err != nil {
			return err
		}
		db := tx.DB()
		if err := db.Where("resource_type_id IN ? OR parent_type_id IN ?", withdrawnResourceTypes, withdrawnResourceTypes).
			Delete(&model.ResourceParent{}).Error; err != nil {
			return err
		}
		if err := db.Where("resource_type_id IN ?", withdrawnResourceTypes).Delete(&model.Operation{}).Error; err != nil {
			return err
		}
		if err := db.Where("id IN ?", withdrawnResourceTypes).Delete(&model.ResourceType{}).Error; err != nil {
			return err
		}
		if removedPolicies > 0 {
			slog.Info("removed policies for withdrawn resource types", "resource_types", withdrawnResourceTypes, "projections", removedPolicies)
		}
		return nil
	})
}

func reconcileDeprecatedSeedRoles(db *gorm.DB, enforcer *authz.Enforcer) error {
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

// ReconcileWithdrawnNormalUserRole removes the withdrawn normal_user role and
// every Casbin policy or membership bound to it. It is idempotent and logs only
// when a persisted role, binding, or policy is actually removed.
func ReconcileWithdrawnNormalUserRole(db *gorm.DB, enforcer *authz.Enforcer) error {
	var roleCount, bindingCount, policyCount int64
	if err := db.Model(&model.Role{}).Where("id = ?", normalUserRoleID).Count(&roleCount).Error; err != nil {
		return err
	}
	if err := db.Table("casbin_rule").Where("ptype = ? AND v1 = ?", "g", normalUserRoleID).Count(&bindingCount).Error; err != nil {
		return err
	}
	if err := db.Table("casbin_rule").Where("ptype = ? AND v0 = ?", "p", normalUserRoleID).Count(&policyCount).Error; err != nil {
		return err
	}

	if err := enforcer.RemoveRoleCompletely(normalUserRoleID); err != nil {
		return err
	}
	if err := db.Delete(&model.Role{}, "id = ?", normalUserRoleID).Error; err != nil {
		return err
	}
	if roleCount+bindingCount+policyCount > 0 {
		slog.Info("removed withdrawn built-in role",
			"role_id", normalUserRoleID,
			"roles", roleCount,
			"bindings", bindingCount,
			"policies", policyCount,
		)
	}
	return nil
}

func seedBusinessProvenanceOwner(db *gorm.DB) error {
	var existing model.User
	err := db.Where(
		"id = ? OR account = ?", BusinessProvenanceOwnerID, BusinessProvenanceOwnerAccount,
	).First(&existing).Error
	if err == nil {
		if existing.ID != BusinessProvenanceOwnerID ||
			existing.Account != BusinessProvenanceOwnerAccount ||
			existing.Name != BusinessProvenanceOwnerName ||
			!existing.Enabled ||
			existing.Source != model.SourceLocal ||
			existing.AccountType != model.AccountTypeApp ||
			existing.PasswordHash != "" {
			return fmt.Errorf("fixed app principal %s conflicts with the deployment contract", BusinessProvenanceOwnerID)
		}
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return db.Create(&model.User{
		ID:          BusinessProvenanceOwnerID,
		Account:     BusinessProvenanceOwnerAccount,
		Name:        BusinessProvenanceOwnerName,
		Enabled:     true,
		Source:      model.SourceLocal,
		AccountType: model.AccountTypeApp,
	}).Error
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

func reconcileSeedRoles(enforcer *authz.Enforcer) error {
	var roles []roleSeed
	if err := json.Unmarshal(rolesJSON, &roles); err != nil {
		return err
	}
	return enforcer.Transaction(context.Background(), func(tx *authz.PolicyTransaction) error {
		for _, r := range roles {
			if err := tx.RemoveSeedRolePermissions(r.ID); err != nil {
				return err
			}
		}
		return nil
	})
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
	if err := validateRequirements(c); err != nil {
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
				ParentOperationID:    op.ParentOperation,
				RequiredOperationIDs: strings.Join(op.Requires, ","),
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

// validateRequirements checks the same-type prerequisites declared in
// catalog.json: every requirement is declared on the same type, is not the
// target itself, and does not itself declare requirements. The last rule keeps
// the first implementation strictly one layer instead of silently calculating
// only part of a dependency graph.
//
// The failure this guards against is silent. A requirement naming an operation
// the type does not declare would expand a grant into an op no check ever asks
// about, while a multi-level declaration would make a one-layer runtime result
// incomplete. Both are authoring mistakes in an embedded file, so refusing to
// boot is the honest response.
func validateRequirements(c catalog) error {
	for _, rt := range c.ResourceTypes {
		declared := make(map[string]bool, len(rt.Operations))
		requires := make(map[string][]string, len(rt.Operations))
		for _, op := range rt.Operations {
			declared[op.ID] = true
			requires[op.ID] = op.Requires
		}
		for _, op := range rt.Operations {
			for _, required := range op.Requires {
				if required == op.ID {
					return fmt.Errorf("operation %s/%s requires itself", rt.ID, op.ID)
				}
				if !declared[required] {
					return fmt.Errorf("operation %s/%s requires %q, which %s does not declare",
						rt.ID, op.ID, required, rt.ID)
				}
				if len(requires[required]) > 0 {
					return fmt.Errorf("operation %s/%s requires %s/%s, which itself declares requires",
						rt.ID, op.ID, rt.ID, required)
				}
			}
		}
	}
	return nil
}

// backfillRequiredOperations repairs grants written before a prerequisite was
// declared: every eligible allow holding an operation gains its direct required
// operations. Legacy and logical bundles keep their compatibility contracts;
// runtime requires still prevents either from bypassing a later deny.
//
// The requirement is normalized when a grant is written, so without this the fix
// would reach new grants only. For catalog.resource_manage that would leave the
// reported defect standing for everyone who had already been granted it — the
// grant reaches nothing on its own, and nothing tells the administrator to
// re-save it. Runs after seedGrants so the rebuilt role matrix is in place, and
// it is idempotent, so a start with nothing to repair costs one filtered read
// per declared requirement.
//
// Deliberately NOT symmetric with revocation: this only ever adds. A row that
// an administrator has since narrowed on purpose is repaired back to a usable
// shape rather than left as a permission that answers 403 everywhere.
func backfillRequiredOperations(db *gorm.DB, enforcer *authz.Enforcer) error {
	var c catalog
	if err := json.Unmarshal(catalogJSON, &c); err != nil {
		return err
	}
	trail := audit.New(db)
	// Role ids up front: the audit vocabulary for a role's permission differs
	// from an object grant's, and telling them apart per repaired row would be a
	// query each.
	roleNames, err := roleNameByID(db)
	if err != nil {
		return err
	}
	for _, rt := range c.ResourceTypes {
		for _, op := range rt.Operations {
			for _, required := range op.Requires {
				added, err := enforcer.BackfillRequiredOperation(rt.ID, op.ID, required)
				if err != nil {
					return err
				}
				if len(added) == 0 {
					continue
				}
				slog.Info("backfilled a direct operation requirement",
					"resource_type", rt.ID, "operation", op.ID, "required", required, "rows", len(added))
				recordBackfillAudit(trail, roleNames, rt.ID, op.ID, required, added)
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
func recordBackfillAudit(trail *audit.Store, roleNames map[string]string,
	resourceType, operation, required string, grants []authz.BackfilledGrant) {

	for _, g := range grants {
		detail, err := json.Marshal(map[string]any{
			"accessor_id": g.AccessorID,
			"resource":    map[string]string{"type": resourceType, "id": g.ResourceID},
			"operations":  []string{required},
			"_reason":     "required by " + operation,
		})
		if err != nil {
			slog.Error("seed: could not encode backfill audit detail", "err", err)
			continue
		}
		entry := audit.Entry{
			ActorID: "system:seed",
			Method:  "SYSTEM",
			Detail:  string(detail),
			Status:  http.StatusOK,
		}
		applyBackfillAuditTarget(&entry, roleNames, g)
		if err := trail.Record(context.Background(), entry); err != nil {
			slog.Error("seed: backfill audit record failed",
				"resource_type", resourceType, "resource_id", g.ResourceID, "err", err)
		}
	}
}

// applyBackfillAuditTarget labels one repaired row with the vocabulary of the
// surface that can actually show it. Getting this wrong is not cosmetic: an
// audit row naming a surface where the grant is absent sends the auditor
// looking in a list that will never contain it.
//
//   - a role subject is a role permission, which the console records as
//     roles/grant_permission against the ROLE, not the resource;
//   - a concrete grant to a real account is an object grant, and lands next to
//     the operator-issued grant of the same object;
//   - everything else — the public accessor, and type-wide "type:*" rows — is
//     excluded from the object-grant list by construction, so it is labelled as
//     what it is: a raw policy row, visible through GET /admin/policies.
func applyBackfillAuditTarget(entry *audit.Entry, roleNames map[string]string, g authz.BackfilledGrant) {
	if name, isRole := roleNames[g.AccessorID]; isRole {
		entry.Resource = "roles"
		entry.Action = "grant_permission"
		entry.TargetID = g.AccessorID
		entry.TargetName = name
		return
	}
	entry.TargetID = g.ResourceID
	entry.Action = "grant"
	if g.ResourceID == "*" || g.AccessorID == authz.PublicAccessorID {
		entry.Resource = "policies"
		return
	}
	entry.Resource = "object-grants"
}

// roleNameByID loads the role id -> name map used to tell a role subject from
// an accessor. Roles are few and this runs once per start.
func roleNameByID(db *gorm.DB) (map[string]string, error) {
	var roles []model.Role
	if err := db.Find(&roles).Error; err != nil {
		return nil, err
	}
	out := make(map[string]string, len(roles))
	for _, r := range roles {
		out[r.ID] = r.Name
	}
	return out, nil
}

func seedGrants(enforcer *authz.Enforcer) error {
	var g grantsFile
	if err := json.Unmarshal(grantsJSON, &g); err != nil {
		return err
	}
	return enforcer.Transaction(context.Background(), func(tx *authz.PolicyTransaction) error {
		for _, gr := range g.Grants {
			// Build the object pattern. Empty resource_type => a pure wildcard
			// object (e.g. "*"), used for the super-admin "do everything" grant;
			// otherwise "type:idPattern".
			object := gr.ResourceType + ":" + gr.IDPattern
			if gr.ResourceType == "" {
				object = gr.IDPattern
			}
			for _, operation := range gr.Operations {
				if err := tx.GrantSeedPolicy(gr.RoleID, object, operation); err != nil {
					return err
				}
			}
		}
		return nil
	})
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
	// Data query was spelled data_query on knowledge networks and query_data on
	// catalog/resource. One vocabulary, one spelling.
	{"knowledge_network", "data_query", "query_data"},
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
