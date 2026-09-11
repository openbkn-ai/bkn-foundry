// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package migrationcontract defines the durable receipt shared by bkn-safe's
// runtime startup gate and the release-owned offline migration tool.
package migrationcontract

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

const (
	CurrentVersion = "authz-edition-boundaries-v1"

	EETableAbsent          = "absent"
	EETablePresentEmpty    = "present_empty"
	EETablePresentWithRows = "present_with_rows"
)

// Marker is the durable receipt for the one-time four-edition authorization
// migration. This contract remains in bkn-safe because every process validates
// it before accepting traffic; the code that creates an upgrade receipt lives
// in the target release's deploy migration package.
type Marker struct {
	Version               string `gorm:"primaryKey;size:64"`
	EETableState          string `gorm:"size:32;not null"`
	CorePolicyCount       int64
	CoreGrantCount        int64
	CoreSourceSummary     string `gorm:"type:text;not null"`
	EERowCount            int64
	EEPublishedCount      int64
	EEDormantCount        int64
	EEInvalidCount        int64
	EEActiveCount         int64
	EEDenyCount           int64
	EEInventoryDigest     string `gorm:"size:64"`
	EEAssemblyEvidenceRef string `gorm:"size:512"`
	ActivatedGrantIDs     string `gorm:"type:text;not null"`
	ActivationConfirmedBy string `gorm:"size:128"`
	ActivationEvidenceRef string `gorm:"size:512"`
	Checksum              string `gorm:"size:64;not null"`
	AppliedAt             time.Time
}

func (Marker) TableName() string { return "authorization_migration_marker" }

// Seal returns a copy carrying the checksum for all semantic receipt fields.
func (m Marker) Seal() Marker {
	m.Checksum = m.ExpectedChecksum()
	return m
}

// Valid reports whether the receipt is the current, untampered contract.
func (m Marker) Valid() bool {
	return m.Version == CurrentVersion && m.Checksum != "" && m.Checksum == m.ExpectedChecksum()
}

// ExpectedChecksum computes the stable receipt checksum used by both writer
// and runtime reader.
func (m Marker) ExpectedChecksum() string {
	payload := strings.Join([]string{
		m.Version, m.EETableState,
		fmt.Sprint(m.CorePolicyCount), fmt.Sprint(m.CoreGrantCount), m.CoreSourceSummary,
		fmt.Sprint(m.EERowCount), fmt.Sprint(m.EEPublishedCount), fmt.Sprint(m.EEDormantCount),
		fmt.Sprint(m.EEInvalidCount), fmt.Sprint(m.EEActiveCount), fmt.Sprint(m.EEDenyCount),
		m.EEInventoryDigest, m.EEAssemblyEvidenceRef, m.ActivatedGrantIDs,
		m.ActivationConfirmedBy, m.ActivationEvidenceRef,
	}, "\x00")
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}
