// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

var ErrSourceCoverageVersionConflict = errors.New("source coverage version conflict")

func (s *Store) Get(ctx context.Context, sourceID, deploymentID string) (observabilityvo.SourceCoverage, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT source_id, deployment_id, coverage_state, reason, dropped_records,
		       first_observed_at, last_observed_at, recovered_at, row_version
		FROM bkn_trace_log_source_coverage
		WHERE source_id=? AND deployment_id=?`, sourceID, deploymentID)
	coverage, err := scanSourceCoverage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return observabilityvo.SourceCoverage{}, false, nil
	}
	if err != nil {
		return observabilityvo.SourceCoverage{}, false, err
	}
	return coverage, true, nil
}

func (s *Store) UpsertDegraded(ctx context.Context, coverage observabilityvo.SourceCoverage) error {
	if coverage.SourceID == "" || coverage.DeploymentID == "" {
		return errors.New("source coverage source_id and deployment_id are required")
	}
	if coverage.State != observabilityvo.SourceCoverageDegraded || coverage.Reason != "telemetry_dropped" {
		return errors.New("source coverage degradation must be telemetry_dropped")
	}
	if coverage.DroppedRecords <= 0 {
		return errors.New("source coverage dropped_records must be positive")
	}
	if coverage.FirstObservedAt.IsZero() {
		coverage.FirstObservedAt = time.Now().UTC()
	}
	if coverage.LastObservedAt.IsZero() {
		coverage.LastObservedAt = coverage.FirstObservedAt
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO bkn_trace_log_source_coverage (
			source_id, deployment_id, coverage_state, reason, dropped_records,
			first_observed_at, last_observed_at, recovered_at, row_version, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, NULL, 1, UTC_TIMESTAMP(6))
		ON DUPLICATE KEY UPDATE
			coverage_state=VALUES(coverage_state),
			reason=VALUES(reason),
			dropped_records=GREATEST(dropped_records, VALUES(dropped_records)),
			first_observed_at=IF(coverage_state='healthy' OR first_observed_at IS NULL, VALUES(first_observed_at), first_observed_at),
			last_observed_at=GREATEST(COALESCE(last_observed_at, VALUES(last_observed_at)), VALUES(last_observed_at)),
			recovered_at=NULL,
			row_version=row_version+1,
			updated_at=UTC_TIMESTAMP(6)`,
		coverage.SourceID, coverage.DeploymentID, coverage.State, coverage.Reason, coverage.DroppedRecords,
		coverage.FirstObservedAt.UTC(), coverage.LastObservedAt.UTC(),
	)
	if err != nil {
		return fmt.Errorf("upsert source coverage: %w", err)
	}
	return nil
}

func (s *Store) MarkHealthyAfterCatchUp(ctx context.Context, sourceID, deploymentID string, version uint64) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE bkn_trace_log_source_coverage
		SET coverage_state=?, reason='', recovered_at=UTC_TIMESTAMP(6),
		    row_version=row_version+1, updated_at=UTC_TIMESTAMP(6)
		WHERE source_id=? AND deployment_id=? AND row_version=?`,
		observabilityvo.SourceCoverageHealthy, sourceID, deploymentID, version,
	)
	if err != nil {
		return fmt.Errorf("recover source coverage: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect source coverage recovery: %w", err)
	}
	if updated != 1 {
		return ErrSourceCoverageVersionConflict
	}
	return nil
}

type sourceCoverageRow interface {
	Scan(dest ...any) error
}

func scanSourceCoverage(row sourceCoverageRow) (observabilityvo.SourceCoverage, error) {
	var coverage observabilityvo.SourceCoverage
	var firstObservedAt, lastObservedAt, recoveredAt sql.NullTime
	if err := row.Scan(
		&coverage.SourceID, &coverage.DeploymentID, &coverage.State, &coverage.Reason, &coverage.DroppedRecords,
		&firstObservedAt, &lastObservedAt, &recoveredAt, &coverage.Version,
	); err != nil {
		return observabilityvo.SourceCoverage{}, err
	}
	if firstObservedAt.Valid {
		coverage.FirstObservedAt = firstObservedAt.Time.UTC()
	}
	if lastObservedAt.Valid {
		coverage.LastObservedAt = lastObservedAt.Time.UTC()
	}
	if recoveredAt.Valid {
		value := recoveredAt.Time.UTC()
		coverage.RecoveredAt = &value
	}
	return coverage, nil
}
