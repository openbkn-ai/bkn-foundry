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

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"bkn-safe/internal/authz"
	"bkn-safe/internal/model"
)

//go:embed data/roles.json
var rolesJSON []byte

//go:embed data/catalog.json
var catalogJSON []byte

//go:embed data/grants.json
var grantsJSON []byte

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
		for _, op := range gr.Operations {
			// AddPolicy is idempotent (no-op if the rule already exists).
			if err := enforcer.GrantRolePermission(gr.RoleID, gr.ResourceType, gr.IDPattern, op); err != nil {
				return err
			}
		}
	}
	return nil
}
