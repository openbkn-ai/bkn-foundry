// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbrefresh

import (
	"context"
	"errors"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbdataset"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbpublication"
)

var ErrNoPublishedTarget = errors.New("the network in use has no acknowledged document publication")

// Resolve follows D9's explicit consumer switch, including rollback. The newest
// release's display state is not an authority to replace the network in use.
// It intentionally never reads mutable project status, owners or draft state.
func Resolve(ctx context.Context, lineage string, releases interfaces.ReleaseAccess, publications interfaces.KBPublicationAccess) (*interfaces.KBPublication, error) {
	if lineage == "" || releases == nil || publications == nil {
		return nil, ErrNoPublishedTarget
	}
	network := lineage
	sw, found, err := releases.GetLatestSwitch(ctx, lineage)
	if err != nil {
		return nil, err
	}
	if found {
		if sw == nil || sw.LineageID != lineage || sw.ToNetworkID == "" {
			return nil, ErrNoPublishedTarget
		}
		network = sw.ToNetworkID
	}
	release, found, err := releases.GetReleaseByNetworkID(ctx, network)
	if err != nil {
		return nil, err
	}
	if !found || release == nil || release.NetworkID != network || release.LineageID != lineage {
		return nil, ErrNoPublishedTarget
	}
	record, err := publications.Get(ctx, network)
	if err != nil {
		return nil, err
	}
	if record == nil || record.State != "active" || record.NetworkID != network || record.LineageID != lineage || record.ProjectID != release.ProjectID || record.Publisher != release.Publisher || record.GenerationID == "" || record.FullBuildID == "" || record.Revision < 1 {
		return nil, ErrNoPublishedTarget
	}
	return record, nil
}

// Preparation is request-local, private execution input. Only its manifest and
// cursors belong in process storage; the fixed model belongs in private Vega.
type Preparation struct {
	Publication *interfaces.KBPublication
	Model       *interfaces.Draft
	Source      kb.PublishedSource
	Plan        *ManifestPlan
	Cursors     map[string]string
	Guard       func(context.Context) error
}
type Service struct {
	Git             interfaces.GitStore
	ExpectedNetwork string
	Enabled         func() bool
	Network         kb.PublishedModelCheck
	Limits          *Limits
	Journal         interfaces.KBRefreshAccess
	Tasks           kb.RefreshTasks
	Vega            interfaces.VegaAccess
	guard           func(context.Context) error
	Releases        interfaces.ReleaseAccess
	Publications    interfaces.KBPublicationAccess
	Builds          interfaces.KBBuildAccess
	Storage         kb.PublicationDatasets
	Sources         kb.RefreshSources
}

// Prepare uses the last committed manifest when provided; the initial manifest
// is the publication's acknowledged full build. It performs no inference or
// Dataset mutation. A caller must journal its intent before executing this plan.
func (s *Service) Prepare(ctx context.Context, lineage, id string, previous *interfaces.KBBuild) (*Preparation, error) {
	record, err := Resolve(ctx, lineage, s.Releases, s.Publications)
	if err != nil {
		return nil, err
	}
	if s.Sources == nil || s.Builds == nil {
		return nil, kb.ErrUnavailable
	}
	expected := *record
	guard := func(ctx context.Context) error {
		if s.Enabled != nil && !s.Enabled() {
			return kb.ErrUnavailable
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if s.guard != nil {
			if err := s.guard(ctx); err != nil {
				return err
			}
		}
		current, err := Resolve(ctx, lineage, s.Releases, s.Publications)
		if err != nil {
			return err
		}
		if (s.ExpectedNetwork != "" && current.NetworkID != s.ExpectedNetwork) || current.NetworkID != expected.NetworkID || current.Revision != expected.Revision || current.FullBuildID != expected.FullBuildID || current.GenerationID != expected.GenerationID || current.ModelFingerprint != expected.ModelFingerprint || current.RuntimeDigest != expected.RuntimeDigest || current.Publisher != expected.Publisher {
			return ErrNoPublishedTarget
		}
		return nil
	}
	source, err := s.Sources.OpenPublished(ctx, record, guard)
	if err != nil {
		return nil, err
	}
	if err := guard(ctx); err != nil {
		return nil, err
	}
	model, err := kbpublication.LoadPublishedModel(ctx, s.Storage, record)
	if err != nil {
		return nil, err
	}
	fingerprint, err := kbdataset.ModelFingerprint(model)
	if err != nil || fingerprint != record.ModelFingerprint {
		return nil, ErrInvalidManifest
	}
	// A previous refresh manifest is a data baseline, never authority to choose
	// another publication's extraction configuration.
	stored, err := s.Builds.Get(ctx, record.ProjectID, record.FullBuildID)
	if err != nil {
		return nil, err
	}
	if stored == nil || stored.Build == nil || stored.Build.ID != record.FullBuildID {
		return nil, ErrInvalidManifest
	}
	original := stored.Build
	valid := func(build *interfaces.KBBuild) bool {
		return build.ProjectID == record.ProjectID && build.NetworkID == record.LineageID && build.GenerationID == record.GenerationID && build.ModelFingerprint == record.ModelFingerprint && build.AgentVersion > 0 && build.PromptVersion != nil && build.ModelID != ""
	}
	if !valid(original) {
		return nil, ErrInvalidManifest
	}
	if previous == nil {
		previous = original
	}
	if !valid(previous) {
		return nil, ErrInvalidManifest
	}

	snapshot, err := Scan(ctx, source, record.NetworkID, record.Ranges, guard)
	if err != nil {
		return nil, err
	}
	plan, err := Plan(id, previous, snapshot.Files)
	if err != nil {
		return nil, err
	}
	// The physical proxy target changes by release; Dataset identities retain
	// the lineage namespace used by the acknowledged original build.
	// Existing file receipts remain actual; only future changed files use the
	// current publication's acknowledged extraction defaults.
	plan.Build.AgentVersion, plan.Build.ModelID, plan.Build.Model = original.AgentVersion, original.ModelID, original.Model
	prompt := *original.PromptVersion
	plan.Build.PromptVersion = &prompt
	plan.Build.NetworkID = record.LineageID
	plan.Build.Owner = record.Publisher
	plan.Build.DefinitionCommit = record.DefinitionCommit
	if err := guard(ctx); err != nil {
		return nil, err
	}
	return &Preparation{Publication: record, Model: model, Source: source, Plan: plan, Cursors: snapshot.Cursors, Guard: guard}, nil
}
