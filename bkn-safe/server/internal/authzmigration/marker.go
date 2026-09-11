// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package authzmigration

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
)

var ErrMigrationMarkerRequired = errors.New("current authorization migration marker required")

// IsFreshAuthorizationStore must be called before schema migration. A missing
// Casbin projection table is the unambiguous fresh-install signal; an existing
// but empty historical table still requires the explicit offline workflow.
func IsFreshAuthorizationStore(db *gorm.DB) bool {
	return db != nil && !db.Migrator().HasTable(&casbinPolicyRow{})
}

// BuildMarker creates the receipt only from fully reconciled reports. Callers
// cannot mark a plan that still has pending writes or anomalies as successful.
func BuildMarker(core CoreReport, enterprise EEReport, confirmation *EEActivationConfirmation, appliedAt time.Time) (safemodel.AuthorizationMigrationMarker, error) {
	if core.MigrationVersion != CurrentVersion || enterprise.MigrationVersion != CurrentVersion ||
		core.Blocked() || enterprise.Blocked() || core.HasChanges() || enterprise.HasChanges() {
		return safemodel.AuthorizationMigrationMarker{}, fmt.Errorf("%w: Core or EE report is not fully reconciled", ErrPlanBlocked)
	}
	if appliedAt.IsZero() {
		appliedAt = time.Now().UTC()
	}

	sourceCounts := make(map[string]int)
	for _, policy := range core.Policies {
		if policy.Action != ActionRemove {
			sourceCounts[policy.PlannedPolicySource]++
		}
	}
	sourceSummary, err := json.Marshal(sourceCounts)
	if err != nil {
		return safemodel.AuthorizationMigrationMarker{}, err
	}
	activeIDs := make([]string, 0)
	for _, rule := range enterprise.Rules {
		if rule.PlannedActivation == eeActivationActive {
			activeIDs = append(activeIDs, rule.GrantID)
		}
	}
	sort.Strings(activeIDs)
	if err := ValidateActiveGrantConfirmation(enterprise, confirmation); err != nil {
		return safemodel.AuthorizationMigrationMarker{}, err
	}
	activeJSON, err := json.Marshal(activeIDs)
	if err != nil {
		return safemodel.AuthorizationMigrationMarker{}, err
	}
	marker := safemodel.AuthorizationMigrationMarker{
		Version: CurrentVersion, EETableState: enterprise.TableState,
		CorePolicyCount: int64(len(core.Policies)), CoreGrantCount: int64(core.Summary.GrantsExisting),
		CoreSourceSummary: string(sourceSummary), EERowCount: int64(enterprise.Summary.Rows),
		EEPublishedCount: int64(enterprise.Summary.Published), EEDormantCount: int64(enterprise.Summary.Dormant),
		EEInvalidCount: int64(enterprise.Summary.Invalid), EEActiveCount: int64(enterprise.Summary.Active),
		EEDenyCount: int64(enterprise.Summary.Deny), EEInventoryDigest: enterprise.InventoryDigest,
		EEAssemblyEvidenceRef: enterprise.Assembly.EvidenceRef, ActivatedGrantIDs: string(activeJSON),
		AppliedAt: appliedAt.UTC(),
	}
	if len(activeIDs) > 0 {
		marker.ActivationConfirmedBy = confirmation.OperatorID
		marker.ActivationEvidenceRef = confirmation.EvidenceRef
	}
	marker.Checksum = markerChecksum(marker)
	return marker, nil
}

// ValidateActiveGrantConfirmation is safe to run against a dry-run report. It
// lets the CLI reject an incomplete administrator receipt before either Core
// or EE data is changed.
func ValidateActiveGrantConfirmation(enterprise EEReport, confirmation *EEActivationConfirmation) error {
	activeIDs := make([]string, 0)
	for _, rule := range enterprise.Rules {
		if rule.PlannedActivation == eeActivationActive {
			activeIDs = append(activeIDs, rule.GrantID)
		}
	}
	sort.Strings(activeIDs)
	if len(activeIDs) == 0 {
		return nil
	}
	if confirmation == nil || confirmation.InventoryDigest != enterprise.InventoryDigest ||
		strings.TrimSpace(confirmation.OperatorID) == "" || strings.TrimSpace(confirmation.EvidenceRef) == "" ||
		confirmation.ConfirmedAt.IsZero() || !sameSortedIDs(activeIDs, confirmation.ConfirmedGrantIDs) {
		return fmt.Errorf("%w: active EE grants are not covered by an exact administrator confirmation", ErrPlanBlocked)
	}
	return nil
}

func sameSortedIDs(expected, actual []string) bool {
	if len(expected) != len(actual) {
		return false
	}
	copyOfActual := append([]string(nil), actual...)
	sort.Strings(copyOfActual)
	for i := range expected {
		if expected[i] != copyOfActual[i] || (i > 0 && copyOfActual[i] == copyOfActual[i-1]) {
			return false
		}
	}
	return true
}

// PersistMarker is idempotent for the same semantic receipt. It refuses to
// overwrite a corrupted current-version marker.
func PersistMarker(ctx context.Context, db *gorm.DB, marker safemodel.AuthorizationMigrationMarker) error {
	if db == nil || marker.Version != CurrentVersion || marker.Checksum != markerChecksum(marker) {
		return fmt.Errorf("%w: invalid marker payload", ErrMigrationMarkerRequired)
	}
	if err := db.WithContext(ctx).AutoMigrate(&safemodel.AuthorizationMigrationMarker{}); err != nil {
		return fmt.Errorf("prepare authorization migration marker: %w", err)
	}
	var existing safemodel.AuthorizationMigrationMarker
	err := db.WithContext(ctx).Where("version = ?", CurrentVersion).First(&existing).Error
	if err == nil {
		if existing.Checksum != markerChecksum(existing) {
			return fmt.Errorf("%w: stored marker checksum is invalid", ErrMigrationMarkerRequired)
		}
		if existing.Checksum == marker.Checksum {
			return nil
		}
		return fmt.Errorf("%w: current-version marker already records a different completed migration", ErrMigrationMarkerRequired)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return db.WithContext(ctx).Create(&marker).Error
}

// VerifyCurrentMarker is the new-binary startup gate. It deliberately checks
// the completed receipt, not the live licence edition: edition changes select
// retained layers and do not rewrite authorization data.
func VerifyCurrentMarker(ctx context.Context, db *gorm.DB) error {
	if db == nil || !db.Migrator().HasTable(&safemodel.AuthorizationMigrationMarker{}) {
		return ErrMigrationMarkerRequired
	}
	var marker safemodel.AuthorizationMigrationMarker
	if err := db.WithContext(ctx).Where("version = ?", CurrentVersion).First(&marker).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrMigrationMarkerRequired
		}
		return err
	}
	if marker.Checksum == "" || marker.Checksum != markerChecksum(marker) {
		return fmt.Errorf("%w: marker checksum is invalid", ErrMigrationMarkerRequired)
	}
	switch marker.EETableState {
	case EETableAbsent, EETablePresentEmpty, EETablePresentWithRows:
		return nil
	default:
		return fmt.Errorf("%w: invalid EE table state %q", ErrMigrationMarkerRequired, marker.EETableState)
	}
}

// SeedFreshInstallMarker writes the same current receipt as the offline tool,
// but only for a database that had no Casbin table before startup. The caller
// supplies that pre-migration fact so an old empty store is never guessed fresh.
func SeedFreshInstallMarker(ctx context.Context, db *gorm.DB, wasFresh bool) error {
	if !wasFresh {
		return VerifyCurrentMarker(ctx, db)
	}
	core, err := PlanCore(ctx, db, nil)
	if err != nil {
		return fmt.Errorf("fresh install Core authorization inventory: %w", err)
	}
	enterprise, err := PlanEnterprise(ctx, db, EEOptions{})
	if err != nil {
		return fmt.Errorf("fresh install EE authorization inventory: %w", err)
	}
	if core.HasChanges() {
		return fmt.Errorf("%w: fresh install contains unclassified Core policies", ErrMigrationMarkerRequired)
	}
	if enterprise.TableState == EETablePresentWithRows {
		return fmt.Errorf("%w: fresh install contains historical EE rules", ErrMigrationMarkerRequired)
	}
	marker, err := BuildMarker(core, enterprise, nil, time.Now().UTC())
	if err != nil {
		return err
	}
	if err := PersistMarker(ctx, db, marker); err != nil {
		return err
	}
	return VerifyCurrentMarker(ctx, db)
}

func markerChecksum(marker safemodel.AuthorizationMigrationMarker) string {
	payload := strings.Join([]string{
		marker.Version, marker.EETableState,
		fmt.Sprint(marker.CorePolicyCount), fmt.Sprint(marker.CoreGrantCount), marker.CoreSourceSummary,
		fmt.Sprint(marker.EERowCount), fmt.Sprint(marker.EEPublishedCount), fmt.Sprint(marker.EEDormantCount),
		fmt.Sprint(marker.EEInvalidCount), fmt.Sprint(marker.EEActiveCount), fmt.Sprint(marker.EEDenyCount),
		marker.EEInventoryDigest, marker.EEAssemblyEvidenceRef, marker.ActivatedGrantIDs,
		marker.ActivationConfirmedBy, marker.ActivationEvidenceRef,
	}, "\x00")
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

// ReconciledReports inventories the already-applied stores for marker
// creation. It is shared by the CLI after ApplyCore/ApplyEnterprise.
func ReconciledReports(ctx context.Context, db *gorm.DB, evidence []LifecycleEvidence, eeOpts EEOptions) (CoreReport, EEReport, error) {
	core, err := PlanCore(ctx, db, evidence)
	if err != nil {
		return core, EEReport{}, err
	}
	enterprise, err := PlanEnterprise(ctx, db, eeOpts)
	if err != nil {
		return core, enterprise, err
	}
	if core.HasChanges() || enterprise.HasChanges() {
		return core, enterprise, fmt.Errorf("%w: stores are not fully reconciled", ErrPlanBlocked)
	}
	return core, enterprise, nil
}

// Keep authz imported here as a compile-time assertion that a fresh seed's
// logical bundle vocabulary is the same one the migration report records.
var _ = authz.ActFullBusinessAccess
