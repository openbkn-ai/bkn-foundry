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
)

var (
	dsOnce sync.Once
	ds     interfaces.DriftService
)

type driftService struct {
	gs  interfaces.GitStore
	bba interfaces.BknBackendAccess
}

func NewDriftService() interfaces.DriftService {
	dsOnce.Do(func() {
		ds = &driftService{gs: logics.GS, bba: logics.BBA}
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

	report := &interfaces.DriftReport{
		LineageID: lineageID,
		KNID:      knID,
		PathDiff:  diffFiles(stored, live),
		Live:      live,
	}
	span.SetStatus(codes.Ok, "")
	return report, nil
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
	span.SetStatus(codes.Ok, "")
	return head, nil
}

// diffFiles compares the bytes of each path: what was added, changed or removed going from base to next.
func diffFiles(base map[string][]byte, next map[string][]byte) interfaces.PathDiff {
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
