// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package authzgate protects bkn-safe startup after the one-time authorization
// transition. Upgrade planning and data writes deliberately live under deploy.
package authzgate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	safemodel "github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/migrationcontract"
)

var ErrMigrationMarkerRequired = errors.New("current authorization migration marker required")

type casbinPolicyRow struct {
	ID    uint `gorm:"primaryKey;autoIncrement"`
	Ptype string
	V0    string
	V1    string
	V2    string
	V3    string
	V4    string
	V5    string
}

func (casbinPolicyRow) TableName() string { return "casbin_rule" }

type eeRuleRow struct{ ID string }

func (eeRuleRow) TableName() string { return "ee_permobject_rules" }

type policyTuple struct {
	accessor, object, operation, effect, policySource, authoritySource string
}

// IsFreshAuthorizationStore must run before schema migration. A missing Casbin
// table is the only supported fresh-install signal.
func IsFreshAuthorizationStore(db *gorm.DB) bool {
	return db != nil && !db.Migrator().HasTable(&casbinPolicyRow{})
}

// VerifyCurrentMarker rejects missing, stale, malformed, or tampered receipts.
func VerifyCurrentMarker(ctx context.Context, db *gorm.DB) error {
	if db == nil || !db.Migrator().HasTable(&migrationcontract.Marker{}) {
		return ErrMigrationMarkerRequired
	}
	var marker migrationcontract.Marker
	if err := db.WithContext(ctx).Where("version = ?", migrationcontract.CurrentVersion).
		First(&marker).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrMigrationMarkerRequired
		}
		return err
	}
	if !marker.Valid() {
		return fmt.Errorf("%w: marker checksum is invalid", ErrMigrationMarkerRequired)
	}
	switch marker.EETableState {
	case migrationcontract.EETableAbsent,
		migrationcontract.EETablePresentEmpty,
		migrationcontract.EETablePresentWithRows:
		return nil
	default:
		return fmt.Errorf(
			"%w: invalid EE table state %q",
			ErrMigrationMarkerRequired,
			marker.EETableState,
		)
	}
}

// SeedFreshInstallMarker creates a receipt only for a store proven fresh before
// normal schema setup. Existing stores must use the release-owned deploy tool.
func SeedFreshInstallMarker(ctx context.Context, db *gorm.DB, wasFresh bool) error {
	if !wasFresh {
		return VerifyCurrentMarker(ctx, db)
	}
	marker, err := freshInstallMarker(ctx, db)
	if err != nil {
		return err
	}
	if err := persistFreshMarker(ctx, db, marker); err != nil {
		return err
	}
	return VerifyCurrentMarker(ctx, db)
}

func freshInstallMarker(ctx context.Context, db *gorm.DB) (migrationcontract.Marker, error) {
	var policies []casbinPolicyRow
	if db.Migrator().HasTable(&casbinPolicyRow{}) {
		if err := db.WithContext(ctx).Where("ptype = ?", "p").Find(&policies).Error; err != nil {
			return migrationcontract.Marker{}, err
		}
	}
	sources := make(map[string]int)
	policyTuples := make(map[policyTuple]struct{}, len(policies))
	for _, policy := range policies {
		if !validFreshPolicy(policy) {
			return migrationcontract.Marker{}, fmt.Errorf(
				"%w: fresh install contains unclassified Core policies",
				ErrMigrationMarkerRequired,
			)
		}
		sources[policy.V4]++
		tuple := policyTuple{
			policy.V0, policy.V1, policy.V2, policy.V3, policy.V4, policy.V5,
		}
		if _, duplicate := policyTuples[tuple]; duplicate {
			return migrationcontract.Marker{}, fmt.Errorf(
				"%w: fresh install contains duplicate Core projections",
				ErrMigrationMarkerRequired,
			)
		}
		policyTuples[tuple] = struct{}{}
	}
	sourceSummary, err := json.Marshal(sources)
	if err != nil {
		return migrationcontract.Marker{}, err
	}

	var grants []safemodel.AuthorizationGrant
	if db.Migrator().HasTable(&safemodel.AuthorizationGrant{}) {
		if err := db.WithContext(ctx).Find(&grants).Error; err != nil {
			return migrationcontract.Marker{}, err
		}
	}
	grantTuples := make(map[policyTuple]int, len(grants))
	for _, grant := range grants {
		tuple := policyTuple{
			grant.AccessorID, grant.Object, grant.Operation, grant.Effect,
			grant.PolicySource, grant.AuthoritySource,
		}
		if !validFreshGrant(grant, tuple) {
			return migrationcontract.Marker{}, fmt.Errorf(
				"%w: fresh install contains invalid stable grants",
				ErrMigrationMarkerRequired,
			)
		}
		if _, exists := policyTuples[tuple]; !exists {
			return migrationcontract.Marker{}, fmt.Errorf(
				"%w: fresh install contains grants without Core projections",
				ErrMigrationMarkerRequired,
			)
		}
		grantTuples[tuple]++
	}
	for tuple := range policyTuples {
		if grantTuples[tuple] == 0 {
			return migrationcontract.Marker{}, fmt.Errorf(
				"%w: fresh install contains Core policies without stable grants",
				ErrMigrationMarkerRequired,
			)
		}
	}

	eeState := migrationcontract.EETableAbsent
	if db.Migrator().HasTable(&eeRuleRow{}) {
		var count int64
		if err := db.WithContext(ctx).Table("ee_permobject_rules").Count(&count).Error; err != nil {
			return migrationcontract.Marker{}, err
		}
		if count != 0 {
			return migrationcontract.Marker{}, fmt.Errorf(
				"%w: fresh install contains historical EE rules",
				ErrMigrationMarkerRequired,
			)
		}
		eeState = migrationcontract.EETablePresentEmpty
	}

	return migrationcontract.Marker{
		Version:           migrationcontract.CurrentVersion,
		EETableState:      eeState,
		CorePolicyCount:   int64(len(policies)),
		CoreGrantCount:    int64(len(grants)),
		CoreSourceSummary: string(sourceSummary),
		ActivatedGrantIDs: "[]",
		AppliedAt:         time.Now().UTC(),
	}.Seal(), nil
}

func validFreshPolicy(policy casbinPolicyRow) bool {
	if strings.TrimSpace(policy.V0) == "" || strings.TrimSpace(policy.V1) == "" ||
		strings.TrimSpace(policy.V2) == "" {
		return false
	}
	if policy.V3 != authz.EffectAllow && policy.V3 != authz.EffectDeny {
		return false
	}
	if !oneOf(policy.V4,
		string(authz.PolicySourceCommunityBundle),
		string(authz.PolicySourceProfessionalRule),
		string(authz.PolicySourceSystemDerived),
		string(authz.PolicySourceRolePermission),
	) {
		return false
	}
	return oneOf(policy.V5,
		string(authz.AuthoritySourceAdminAuthz),
		string(authz.AuthoritySourceOwnerDelegate),
		string(authz.AuthoritySourceSystem),
	)
}

func validFreshGrant(grant safemodel.AuthorizationGrant, tuple policyTuple) bool {
	if strings.TrimSpace(grant.GrantID) == "" ||
		strings.TrimSpace(grant.CreatedBy) == "" ||
		grant.ProjectionKey != projectionKey(tuple) {
		return false
	}
	return validFreshPolicy(casbinPolicyRow{
		V0: tuple.accessor, V1: tuple.object, V2: tuple.operation,
		V3: tuple.effect, V4: tuple.policySource, V5: tuple.authoritySource,
	})
}

func projectionKey(tuple policyTuple) string {
	payload := strings.Join([]string{
		tuple.accessor,
		tuple.object,
		tuple.operation,
		tuple.effect,
		tuple.policySource,
		tuple.authoritySource,
	}, "\x00")
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

func oneOf(value string, accepted ...string) bool {
	sort.Strings(accepted)
	index := sort.SearchStrings(accepted, value)
	return index < len(accepted) && accepted[index] == value
}

func persistFreshMarker(ctx context.Context, db *gorm.DB, marker migrationcontract.Marker) error {
	if !marker.Valid() {
		return fmt.Errorf("%w: invalid marker payload", ErrMigrationMarkerRequired)
	}
	if err := db.WithContext(ctx).AutoMigrate(&migrationcontract.Marker{}); err != nil {
		return fmt.Errorf("prepare authorization migration marker: %w", err)
	}
	var existing migrationcontract.Marker
	err := db.WithContext(ctx).Where("version = ?", migrationcontract.CurrentVersion).
		First(&existing).Error
	if err == nil {
		if existing.Valid() && existing.Checksum == marker.Checksum {
			return nil
		}
		return fmt.Errorf(
			"%w: current-version marker already contains different data",
			ErrMigrationMarkerRequired,
		)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return db.WithContext(ctx).Create(&marker).Error
}
