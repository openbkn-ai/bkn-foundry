// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package drift catches edits made to a live network outside bkn-project, for
// example in Studio, before a release builds on main in git.
package drift

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/capability"
)

var (
	dsOnce sync.Once
	ds     interfaces.DriftService
)

type driftService struct {
	gs  interfaces.GitStore
	bba interfaces.BknBackendAccess
	efa interfaces.ExecutionFactoryAccess
}

func NewDriftService() interfaces.DriftService {
	dsOnce.Do(func() {
		ds = &driftService{gs: logics.GS, bba: logics.BBA, efa: logics.EFA}
	})
	return ds
}

func (s *driftService) DriftCheck(ctx context.Context, token string, lineageID string, knID string) (*interfaces.DriftReport, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DriftCheck")
	defer span.End()

	live, err := s.bba.ExportBKN(ctx, token, knID)
	if err != nil {
		otellog.LogError(ctx, "Export live definitions failed", err)
		span.SetStatus(codes.Error, "Export live definitions failed")
		return nil, err
	}
	stored, err := s.gs.ReadTree(ctx, lineageID, interfaces.GIT_MAIN_BRANCH)
	if err != nil {
		otellog.LogError(ctx, "Read main failed", err)
		span.SetStatus(codes.Error, "Read main failed")
		return nil, fmt.Errorf("read main of lineage %s: %w", lineageID, err)
	}

	manifest, liveCapabilities, capabilityDiff, err := CheckCapabilities(ctx, s.gs, s.efa, token, lineageID, interfaces.GIT_MAIN_BRANCH)
	if err != nil {
		otellog.LogError(ctx, "Check functions and Skills failed", err)
		span.SetStatus(codes.Error, "Check functions and Skills failed")
		return nil, err
	}
	liveFiles := capability.Files(liveCapabilities)
	// Committing the drift must drop what no longer exists from release.yaml too,
	// or every later check reports it again.
	if kept := manifest.Keep(liveCapabilities); len(kept.Functions)+len(kept.Skills) < len(manifest.Functions)+len(manifest.Skills) {
		data, err := kept.Marshal()
		if err != nil {
			span.SetStatus(codes.Error, "Write release.yaml failed")
			return nil, err
		}
		liveFiles[interfaces.GIT_MANIFEST_FILE] = data
	}

	report := &interfaces.DriftReport{
		LineageID:        lineageID,
		KNID:             knID,
		PathDiff:         DiffFiles(stored, live),
		Capabilities:     capabilityDiff,
		Live:             live,
		LiveCapabilities: liveFiles,
	}
	span.SetStatus(codes.Ok, "")
	return report, nil
}

// CheckCapabilities reads release.yaml at ref, exports the live state of the
// toolboxes and Skills it lists and compares their files with those at ref.
func CheckCapabilities(ctx context.Context, gs interfaces.GitStore, efa interfaces.ExecutionFactoryAccess,
	token string, lineageID string, ref string) (*capability.Manifest, []*capability.Item, interfaces.PathDiff, error) {
	stored, err := gs.ReadCapabilities(ctx, lineageID, ref)
	if err != nil {
		return nil, nil, interfaces.PathDiff{}, fmt.Errorf("read functions and Skills of lineage %s: %w", lineageID, err)
	}
	manifest, err := capability.ParseManifest(stored[interfaces.GIT_MANIFEST_FILE])
	if err != nil {
		return nil, nil, interfaces.PathDiff{}, err
	}
	live, err := capability.ExportManifest(ctx, efa, token, manifest)
	if err != nil {
		return nil, nil, interfaces.PathDiff{}, err
	}
	return manifest, live, DiffFiles(capability.StoredFiles(stored), capability.Files(live)), nil
}

func (s *driftService) CommitLiveToMain(ctx context.Context, report *interfaces.DriftReport, author interfaces.GitAuthor) (string, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "CommitLiveToMain")
	defer span.End()

	message := fmt.Sprintf("sync live definitions from %s", report.KNID)
	head, err := s.gs.CommitFiles(ctx, report.LineageID, interfaces.GIT_MAIN_BRANCH, report.Live, message, author)
	if err != nil {
		otellog.LogError(ctx, "Commit live definitions failed", err)
		span.SetStatus(codes.Error, "Commit live definitions failed")
		return "", fmt.Errorf("commit live definitions of %s to lineage %s: %w", report.KNID, report.LineageID, err)
	}
	if !report.Capabilities.Empty() {
		message = fmt.Sprintf("sync live functions and Skills of %s", report.KNID)
		head, err = s.gs.CommitCapabilities(ctx, report.LineageID, interfaces.GIT_MAIN_BRANCH, report.LiveCapabilities, message, author)
		if err != nil {
			otellog.LogError(ctx, "Commit live functions and Skills failed", err)
			span.SetStatus(codes.Error, "Commit live functions and Skills failed")
			return "", fmt.Errorf("commit live functions and Skills to lineage %s: %w", report.LineageID, err)
		}
	}
	span.SetStatus(codes.Ok, "")
	return head, nil
}

// DiffFiles compares the bytes of each path: what was added, changed or removed going from base to next.
func DiffFiles(base map[string][]byte, next map[string][]byte) interfaces.PathDiff {
	diff := interfaces.PathDiff{Added: []string{}, Changed: []string{}, Removed: []string{}}
	for p, data := range next {
		old, ok := base[p]
		switch {
		case !ok:
			diff.Added = append(diff.Added, p)
		case !bytes.Equal(old, data):
			diff.Changed = append(diff.Changed, p)
		}
	}
	for p := range base {
		if _, ok := next[p]; !ok {
			diff.Removed = append(diff.Removed, p)
		}
	}
	sort.Strings(diff.Added)
	sort.Strings(diff.Changed)
	sort.Strings(diff.Removed)
	return diff
}
