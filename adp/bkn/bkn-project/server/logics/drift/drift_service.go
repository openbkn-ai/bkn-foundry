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
	kva interfaces.KNVersionAccess
	efa interfaces.ExecutionFactoryAccess
}

func NewDriftService() interfaces.DriftService {
	dsOnce.Do(func() {
		ds = &driftService{gs: logics.GS, kva: logics.KVA, efa: logics.EFA}
	})
	return ds
}

func (s *driftService) DriftCheck(ctx context.Context, token string, lineageID string, knID string) (*interfaces.DriftReport, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DriftCheck")
	defer span.End()

	live, err := s.kva.ExportVersion(ctx, token, knID, interfaces.KN_VERSION_DRAFT)
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

// networkFile is the network's own definition file; its front matter names the version it was
// exported from (branch: draft, v1, v2 ...), which says nothing about its content.
const networkFile = "network.bkn"

// sameFile tells whether two copies of a file at path hold the same definitions.
func sameFile(path string, a []byte, b []byte) bool {
	if path != networkFile {
		return bytes.Equal(a, b)
	}
	return bytes.Equal(withoutBranch(a), withoutBranch(b))
}

// withoutBranch drops the branch line of a definition file's front matter.
func withoutBranch(data []byte) []byte {
	lines := bytes.SplitAfter(data, []byte("\n"))
	if len(lines) == 0 || !bytes.Equal(bytes.TrimSpace(lines[0]), []byte("---")) {
		return data
	}
	out := make([]byte, 0, len(data))
	out = append(out, lines[0]...)
	inFront := true
	for _, line := range lines[1:] {
		if inFront && bytes.Equal(bytes.TrimSpace(line), []byte("---")) {
			inFront = false
		}
		if inFront && bytes.HasPrefix(line, []byte("branch:")) {
			continue
		}
		out = append(out, line...)
	}
	return out
}

// DiffFiles compares the bytes of each path, the network file without the version it names: what was
// added, changed or removed going from base to next.
func DiffFiles(base map[string][]byte, next map[string][]byte) interfaces.PathDiff {
	diff := interfaces.PathDiff{Added: []string{}, Changed: []string{}, Removed: []string{}}
	for p, data := range next {
		old, ok := base[p]
		switch {
		case !ok:
			diff.Added = append(diff.Added, p)
		case !sameFile(p, old, data):
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
