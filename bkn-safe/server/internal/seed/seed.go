// Package seed centrally initialises authorization data at bkn-safe startup:
// roles, the resource-type/operation catalog, and role->permission grants.
//
// This replaces ISF's scattered initialisation (authorization service startup
// seed + each module's HTTP resource_type registration + DA InitPermission) with
// one idempotent seed in one service — no cross-service registration, no boot
// ordering. Role UUIDs are preserved (see data/roles.json).
package seed

import (
	_ "embed"
	"encoding/json"
	"fmt"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"bkn-safe/internal/auth"
	"bkn-safe/internal/authz"
	"bkn-safe/internal/model"
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

// adminInitialPwd is the platform initial password (shared single source).
// Not a const: auth.DefaultInitialPassword is a runtime var (env-overridable).
var adminInitialPwd = auth.DefaultInitialPassword

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
	ResourceTypes []struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Operations []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"operations"`
	} `json:"resource_types"`
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
	if err := seedGrants(enforcer); err != nil {
		return fmt.Errorf("seed grants: %w", err)
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
func seedAdminUser(db *gorm.DB) error {
	var count int64
	if err := db.Model(&model.User{}).Where("id = ?", adminUserID).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(adminInitialPwd), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return db.Create(&model.User{
		ID:                 adminUserID,
		Account:            adminAccount,
		Name:               "Administrator",
		Enabled:            true,
		Source:             model.SourceLocal,
		AccountType:        model.AccountTypeOther,
		PasswordHash:       string(hash),
		MustChangePassword: true,
	}).Error
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

func seedCatalog(db *gorm.DB) error {
	var c catalog
	if err := json.Unmarshal(catalogJSON, &c); err != nil {
		return err
	}
	for _, rt := range c.ResourceTypes {
		rtRow := model.ResourceType{ID: rt.ID, Name: rt.Name}
		if err := db.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "id"}},
			DoUpdates: clause.AssignmentColumns([]string{"name"}),
		}).Create(&rtRow).Error; err != nil {
			return err
		}
		for _, op := range rt.Operations {
			opRow := model.Operation{ResourceTypeID: rt.ID, ID: op.ID, Name: op.Name}
			if err := db.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "resource_type_id"}, {Name: "id"}},
				DoUpdates: clause.AssignmentColumns([]string{"name"}),
			}).Create(&opRow).Error; err != nil {
				return err
			}
		}
	}
	return nil
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
// fallback identity — to 超级管理员, so internal /in/v1 calls pass FilterResources
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
