// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/bkn-specification/bkn"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/draft"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/lineage"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/project"
)

// Directories of the definitions a review names.
const (
	objectTypesDir   = "object_types/"
	relationTypesDir = "relation_types/"
)

var (
	prsOnce sync.Once
	prs     interfaces.ProjectReleaseService
)

type projectReleaseService struct {
	ps  interfaces.ProjectService
	drs interfaces.DraftService
	ls  interfaces.LineageService
	da  interfaces.DraftAccess
	ca  interfaces.CheckAccess
	gs  interfaces.GitStore
}

func NewProjectReleaseService(appSetting *common.AppSetting) interfaces.ProjectReleaseService {
	prsOnce.Do(func() {
		prs = &projectReleaseService{
			ps:  project.NewProjectService(),
			drs: draft.NewDraftService(appSetting),
			ls:  lineage.NewLineageService(),
			da:  logics.DA,
			ca:  logics.CA,
			gs:  logics.GS,
		}
	})
	return prs
}

// ReviewRelease reads, as the caller, what publishing the project's branch would bring. The checks
// the release itself makes come from its preview; the project's own come from its draft, the
// check of its confirmation and the readiness of its indexes.
func (s *projectReleaseService) ReviewRelease(ctx context.Context, token string, projectID string, version string,
	caller interfaces.AccountInfo) (*interfaces.ReleaseReview, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ReviewRelease")
	defer span.End()

	entry, err := s.ps.GetProject(ctx, token, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get project failed")
		return nil, err
	}
	preview, err := s.ls.PreviewRelease(ctx, token, entry.KNID, entry.Branch, version)
	if err != nil {
		span.SetStatus(codes.Error, "Preview release failed")
		return nil, err
	}
	draft, hasDraft, err := s.da.GetDraft(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get draft failed")
		return nil, internalError(ctx, "Get draft failed", err)
	}
	check, hasCheck, err := s.ca.GetCheck(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get check failed")
		return nil, internalError(ctx, "Get check failed", err)
	}
	branchFiles, err := s.gs.ReadTree(ctx, entry.KNID, preview.BranchCommit)
	if err != nil {
		span.SetStatus(codes.Error, "Read branch failed")
		return nil, internalError(ctx, "Read branch failed", err)
	}
	mainFiles, err := s.gs.ReadTree(ctx, entry.KNID, preview.MainCommit)
	if err != nil {
		span.SetStatus(codes.Error, "Read main failed")
		return nil, internalError(ctx, "Read main failed", err)
	}

	// Only the confirmation of the draft as it now is counts, and only the check of it.
	var confirmation *interfaces.DraftConfirmation
	outdated := false
	if hasDraft && draft.Items.Confirmation != nil && draft.Items.Confirmation.Commit != "" {
		confirmation = draft.Items.Confirmation
		outdated = confirmation.UpdateTime != draft.UpdateTime
	}
	if !hasCheck || confirmation == nil || check.Commit != confirmation.Commit {
		check = nil
	}
	var index *interfaces.ProjectIndex
	var indexErr error
	if confirmation != nil {
		index, indexErr = s.drs.GetIndex(ctx, token, projectID, caller)
	}

	review := &interfaces.ReleaseReview{
		ProjectID: projectID,
		Version:   version,
		Refusals:  []interfaces.ReleaseRefusal{},
		Accepts:   []string{},
		Changes:   releaseChanges(preview.Changes, branchFiles, mainFiles),
		Release: interfaces.ReleaseTarget{
			Tag: preview.Tag, NetworkID: preview.NetworkID, NetworkName: preview.NetworkName,
			LiveNetworkID: preview.LiveNetworkID, BaseVersion: preview.BaseVersion,
			MainCommit: preview.MainCommit, BranchCommit: preview.BranchCommit,
		},
		Capabilities: preview.Capabilities,
	}
	var drift, behind *interfaces.ReleaseRefusal
	for i, r := range preview.Refusals {
		switch r.Code {
		case perrors.BknProject_Release_Drift:
			drift = &preview.Refusals[i]
		case perrors.BknProject_Release_BranchBehind:
			behind = &preview.Refusals[i]
		default:
			review.Refusals = append(review.Refusals, r)
		}
	}
	review.Checks = []interfaces.ReleaseCheck{
		confirmedCheck(confirmation, outdated),
		mappedCheck(confirmation, branchFiles),
		namesCheck(preview.Changes.Added, branchFiles),
		driftCheck(drift),
		checkCheck(confirmation, check),
		indexCheck(confirmation, index, indexErr),
		baselineCheck(behind, preview),
	}
	review.Risks = releaseRisks(review.Changes, preview.Changes, check, branchFiles, preview.Capabilities)

	for _, c := range review.Checks {
		switch {
		case c.Passed:
		case c.Accept == "":
			review.Blocked = true
		default:
			review.Accepts = append(review.Accepts, c.Accept)
		}
	}
	review.Blocked = review.Blocked || len(review.Refusals) > 0
	sort.Strings(review.Accepts)

	updateTime := int64(0)
	if hasDraft {
		updateTime = draft.UpdateTime
	}
	review.Signature = signature(preview.BranchCommit, preview.MainCommit, updateTime)
	span.SetStatus(codes.Ok, "")
	return review, nil
}

// signature stands for the branch, main and draft a review was made from.
func signature(branchCommit string, mainCommit string, draftUpdateTime int64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\n%s\n%d", branchCommit, mainCommit, draftUpdateTime)))
	return hex.EncodeToString(sum[:])
}

func passed(code string) interfaces.ReleaseCheck {
	return interfaces.ReleaseCheck{Code: code, Passed: true}
}

func failed(code string, reason string, accept string, details any) interfaces.ReleaseCheck {
	return interfaces.ReleaseCheck{Code: code, Reason: reason, Accept: accept, Details: details}
}

func confirmedCheck(confirmation *interfaces.DraftConfirmation, outdated bool) interfaces.ReleaseCheck {
	switch {
	case confirmation == nil:
		return failed(interfaces.RELEASE_CHECK_CONFIRMED, interfaces.RELEASE_REASON_NOT_CONFIRMED, "", nil)
	case outdated:
		return failed(interfaces.RELEASE_CHECK_CONFIRMED, interfaces.RELEASE_REASON_CONFIRMATION_OUTDATED, "", nil)
	}
	return passed(interfaces.RELEASE_CHECK_CONFIRMED)
}

// mappedCheck reads, on the branch, each object type the confirmation wrote: it must be bound to a
// table, identified by keys, and map every property to a field.
func mappedCheck(confirmation *interfaces.DraftConfirmation, files map[string][]byte) interfaces.ReleaseCheck {
	if confirmation == nil {
		return failed(interfaces.RELEASE_CHECK_MAPPED, interfaces.RELEASE_REASON_NOT_CONFIRMED, "", nil)
	}
	missing := []interfaces.ReleaseItem{}
	for _, p := range confirmation.Files {
		if !strings.HasPrefix(p, objectTypesDir) {
			continue
		}
		ot, err := bkn.ParseObjectTypeFile(string(files[p]), p)
		if err == nil && mapped(ot) {
			continue
		}
		missing = append(missing, itemAt(p, files))
	}
	if len(missing) > 0 {
		return failed(interfaces.RELEASE_CHECK_MAPPED, interfaces.RELEASE_REASON_MAPPING_MISSING, "", missing)
	}
	return passed(interfaces.RELEASE_CHECK_MAPPED)
}

func mapped(ot *bkn.BknObjectType) bool {
	if ot.DataSource == nil || ot.DataSource.ID == "" || len(ot.PrimaryKeys) == 0 {
		return false
	}
	for _, p := range ot.DataProperties {
		if p.MappedField == "" {
			return false
		}
	}
	return true
}

// namesCheck compares the names of the object types and relation types the release adds with
// those of the others on the branch. The same name blocks; a similar one can be accepted.
func namesCheck(added []string, files map[string][]byte) interfaces.ReleaseCheck {
	addedItems := []interfaces.ReleaseItem{}
	for _, p := range added {
		if kindOf(p) != "" {
			addedItems = append(addedItems, itemAt(p, files))
		}
	}
	all := []interfaces.ReleaseItem{}
	for p := range files {
		if kindOf(p) != "" {
			all = append(all, itemAt(p, files))
		}
	}
	same, similar := compareNames(addedItems, all)
	details := map[string][]interfaces.SimilarNames{"same": same, "similar": similar}
	switch {
	case len(same) > 0:
		return failed(interfaces.RELEASE_CHECK_NAMES, interfaces.RELEASE_REASON_DUPLICATE_NAMES, "", details)
	case len(similar) > 0:
		return failed(interfaces.RELEASE_CHECK_NAMES, interfaces.RELEASE_REASON_SIMILAR_NAMES,
			interfaces.RELEASE_ACCEPT_SIMILAR_NAMES, details)
	}
	return passed(interfaces.RELEASE_CHECK_NAMES)
}

func driftCheck(drift *interfaces.ReleaseRefusal) interfaces.ReleaseCheck {
	if drift != nil {
		return failed(interfaces.RELEASE_CHECK_DRIFT, interfaces.RELEASE_REASON_DRIFT, "", drift.Details)
	}
	return passed(interfaces.RELEASE_CHECK_DRIFT)
}

// checkCheck reads the check of the confirmation: a partial one, which could not count every item,
// can be accepted; issues found, a failure or a check still running block.
func checkCheck(confirmation *interfaces.DraftConfirmation, check *interfaces.Check) interfaces.ReleaseCheck {
	if confirmation == nil || check == nil {
		return failed(interfaces.RELEASE_CHECK_CHECK, interfaces.RELEASE_REASON_CHECK_NOT_RUN, "", nil)
	}
	switch check.Status {
	case interfaces.CHECK_STATUS_PASSED:
		return passed(interfaces.RELEASE_CHECK_CHECK)
	case interfaces.CHECK_STATUS_PARTIAL:
		return failed(interfaces.RELEASE_CHECK_CHECK, interfaces.RELEASE_REASON_CHECK_PARTIAL,
			interfaces.RELEASE_ACCEPT_CHECK_PARTIAL, nil)
	case interfaces.CHECK_STATUS_RUNNING:
		return failed(interfaces.RELEASE_CHECK_CHECK, interfaces.RELEASE_REASON_CHECK_RUNNING, "", nil)
	case interfaces.CHECK_STATUS_ISSUES:
		return failed(interfaces.RELEASE_CHECK_CHECK, interfaces.RELEASE_REASON_CHECK_ISSUES, "", nil)
	}
	return failed(interfaces.RELEASE_CHECK_CHECK, interfaces.RELEASE_REASON_CHECK_FAILED, "", nil)
}

// indexCheck passes when the index of every adopted object type is ready. Indexes not ready, or
// not readable, can be accepted: full-text and vector search wait for them.
func indexCheck(confirmation *interfaces.DraftConfirmation, index *interfaces.ProjectIndex, err error) interfaces.ReleaseCheck {
	switch {
	case confirmation == nil:
		return failed(interfaces.RELEASE_CHECK_INDEX, interfaces.RELEASE_REASON_NOT_CONFIRMED, "", nil)
	case err != nil:
		return failed(interfaces.RELEASE_CHECK_INDEX, interfaces.RELEASE_REASON_INDEX_UNREADABLE,
			interfaces.RELEASE_ACCEPT_INDEX_NOT_READY, nil)
	}
	for _, it := range index.ObjectTypes {
		if it.State != interfaces.INDEX_STATE_READY {
			return failed(interfaces.RELEASE_CHECK_INDEX, interfaces.RELEASE_REASON_INDEX_NOT_READY,
				interfaces.RELEASE_ACCEPT_INDEX_NOT_READY, index.Counts)
		}
	}
	return passed(interfaces.RELEASE_CHECK_INDEX)
}

// baselineCheck passes while the live version is the one the branch started from.
func baselineCheck(behind *interfaces.ReleaseRefusal, preview *interfaces.ReleasePreview) interfaces.ReleaseCheck {
	if behind != nil {
		return failed(interfaces.RELEASE_CHECK_BASELINE, interfaces.RELEASE_REASON_BRANCH_BEHIND, "",
			map[string]any{"base_version": preview.BaseVersion, "live_network_id": preview.LiveNetworkID})
	}
	return passed(interfaces.RELEASE_CHECK_BASELINE)
}

// releaseChanges names the object types and relation types of a diff: added and changed ones as the
// branch has them, removed ones as main has them. network.bkn, which every release renames, is left
// out; other definitions are counted.
func releaseChanges(diff interfaces.PathDiff, branchFiles map[string][]byte,
	mainFiles map[string][]byte) interfaces.ReleaseChanges {
	changes := interfaces.ReleaseChanges{
		Added: []interfaces.ReleaseItem{}, Changed: []interfaces.ReleaseItem{}, Removed: []interfaces.ReleaseItem{},
	}
	for _, list := range []struct {
		paths []string
		files map[string][]byte
		into  *[]interfaces.ReleaseItem
	}{
		{diff.Added, branchFiles, &changes.Added},
		{diff.Changed, branchFiles, &changes.Changed},
		{diff.Removed, mainFiles, &changes.Removed},
	} {
		for _, p := range list.paths {
			switch {
			case p == bkn.RootFileName:
			case kindOf(p) == "":
				changes.Other++
			default:
				*list.into = append(*list.into, itemAt(p, list.files))
			}
		}
		sortItems(*list.into)
	}
	return changes
}

// releaseRisks are where the release touches what is live: the object types and relation types it
// changes or removes, the relations whose records partly refer to nothing, and the functions and
// Skills it copies. It only adds when it changes and removes no definition but network.bkn.
func releaseRisks(changes interfaces.ReleaseChanges, diff interfaces.PathDiff, check *interfaces.Check,
	files map[string][]byte, capabilities []interfaces.CapabilityPlan) interfaces.ReleaseRisks {
	existing := append(append([]interfaces.ReleaseItem{}, changes.Changed...), changes.Removed...)
	sortItems(existing)
	risks := interfaces.ReleaseRisks{Existing: existing, Orphans: orphansOf(check, files)}
	otherTouched := 0
	for _, p := range append(append([]string{}, diff.Changed...), diff.Removed...) {
		if p != bkn.RootFileName && kindOf(p) == "" {
			otherTouched++
		}
	}
	risks.OnlyAdditions = len(existing) == 0 && otherTouched == 0
	for _, c := range capabilities {
		if c.Copy {
			risks.Copies++
		}
	}
	return risks
}

// orphansOf sums, per relation type the check counted, the records that refer to nothing in its end.
func orphansOf(check *interfaces.Check, files map[string][]byte) []interfaces.ReleaseOrphans {
	orphans := []interfaces.ReleaseOrphans{}
	if check == nil {
		return orphans
	}
	var results struct {
		Relations []struct {
			RelationType string `json:"relation_type"`
			Joins        []struct {
				Orphans int64 `json:"orphans"`
			} `json:"joins"`
		} `json:"relations"`
	}
	if json.Unmarshal(check.Results, &results) != nil {
		return orphans
	}
	for _, rel := range results.Relations {
		n := int64(0)
		for _, j := range rel.Joins {
			n += j.Orphans
		}
		if n > 0 {
			orphans = append(orphans, interfaces.ReleaseOrphans{
				ReleaseItem: itemAt(relationTypesDir+rel.RelationType+".bkn", files), Orphans: n,
			})
		}
	}
	sort.Slice(orphans, func(i, j int) bool { return orphans[i].ID < orphans[j].ID })
	return orphans
}

// kindOf is the kind of the object type or relation type at p, or empty for another definition.
func kindOf(p string) string {
	switch {
	case strings.HasPrefix(p, objectTypesDir):
		return interfaces.RELEASE_ITEM_OBJECT_TYPE
	case strings.HasPrefix(p, relationTypesDir):
		return interfaces.RELEASE_ITEM_RELATION_TYPE
	}
	return ""
}

// itemAt names the object type or relation type at p in files; one that does not parse is named by
// its file.
func itemAt(p string, files map[string][]byte) interfaces.ReleaseItem {
	kind := kindOf(p)
	it := interfaces.ReleaseItem{Kind: kind, ID: strings.TrimSuffix(path.Base(p), path.Ext(p))}
	switch kind {
	case interfaces.RELEASE_ITEM_OBJECT_TYPE:
		if ot, err := bkn.ParseObjectTypeFile(string(files[p]), p); err == nil && ot.ID != "" {
			it.ID, it.Name = ot.ID, ot.Name
		}
	case interfaces.RELEASE_ITEM_RELATION_TYPE:
		if rt, err := bkn.ParseRelationTypeFile(string(files[p]), p); err == nil && rt.ID != "" {
			it.ID, it.Name = rt.ID, rt.Name
		}
	}
	if it.Name == "" {
		it.Name = it.ID
	}
	return it
}

func sortItems(items []interfaces.ReleaseItem) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Kind != items[j].Kind {
			return items[i].Kind < items[j].Kind
		}
		return items[i].ID < items[j].ID
	})
}
