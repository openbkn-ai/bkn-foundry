// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package lineage sets up the git lineage of a knowledge network and lets
// projects work on their own branches of it. main mirrors the live network and
// moves only through the drift commit and publishing; project branches take
// definition uploads, sync main into themselves and are published as new networks.
package lineage

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"path"
	"slices"
	"sort"
	"sync"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/bkn-specification/bkn"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/capability"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/drift"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/merge"
)

var (
	lsOnce sync.Once
	ls     interfaces.LineageService
)

type lineageService struct {
	gs  interfaces.GitStore
	bba interfaces.BknBackendAccess
	efa interfaces.ExecutionFactoryAccess
	ds  interfaces.DriftService
	ra  interfaces.ReleaseAccess
}

func NewLineageService() interfaces.LineageService {
	lsOnce.Do(func() {
		ls = &lineageService{gs: logics.GS, bba: logics.BBA, efa: logics.EFA, ds: drift.NewDriftService(), ra: logics.RA}
	})
	return ls
}

func (s *lineageService) CreateLineage(ctx context.Context, token string, knID string,
	author interfaces.GitAuthor) (*interfaces.CreateLineageResult, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "CreateLineage")
	defer span.End()

	if knID == "" {
		span.SetStatus(codes.Error, "Empty kn_id")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Lineage_InvalidKNID)
	}
	files, err := s.bba.ExportBKN(ctx, token, knID)
	if err != nil {
		span.SetStatus(codes.Error, "Export live definitions failed")
		return nil, toHTTPError(ctx, "Export live definitions failed", err)
	}
	head, err := s.gs.InitLineage(ctx, knID, files, fmt.Sprintf("start lineage from live network %s", knID), author)
	if err != nil {
		span.SetStatus(codes.Error, "Init lineage failed")
		return nil, toHTTPError(ctx, "Init lineage failed", err)
	}
	span.SetStatus(codes.Ok, "")
	return &interfaces.CreateLineageResult{LineageID: knID, Commit: head}, nil
}

func (s *lineageService) CreateBranch(ctx context.Context, lineageID string, name string,
	from string) (*interfaces.CreateBranchResult, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "CreateBranch")
	defer span.End()

	if !isProjectBranch(name) {
		span.SetStatus(codes.Error, "Invalid branch name")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Lineage_InvalidBranchName)
	}
	if from == "" {
		from = interfaces.GIT_MAIN_BRANCH
	}
	var head string
	err := s.gs.WithLineageLock(ctx, lineageID, func(gs interfaces.GitStore) error {
		if err := gs.CreateBranch(ctx, lineageID, name, from); err != nil {
			return err
		}
		var err error
		head, err = gs.ResolveRef(ctx, lineageID, branchRef(name))
		return err
	})
	if err != nil {
		span.SetStatus(codes.Error, "Create branch failed")
		return nil, toHTTPError(ctx, "Create branch failed", err)
	}
	span.SetStatus(codes.Ok, "")
	return &interfaces.CreateBranchResult{Name: name, Commit: head}, nil
}

func (s *lineageService) CommitDefinitions(ctx context.Context, lineageID string, branch string, files map[string][]byte,
	message string, author interfaces.GitAuthor) (*interfaces.CommitDefinitionsResult, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "CommitDefinitions")
	defer span.End()

	if err := checkProjectBranch(ctx, branch); err != nil {
		span.SetStatus(codes.Error, "Invalid branch")
		return nil, err
	}
	if len(files) == 0 {
		span.SetStatus(codes.Error, "Empty definitions")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Lineage_InvalidDefinitions)
	}
	if clash := fileAlsoDirectory(files); clash != "" {
		span.SetStatus(codes.Error, "Path is both a file and a directory")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Lineage_InvalidDefinitions).
			WithErrorDetails(fmt.Sprintf("path %q is both a file and a directory", clash))
	}
	// Refuse a set the BKN loader cannot read now; stored, it would only fail later in sync or
	// publish, far from the upload that caused it.
	fsys := bkn.NewMemoryFileSystem()
	fsys.AddFiles(files)
	if _, err := bkn.LoadNetworkWithFS(fsys, "."); err != nil {
		span.SetStatus(codes.Error, "Definitions do not load")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Lineage_InvalidDefinitions).
			WithErrorDetails(err.Error())
	}
	if message == "" {
		message = fmt.Sprintf("update definitions on %s", branch)
	}

	result := &interfaces.CommitDefinitionsResult{}
	// Under the lock, the diff covers exactly this commit and nothing a concurrent writer added.
	err := s.gs.WithLineageLock(ctx, lineageID, func(gs interfaces.GitStore) error {
		before, err := gs.ResolveRef(ctx, lineageID, branchRef(branch))
		if err != nil {
			return err
		}
		if result.Commit, err = gs.CommitFiles(ctx, lineageID, branch, files, message, author); err != nil {
			return err
		}
		diff, err := gs.Diff(ctx, lineageID, before, result.Commit)
		if err != nil {
			return err
		}
		result.Changed = *diff
		return nil
	})
	if err != nil {
		span.SetStatus(codes.Error, "Commit definitions failed")
		return nil, toHTTPError(ctx, "Commit definitions failed", err)
	}
	span.SetStatus(codes.Ok, "")
	return result, nil
}

// AddDefinitions reads the branch, writes files and commits under one hold of the lineage lock, so
// nothing a concurrent writer committed in between is lost. It replaces only what the same source
// wrote before (owned), removing what of it files no longer hold; any other path the branch holds
// is refused, listed. The branch has to be kept in BKN 3.0, the rules the files are written in; a
// branch started in 2.x moves to 3.0 when it syncs.
func (s *lineageService) AddDefinitions(ctx context.Context, lineageID string, branch string,
	files map[string][]byte, owned []string, message string,
	author interfaces.GitAuthor) (*interfaces.CommitDefinitionsResult, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "AddDefinitions")
	defer span.End()

	if err := checkProjectBranch(ctx, branch); err != nil {
		span.SetStatus(codes.Error, "Invalid branch")
		return nil, err
	}
	if len(files) == 0 {
		span.SetStatus(codes.Error, "Empty definitions")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Lineage_InvalidDefinitions)
	}
	if message == "" {
		message = fmt.Sprintf("add definitions on %s", branch)
	}

	result := &interfaces.CommitDefinitionsResult{}
	err := s.gs.WithLineageLock(ctx, lineageID, func(gs interfaces.GitStore) error {
		before, err := gs.ResolveRef(ctx, lineageID, branchRef(branch))
		if err != nil {
			return err
		}
		current, err := gs.ReadTree(ctx, lineageID, branchRef(branch))
		if err != nil {
			return err
		}
		if !keptInV3(current) {
			// Syncing moves the branch to 3.0 only once main is; until then main's drift has to
			// be committed first, which writes main in 3.0.
			code := perrors.BknProject_Lineage_BranchNotV3
			if mainFiles, err := gs.ReadTree(ctx, lineageID, branchRef(interfaces.GIT_MAIN_BRANCH)); err != nil {
				return err
			} else if !keptInV3(mainFiles) {
				code = perrors.BknProject_Lineage_LineageNotV3
			}
			return httperrors.NewHTTPError(ctx, http.StatusConflict, code)
		}
		mine := make(map[string]bool, len(owned))
		for _, p := range owned {
			mine[p] = true
		}
		var existing []string
		merged := make(map[string][]byte, len(current)+len(files))
		for p, data := range current {
			if !mine[p] {
				merged[p] = data
			}
		}
		for p, data := range files {
			if _, ok := current[p]; ok && !mine[p] {
				existing = append(existing, p)
			}
			merged[p] = data
		}
		if len(existing) > 0 {
			sort.Strings(existing)
			return httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Lineage_DefinitionsExist).
				WithErrorDetails(existing)
		}
		if clash := fileAlsoDirectory(merged); clash != "" {
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Lineage_InvalidDefinitions).
				WithErrorDetails(fmt.Sprintf("path %q is both a file and a directory", clash))
		}
		// Refuse what the BKN loader cannot read now, such as a value type named as one the
		// branch has; stored, it would only fail later in sync or publish.
		fsys := bkn.NewMemoryFileSystem()
		fsys.AddFiles(merged)
		if _, err := bkn.LoadNetworkWithFS(fsys, "."); err != nil {
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Lineage_InvalidDefinitions).
				WithErrorDetails(err.Error())
		}
		// The loader lets through what the specification's checks refuse, such as an object type
		// named as one the branch holds; only what these files bring in is refused.
		introduced, err := merge.IntroducedErrors(current, merged)
		if err != nil {
			return err
		}
		if len(introduced) > 0 {
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Lineage_InvalidDefinitions).
				WithErrorDetails(introduced)
		}
		if result.Commit, err = gs.CommitFiles(ctx, lineageID, branch, merged, message, author); err != nil {
			return err
		}
		diff, err := gs.Diff(ctx, lineageID, before, result.Commit)
		if err != nil {
			return err
		}
		result.Changed = *diff
		return nil
	})
	if err != nil {
		span.SetStatus(codes.Error, "Add definitions failed")
		return nil, toHTTPError(ctx, "Add definitions failed", err)
	}
	span.SetStatus(codes.Ok, "")
	return result, nil
}

// keptInV3 reports whether a set of definitions declares BKN 3.0 in its network.bkn.
func keptInV3(files map[string][]byte) bool {
	network, err := bkn.ParseNetworkFile(string(files[bkn.RootFileName]), bkn.RootFileName)
	return err == nil && network.BknSpec == bkn.BknSpecV3
}

// SyncBranch reads both heads, decides and writes under one hold of the lineage
// lock, so main cannot move between the merge and its commit.
func (s *lineageService) SyncBranch(ctx context.Context, lineageID string, branch string,
	author interfaces.GitAuthor) (*interfaces.SyncBranchResult, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "SyncBranch")
	defer span.End()

	if err := checkProjectBranch(ctx, branch); err != nil {
		span.SetStatus(codes.Error, "Invalid branch")
		return nil, err
	}

	var result *interfaces.SyncBranchResult
	err := s.gs.WithLineageLock(ctx, lineageID, func(gs interfaces.GitStore) error {
		var err error
		result, err = syncBranch(ctx, gs, lineageID, branch, author)
		return err
	})
	if err != nil {
		span.SetStatus(codes.Error, "Sync branch failed")
		return nil, toHTTPError(ctx, "Sync branch failed", err)
	}
	span.SetStatus(codes.Ok, "")
	return result, nil
}

func syncBranch(ctx context.Context, gs interfaces.GitStore, lineageID string, branch string,
	author interfaces.GitAuthor) (*interfaces.SyncBranchResult, error) {
	branchHead, err := gs.ResolveRef(ctx, lineageID, branchRef(branch))
	if err != nil {
		return nil, err
	}
	mainHead, err := gs.ResolveRef(ctx, lineageID, branchRef(interfaces.GIT_MAIN_BRANCH))
	if err != nil {
		return nil, err
	}
	base, err := gs.MergeBase(ctx, lineageID, mainHead, branchHead)
	if err != nil {
		return nil, err
	}

	switch base {
	case mainHead:
		return &interfaces.SyncBranchResult{Result: interfaces.SYNC_RESULT_UP_TO_DATE, Commit: branchHead}, nil
	case branchHead:
		head, err := gs.FastForward(ctx, lineageID, branch, mainHead)
		if err != nil {
			return nil, err
		}
		return &interfaces.SyncBranchResult{Result: interfaces.SYNC_RESULT_FAST_FORWARD, Commit: head}, nil
	}

	baseFiles, err := gs.ReadTree(ctx, lineageID, base)
	if err != nil {
		return nil, err
	}
	mainFiles, err := gs.ReadTree(ctx, lineageID, mainHead)
	if err != nil {
		return nil, err
	}
	branchFiles, err := gs.ReadTree(ctx, lineageID, branchHead)
	if err != nil {
		return nil, err
	}
	// Ours is main and theirs is the branch, as conflicts report them.
	merged, err := merge.Merge(baseFiles, mainFiles, branchFiles)
	if err != nil {
		return nil, fmt.Errorf("merge main into %s: %w", branch, err)
	}
	if merged.HasConflicts() {
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Lineage_MergeConflict).
			WithErrorDetails(map[string]any{"conflicts": merged.Conflicts, "summary": merged.Summary})
	}
	head, err := gs.CommitMerge(ctx, lineageID, branch, mainHead, merged.Files,
		fmt.Sprintf("merge %s into %s", interfaces.GIT_MAIN_BRANCH, branch), author)
	if err != nil {
		return nil, err
	}
	return &interfaces.SyncBranchResult{Result: interfaces.SYNC_RESULT_MERGED, Commit: head, Summary: &merged.Summary}, nil
}

func (s *lineageService) CheckDrift(ctx context.Context, token string, lineageID string, knID string, commit bool,
	author interfaces.GitAuthor) (*interfaces.DriftResult, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "CheckDrift")
	defer span.End()

	if knID == "" {
		span.SetStatus(codes.Error, "Empty kn_id")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Lineage_InvalidKNID)
	}
	// Committing another network's definitions would replace main with them, so only the
	// lineage's live network is checked; an older release of the lineage is not live either.
	live, err := s.liveNetwork(ctx, lineageID)
	if err != nil {
		span.SetStatus(codes.Error, "Find live network failed")
		return nil, toHTTPError(ctx, "Find live network failed", err)
	}
	if knID != live {
		span.SetStatus(codes.Error, "Not the live network")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Lineage_NotLiveNetwork).
			WithErrorDetails(map[string]any{"kn_id": knID, "live_network_id": live})
	}
	report, err := s.ds.DriftCheck(ctx, token, lineageID, knID)
	if err != nil {
		span.SetStatus(codes.Error, "Drift check failed")
		return nil, toHTTPError(ctx, "Drift check failed", err)
	}
	result := &interfaces.DriftResult{DriftReport: report}
	if commit && !report.Empty() {
		if result.Commit, err = s.ds.CommitLiveToMain(ctx, report, author); err != nil {
			span.SetStatus(codes.Error, "Commit live definitions failed")
			return nil, toHTTPError(ctx, "Commit live definitions failed", err)
		}
	}
	span.SetStatus(codes.Ok, "")
	return result, nil
}

// fileAlsoDirectory returns the first path, in sorted order, that is a file and
// also a parent directory of another path. Git cannot store such a set, and the
// loader reads it anyway, so it is checked here with the other upload rules.
func fileAlsoDirectory(files map[string][]byte) string {
	for _, p := range slices.Sorted(maps.Keys(files)) {
		for dir := path.Dir(p); dir != "." && dir != "/"; dir = path.Dir(dir) {
			if _, ok := files[dir]; ok {
				return dir
			}
		}
	}
	return ""
}

// isProjectBranch accepts the names git accepts for a branch, except main.
func isProjectBranch(name string) bool {
	return name != "" && name != interfaces.GIT_MAIN_BRANCH && plumbing.NewBranchReferenceName(name).Validate() == nil
}

// checkProjectBranch guards writes to an existing branch: main is read-only here.
func checkProjectBranch(ctx context.Context, name string) error {
	if name == interfaces.GIT_MAIN_BRANCH {
		return httperrors.NewHTTPError(ctx, http.StatusForbidden, perrors.BknProject_Lineage_MainBranchReadOnly)
	}
	if !isProjectBranch(name) {
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Lineage_InvalidBranchName)
	}
	return nil
}

// branchRef names a branch in full, so a tag of the same name never shadows it.
func branchRef(name string) string {
	return plumbing.NewBranchReferenceName(name).String()
}

// toHTTPError maps an error of the git store or bkn-backend to the reply.
// v3ExportRefusals maps bkn-backend's refusals to export a network as BKN 3.0 to bkn-project's
// codes: some action parameters are misplaced or invalid, or a definition does not read back
// under the 3.0 rules.
var v3ExportRefusals = map[string]string{
	"BknBackend.KnowledgeNetwork.ExportNeedsNetworkCheck": perrors.BknProject_Lineage_NetworkNeedsCheck,
	"BknBackend.KnowledgeNetwork.ExportNotReadableAsV3":   perrors.BknProject_Lineage_NetworkNotReadableAsV3,
}

func toHTTPError(ctx context.Context, operation string, err error) error {
	var httpErr *httperrors.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr
	}
	var itemErr *capability.ItemError
	if errors.As(err, &itemErr) {
		// A 4xx from execution-factory is the caller's to fix and keeps its status; anything else is a bad gateway.
		status := http.StatusBadGateway
		details := map[string]any{"kind": itemErr.Kind, "id": itemErr.ID, "name": itemErr.Name,
			"operation": itemErr.Operation, "error": itemErr.Err.Error()}
		var efaErr *interfaces.ExecutionFactoryError
		if errors.As(err, &efaErr) {
			if efaErr.HTTPCode >= http.StatusBadRequest && efaErr.HTTPCode < http.StatusInternalServerError {
				status = efaErr.HTTPCode
			}
			details["error_code"], details["description"] = efaErr.ErrorCode, efaErr.Description
		}
		otellog.LogWarn(ctx, fmt.Sprintf("%s: %v", operation, err))
		return httperrors.NewHTTPError(ctx, status, perrors.BknProject_Lineage_CapabilityRequestFailed).
			WithErrorDetails(details)
	}
	var backendErr *interfaces.BknBackendError
	if errors.As(err, &backendErr) {
		// The network's own definitions need fixing before a lineage can hold it as BKN 3.0;
		// bkn-backend names what to fix, which goes on as it is. Its own advice to export as
		// 2.x is not one a lineage can take.
		if code, ok := v3ExportRefusals[backendErr.ErrorCode]; ok {
			otellog.LogWarn(ctx, fmt.Sprintf("%s: %v", operation, err))
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest, code).WithErrorDetails(backendErr.ErrorDetails)
		}
	}
	if errors.As(err, &backendErr) {
		// A 4xx is the caller's to fix and keeps its status; anything else is a bad gateway.
		status := backendErr.HTTPCode
		if status < http.StatusBadRequest || status >= http.StatusInternalServerError {
			status = http.StatusBadGateway
		}
		otellog.LogWarn(ctx, fmt.Sprintf("%s: %v", operation, err))
		return httperrors.NewHTTPError(ctx, status, perrors.BknProject_Lineage_BknBackendRequestFailed).
			WithErrorDetails(map[string]any{"error_code": backendErr.ErrorCode, "description": backendErr.Description})
	}
	switch {
	case errors.Is(err, git.ErrRepositoryNotExists):
		return httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Lineage_NotFound)
	case errors.Is(err, plumbing.ErrReferenceNotFound):
		return httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Lineage_BranchNotFound)
	case errors.Is(err, git.ErrRepositoryAlreadyExists):
		return httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Lineage_AlreadyExists)
	case errors.Is(err, git.ErrBranchExists):
		return httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Lineage_BranchAlreadyExists)
	}
	otellog.LogError(ctx, operation, err)
	return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, perrors.BknProject_Lineage_InternalError).
		WithErrorDetails(err.Error())
}

// liveNetwork returns the network of the lineage's latest release, or the lineage id, which is
// the network the lineage was started from, before the first release.
func (s *lineageService) liveNetwork(ctx context.Context, lineageID string) (string, error) {
	latest, err := s.ra.ListReleases(ctx, interfaces.ReleasesQuery{LineageID: lineageID, Limit: 1})
	if err != nil {
		return "", err
	}
	if len(latest) == 0 {
		return lineageID, nil
	}
	return latest[0].NetworkID, nil
}
