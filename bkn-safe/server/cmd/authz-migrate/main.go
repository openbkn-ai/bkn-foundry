// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Command authz-migrate is the bkn-safe domain step invoked by the target
// release's deploy migration entry. It always preflights Core and EE before the
// first write and emits a machine-readable JSON report.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/config"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authzmigration"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/database"
	safemodel "github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

const (
	modeDryRun = "dry-run"
	modeApply  = "apply"
)

type manifest struct {
	LifecycleEvidence []authzmigration.LifecycleEvidence `json:"lifecycle_evidence,omitempty"`
	EE                struct {
		Assembly     authzmigration.EEAssemblyEvidence        `json:"assembly"`
		RuleEvidence []authzmigration.EERuleEvidence          `json:"rule_evidence,omitempty"`
		Activation   *authzmigration.EEActivationConfirmation `json:"activation,omitempty"`
	} `json:"ee"`
}

func (m manifest) eeOptions(now time.Time) authzmigration.EEOptions {
	return authzmigration.EEOptions{
		Now: now, Assembly: m.EE.Assembly, RuleEvidence: m.EE.RuleEvidence,
		Activation: m.EE.Activation,
	}
}

type commandReport struct {
	Mode        string                                  `json:"mode"`
	GeneratedAt time.Time                               `json:"generated_at"`
	Core        authzmigration.CoreReport               `json:"core"`
	Enterprise  authzmigration.EEReport                 `json:"enterprise"`
	Marker      *safemodel.AuthorizationMigrationMarker `json:"marker,omitempty"`
	Error       string                                  `json:"error,omitempty"`
}

func main() {
	mode := flag.String("mode", modeDryRun, "dry-run or apply")
	configPath := flag.String("config", "", "bkn-safe YAML config; normal SAFE_* environment resolution is used when empty")
	manifestPath := flag.String("manifest", "", "review evidence and optional administrator activation confirmation JSON")
	flag.Parse()

	now := time.Now().UTC()
	report, err := execute(context.Background(), *mode, *configPath, *manifestPath, now)
	if err != nil {
		report.Error = err.Error()
	}
	if encodeErr := writeReport(os.Stdout, report); encodeErr != nil {
		fmt.Fprintln(os.Stderr, "encode migration report:", encodeErr)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "authorization migration failed:", err)
		os.Exit(1)
	}
}

func execute(ctx context.Context, mode, configPath, manifestPath string, now time.Time) (commandReport, error) {
	report := commandReport{Mode: mode, GeneratedAt: now}
	if mode != modeDryRun && mode != modeApply {
		return report, fmt.Errorf("unsupported mode %q", mode)
	}
	m, err := loadManifest(manifestPath)
	if err != nil {
		return report, err
	}
	if mode == modeApply {
		if strings.TrimSpace(manifestPath) == "" {
			return report, errors.New("apply requires an explicitly reviewed migration manifest")
		}
		if strings.TrimSpace(m.EE.Assembly.EvidenceRef) == "" {
			return report, errors.New("apply requires ee.assembly.evidence_ref for both Community and EE installations")
		}
	}
	cfg, err := config.LoadWithOptions(config.LoadOptions{ConfigPath: configPath})
	if err != nil {
		return report, fmt.Errorf("load bkn-safe config: %w", err)
	}
	db, err := database.Open(cfg.DB)
	if err != nil {
		return report, err
	}
	if sqlDB, sqlErr := db.DB(); sqlErr == nil {
		defer sqlDB.Close()
	}

	// Both inventories must pass before ApplyCore can make the first write.
	report.Core, err = authzmigration.PlanCore(ctx, db, m.LifecycleEvidence)
	if err != nil {
		return report, fmt.Errorf("Core preflight: %w", err)
	}
	report.Enterprise, err = authzmigration.PlanEnterprise(ctx, db, m.eeOptions(now))
	if err != nil {
		return report, fmt.Errorf("Enterprise preflight: %w", err)
	}
	if mode == modeDryRun {
		return report, nil
	}
	if err := authzmigration.ValidateActiveGrantConfirmation(report.Enterprise, m.EE.Activation); err != nil {
		return report, fmt.Errorf("activation receipt preflight: %w", err)
	}

	if _, err := authzmigration.ApplyCore(ctx, db, m.LifecycleEvidence); err != nil {
		return report, err
	}
	if _, err := authzmigration.ApplyEnterprise(ctx, db, m.eeOptions(now)); err != nil {
		return report, err
	}
	report.Core, report.Enterprise, err = authzmigration.ReconciledReports(ctx, db, m.LifecycleEvidence, m.eeOptions(now))
	if err != nil {
		return report, err
	}
	marker, err := authzmigration.BuildMarker(report.Core, report.Enterprise, m.EE.Activation, now)
	if err != nil {
		return report, err
	}
	if err := authzmigration.PersistMarker(ctx, db, marker); err != nil {
		return report, err
	}
	if err := authzmigration.VerifyCurrentMarker(ctx, db); err != nil {
		return report, err
	}
	report.Marker = &marker
	return report, nil
}

func loadManifest(path string) (manifest, error) {
	var result manifest
	if strings.TrimSpace(path) == "" {
		return result, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return result, fmt.Errorf("open migration manifest: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, fmt.Errorf("decode migration manifest: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return result, errors.New("decode migration manifest: multiple JSON values")
		}
		return result, fmt.Errorf("decode migration manifest: %w", err)
	}
	return result, nil
}

func writeReport(w io.Writer, report commandReport) error {
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}
