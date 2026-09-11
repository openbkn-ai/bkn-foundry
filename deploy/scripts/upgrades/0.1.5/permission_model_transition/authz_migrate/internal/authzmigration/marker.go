// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package authzmigration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/migrationcontract"
)

var ErrMigrationMarkerRequired = errors.New("current authorization migration marker required")

// BuildMarker creates the receipt only from fully reconciled reports. Callers
// cannot mark a plan that still has pending writes or anomalies as successful.
func BuildMarker(core CoreReport, enterprise EEReport, confirmation *EEActivationConfirmation, appliedAt time.Time) (migrationcontract.Marker, error) {
	if core.MigrationVersion != CurrentVersion || enterprise.MigrationVersion != CurrentVersion ||
		core.Blocked() || enterprise.Blocked() || core.HasChanges() || enterprise.HasChanges() {
		return migrationcontract.Marker{}, fmt.Errorf("%w: Core or EE report is not fully reconciled", ErrPlanBlocked)
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
		return migrationcontract.Marker{}, err
	}
	activeIDs := make([]string, 0)
	for _, rule := range enterprise.Rules {
		if rule.PlannedActivation == eeActivationActive {
			activeIDs = append(activeIDs, rule.GrantID)
		}
	}
	sort.Strings(activeIDs)
	if err := ValidateActiveGrantConfirmation(enterprise, confirmation); err != nil {
		return migrationcontract.Marker{}, err
	}
	activeJSON, err := json.Marshal(activeIDs)
	if err != nil {
		return migrationcontract.Marker{}, err
	}
	marker := migrationcontract.Marker{
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
	return marker.Seal(), nil
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
func PersistMarker(ctx context.Context, db *gorm.DB, marker migrationcontract.Marker) error {
	if db == nil || !marker.Valid() {
		return fmt.Errorf("%w: invalid marker payload", ErrMigrationMarkerRequired)
	}
	if err := db.WithContext(ctx).AutoMigrate(&migrationcontract.Marker{}); err != nil {
		return fmt.Errorf("prepare authorization migration marker: %w", err)
	}
	var existing migrationcontract.Marker
	err := db.WithContext(ctx).Where("version = ?", CurrentVersion).First(&existing).Error
	if err == nil {
		if !existing.Valid() {
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
