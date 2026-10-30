// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package lineage

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/bkn-specification/bkn"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"
	"gopkg.in/yaml.v3"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/capability"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/drift"
)

const (
	// maxNetworkIDLength and maxNetworkNameRunes are the bkn-backend limits on a network.
	maxNetworkIDLength  = 128
	maxNetworkNameRunes = 128
	// maxProjectIDRunes and maxNotesBytes are what the release table holds:
	// f_project_id VARCHAR(40) and f_notes TEXT.
	maxProjectIDRunes = 40
	maxNotesBytes     = 65535
	// maxVersionLength leaves room for the lineage in the network id and for the
	// base name in the network name.
	maxVersionLength = 32
	// networkNameSeparator joins the base name and the version in a release name.
	networkNameSeparator = " · "
)

var (
	versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	// releaseNameSuffix matches the version a previous release appended to the
	// name; main carries that name once the release is synced back.
	releaseNameSuffix = regexp.MustCompile(`\s*·\s*[0-9]+\.[0-9]+\.[0-9]+$`)
)

// PublishRelease checks, tags, imports and records a release under one hold of the
// lineage lock, so neither main nor the tags move while it runs.
//
// Every check, the release package included, runs before the tag, the first
// side effect. main moves only after the release is recorded. Until then a
// failed attempt leaves main and the release table as they were, and at most
// the tag behind: a retry of the same version from the same branch head reuses
// that tag, and the import overwrites the network the failed attempt may have
// created.
func (s *lineageService) PublishRelease(ctx context.Context, token string, lineageID string,
	req interfaces.PublishReleaseRequest, publisher interfaces.AccountInfo,
	author interfaces.GitAuthor) (*interfaces.PublishReleaseResult, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "PublishRelease")
	defer span.End()

	if err := checkReleaseRequest(ctx, req.Branch, req.Version); err != nil {
		span.SetStatus(codes.Error, "Invalid request")
		return nil, err
	}
	// The release row is written after the import; a value it cannot hold must
	// not fail the release only then.
	if utf8.RuneCountInString(req.ProjectID) > maxProjectIDRunes {
		span.SetStatus(codes.Error, "Invalid project id")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Release_InvalidProjectID)
	}
	if len(req.Notes) > maxNotesBytes {
		span.SetStatus(codes.Error, "Invalid notes")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Release_InvalidNotes)
	}

	var result *interfaces.PublishReleaseResult
	err := s.gs.WithLineageLock(ctx, lineageID, func(gs interfaces.GitStore) error {
		var err error
		result, err = s.publish(ctx, gs, token, lineageID, req, publisher, author)
		return err
	})
	if err != nil {
		span.SetStatus(codes.Error, "Publish release failed")
		return nil, toHTTPError(ctx, "Publish release failed", err)
	}
	span.SetStatus(codes.Ok, "")
	return result, nil
}

// PreviewRelease runs the checks of a release without the lock: it changes nothing, and what it
// reads may move before a release, which checks it all again under the lock.
func (s *lineageService) PreviewRelease(ctx context.Context, token string, lineageID string, branch string,
	version string) (*interfaces.ReleasePreview, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "PreviewRelease")
	defer span.End()

	if err := checkReleaseRequest(ctx, branch, version); err != nil {
		span.SetStatus(codes.Error, "Invalid request")
		return nil, err
	}
	checked, err := s.checkRelease(ctx, s.gs, token, lineageID, branch, version, true)
	if err != nil {
		span.SetStatus(codes.Error, "Preview release failed")
		return nil, toHTTPError(ctx, "Preview release failed", err)
	}
	span.SetStatus(codes.Ok, "")
	return checked.preview, nil
}

// checkReleaseRequest refuses a branch or version no release can have.
func checkReleaseRequest(ctx context.Context, branch string, version string) error {
	if err := checkProjectBranch(ctx, branch); err != nil {
		return err
	}
	if len(version) > maxVersionLength || !versionPattern.MatchString(version) {
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Release_InvalidVersion)
	}
	return nil
}

// releaseCheck is what the checks of a release read and decided, for publishing to go on from.
type releaseCheck struct {
	preview *interfaces.ReleasePreview
	// refusals are the replies publishing is refused with, in the order they were found.
	refusals []error
	// retry is a tag a failed attempt left at the branch head, to reuse.
	retry    bool
	previous *capability.Manifest
	pkg      map[string][]byte
	steps    []*capability.Step
}

func (s *lineageService) publish(ctx context.Context, gs interfaces.GitStore, token string, lineageID string,
	req interfaces.PublishReleaseRequest, publisher interfaces.AccountInfo,
	author interfaces.GitAuthor) (*interfaces.PublishReleaseResult, error) {
	checked, err := s.checkRelease(ctx, gs, token, lineageID, req.Branch, req.Version, false)
	if err != nil {
		return nil, err
	}
	if len(checked.refusals) > 0 {
		return nil, checked.refusals[0]
	}
	preview := checked.preview
	networkID, tag, branchHead := preview.NetworkID, preview.Tag, preview.BranchCommit
	liveID, baseVersion, previous := preview.LiveNetworkID, preview.BaseVersion, checked.previous

	if !checked.retry {
		if err := gs.Tag(ctx, lineageID, tag, branchHead); err != nil {
			return nil, err
		}
	}
	pkg, released, err := s.applyCapabilities(ctx, token, checked.pkg, checked.steps, liveID, networkID)
	if err != nil {
		return nil, err
	}

	imported, err := s.bba.ImportBKN(ctx, token, pkg)
	if err != nil {
		return nil, err
	}
	if imported.Capabilities != nil && len(imported.Capabilities.Skipped) > 0 {
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Release_CapabilitiesSkipped).
			WithErrorDetails(map[string]any{"network_id": networkID, "tag": tag, "skipped": imported.Capabilities.Skipped})
	}

	id, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	now := time.Now().UnixMilli()
	release := &interfaces.Release{
		ID:          id.String(),
		LineageID:   lineageID,
		NetworkID:   networkID,
		Version:     req.Version,
		BaseVersion: baseVersion,
		ProjectID:   req.ProjectID,
		Publisher:   publisher,
		Notes:       req.Notes,
		// The newest release is live; the state is worked out, not stored.
		State:      interfaces.RELEASE_STATE_LIVE,
		CreateTime: now,
		UpdateTime: now,
	}
	if err := s.ra.CreateRelease(ctx, release); err != nil {
		return nil, err
	}

	commit, err := gs.FastForward(ctx, lineageID, interfaces.GIT_MAIN_BRANCH, branchHead)
	if err != nil {
		return nil, err
	}
	// The import re-serializes what it stores, network.bkn above all. Committing
	// the export keeps the next drift check clean. Should the export fail, that
	// drift check reports the difference instead, so the release stands.
	exported, err := s.bba.ExportBKN(ctx, token, networkID)
	if err != nil {
		otellog.LogWarn(ctx, fmt.Sprintf("Export released network %s failed, main keeps the tag: %v", networkID, err))
	} else if commit, err = gs.CommitFiles(ctx, lineageID, interfaces.GIT_MAIN_BRANCH, exported,
		fmt.Sprintf("sync live definitions after release %s", tag), author); err != nil {
		return nil, err
	}
	// release.yaml is what the next release compares against; a lineage that
	// never used a function or Skill gets none.
	if len(released) > 0 || !previous.Empty() {
		manifest, err := capability.NextManifest(previous, released, networkID).Marshal()
		if err != nil {
			return nil, err
		}
		recorded := capability.Files(released)
		recorded[interfaces.GIT_MANIFEST_FILE] = manifest
		if commit, err = gs.CommitCapabilities(ctx, lineageID, interfaces.GIT_MAIN_BRANCH, recorded,
			fmt.Sprintf("record functions and Skills of release %s", tag), author); err != nil {
			return nil, err
		}
	}
	// main now has what the release committed after the branch head; the branch follows it, so the
	// project's next release starts from the version it published, not behind it. Should this fail,
	// the next release review shows the branch behind and syncing brings it up, so the release stands.
	if _, err := gs.FastForward(ctx, lineageID, req.Branch, commit); err != nil {
		otellog.LogWarn(ctx, fmt.Sprintf("Bring branch %s up to main after release %s failed: %v", req.Branch, tag, err))
	}

	return &interfaces.PublishReleaseResult{Release: release, NetworkID: networkID, Tag: tag, Commit: commit}, nil
}

// checkRelease runs every check of publishing the branch as version, in the order publishing
// needs them, without acting on any. A check that refuses is recorded as a refusal; with all it
// goes on to the next, otherwise it stops there. Definitions that make no release package stop
// it either way: nothing is left to plan. A failure to read is an error.
func (s *lineageService) checkRelease(ctx context.Context, gs interfaces.GitStore, token string, lineageID string,
	branch string, version string, all bool) (*releaseCheck, error) {
	networkID := releaseNetworkID(lineageID, version)
	checked := &releaseCheck{preview: &interfaces.ReleasePreview{
		LineageID: lineageID, Branch: branch, Version: version, Tag: "v" + version, NetworkID: networkID,
		Capabilities: []interfaces.CapabilityPlan{}, Refusals: []interfaces.ReleaseRefusal{},
	}}
	preview := checked.preview
	// refuse records a refusal and tells whether to stop.
	refuse := func(err *httperrors.HTTPError) bool {
		checked.refusals = append(checked.refusals, err)
		preview.Refusals = append(preview.Refusals, interfaces.ReleaseRefusal{Code: err.BaseError.ErrorCode, Details: err.ErrorDetails})
		return !all
	}

	// A recorded release owns its network id: this version of the lineage, or
	// another lineage whose truncated id is the same.
	owner, recorded, err := s.ra.GetReleaseByNetworkID(ctx, networkID)
	if err != nil {
		return nil, err
	}
	if recorded && refuse(httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Release_VersionExists).
		WithErrorDetails(map[string]any{"network_id": networkID, "lineage_id": owner.LineageID, "version": owner.Version})) {
		return checked, nil
	}

	branchHead, err := gs.ResolveRef(ctx, lineageID, branchRef(branch))
	if err != nil {
		return nil, err
	}
	preview.BranchCommit = branchHead
	// A tag without a release row was left by a failed attempt, which may have
	// imported the network before recording failed. Only the same branch head may
	// reuse it: the retry imports over that network and records the release.
	// Anything else would make the tag lie; the version stays unfinished.
	tagHead, err := gs.ResolveRef(ctx, lineageID, tagRef(preview.Tag))
	switch {
	case err == nil && tagHead == branchHead:
		checked.retry = true
	case err == nil:
		if refuse(httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Release_UnfinishedAttempt).
			WithErrorDetails(map[string]any{"tag": preview.Tag, "commit": tagHead, "branch_commit": branchHead,
				"network_id": networkID})) {
			return checked, nil
		}
	case !errors.Is(err, plumbing.ErrReferenceNotFound):
		return nil, err
	}

	// The baseline is the newest release; before the first one it is the lineage's own network.
	preview.LiveNetworkID = lineageID
	latest, err := s.ra.ListReleases(ctx, interfaces.ReleasesQuery{LineageID: lineageID, Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(latest) > 0 {
		preview.LiveNetworkID, preview.BaseVersion = latest[0].NetworkID, latest[0].Version
	}
	liveID := preview.LiveNetworkID

	mainHead, err := gs.ResolveRef(ctx, lineageID, branchRef(interfaces.GIT_MAIN_BRANCH))
	if err != nil {
		return nil, err
	}
	preview.MainCommit = mainHead
	base, err := gs.MergeBase(ctx, lineageID, mainHead, branchHead)
	if err != nil {
		return nil, err
	}
	if base != mainHead && refuse(httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Release_BranchBehind).
		WithErrorDetails(map[string]any{"main": mainHead, "branch": branchHead})) {
		return checked, nil
	}

	// Edits made to the live network since main was last synced would be lost silently.
	live, err := s.bba.ExportBKN(ctx, token, liveID)
	if err != nil {
		return nil, err
	}
	stored, err := gs.ReadTree(ctx, lineageID, mainHead)
	if err != nil {
		return nil, err
	}
	// So would edits made to the functions and Skills the last release recorded.
	previous, tracked, capabilityDiff, err := drift.CheckCapabilities(ctx, gs, s.efa, token, lineageID, mainHead)
	if err != nil {
		return nil, err
	}
	checked.previous = previous
	if diff := drift.DiffFiles(stored, live); !diff.Empty() || !capabilityDiff.Empty() {
		if refuse(httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Release_Drift).
			WithErrorDetails(&interfaces.DriftReport{LineageID: lineageID, KNID: liveID, PathDiff: diff, Capabilities: capabilityDiff})) {
			return checked, nil
		}
	}

	// The import runs in overwrite mode, so a network that already has this id and that no
	// release created must not be touched. On a retry the network is the one the failed
	// attempt created, and a recorded release owns its network.
	if !checked.retry && !recorded {
		_, err := s.bba.ExportBKN(ctx, token, networkID)
		var backendErr *interfaces.BknBackendError
		switch {
		case err == nil:
			if refuse(httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Release_NetworkExists).
				WithErrorDetails(map[string]any{"network_id": networkID})) {
				return checked, nil
			}
		case !errors.As(err, &backendErr) || backendErr.HTTPCode != http.StatusNotFound:
			return nil, err
		}
	}

	// The package is built from the branch head before the tag is made, so
	// definitions the release cannot rename leave no tag that would block the
	// same version once the branch is fixed. On a retry the tag is that head.
	files, err := gs.ReadTree(ctx, lineageID, branchHead)
	if err != nil {
		return nil, err
	}
	preview.Changes = drift.DiffFiles(stored, files)
	pkg, err := buildPackage(files, networkID, version)
	if err != nil {
		refuse(httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Lineage_InvalidDefinitions).
			WithErrorDetails(err.Error()))
		return checked, nil
	}
	if header, err := bkn.ParseNetworkFile(string(pkg[bkn.RootFileName]), bkn.RootFileName); err == nil {
		preview.NetworkName = header.Name
	}
	// Functions and Skills follow the network. What to reuse or copy, and whether
	// the package's references can move to the copies, is settled before the tag
	// too; the copies are made after it, before the import, so the new network
	// is created pointing at them.
	steps, err := s.planCapabilities(ctx, token, pkg, previous, tracked, liveID, networkID)
	var refusal *httperrors.HTTPError
	if errors.As(err, &refusal) {
		refuse(refusal)
		return checked, nil
	}
	if err != nil {
		return nil, err
	}
	checked.pkg, checked.steps = pkg, steps
	for _, step := range steps {
		preview.Capabilities = append(preview.Capabilities, interfaces.CapabilityPlan{
			Kind: string(step.Item.Kind), ID: step.Item.ID, Name: step.Item.Name, Copy: step.Copy,
			Reasons: append([]string{}, step.Reasons...),
		})
	}
	return checked, nil
}

// planCapabilities decides for each toolbox and Skill the package references
// whether it is reused or copied (see capability.Plan), and checks that the
// package's references can move to the copies, without making any. tracked
// are the live items the previous release recorded, already exported.
func (s *lineageService) planCapabilities(ctx context.Context, token string, pkg map[string][]byte,
	previous *capability.Manifest, tracked []*capability.Item, oldNetworkID string,
	networkID string) ([]*capability.Step, error) {
	network, err := loadNetwork(pkg)
	if err != nil {
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Lineage_InvalidDefinitions).
			WithErrorDetails(err.Error())
	}
	boxIDs, skillIDs := capability.References(network)
	known := map[string]*capability.Item{}
	for _, it := range tracked {
		known[it.Dir()] = it
	}
	items := []*capability.Item{}
	var newBoxes, newSkills []string
	for _, id := range boxIDs {
		if it, ok := known[(&capability.Item{Kind: capability.KIND_FUNCTION, ID: id}).Dir()]; ok {
			items = append(items, it)
		} else {
			newBoxes = append(newBoxes, id)
		}
	}
	for _, id := range skillIDs {
		if it, ok := known[(&capability.Item{Kind: capability.KIND_SKILL, ID: id}).Dir()]; ok {
			items = append(items, it)
		} else {
			newSkills = append(newSkills, id)
		}
	}
	exported, err := capability.Export(ctx, s.efa, token, newBoxes, newSkills)
	if err != nil {
		return nil, err
	}
	items = append(items, exported...)

	steps := capability.Plan(previous, items, oldNetworkID)
	copies, err := capability.Preview(steps, oldNetworkID, networkID)
	if err != nil {
		return nil, err
	}
	if _, err := rewriteToCopies(pkg, oldNetworkID, networkID, copies); err != nil {
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Lineage_InvalidDefinitions).
			WithErrorDetails(err.Error())
	}
	return steps, nil
}

// applyCapabilities makes the copies the steps call for and points the package
// at them. It returns the package and the items the new network uses, as git
// keeps them.
func (s *lineageService) applyCapabilities(ctx context.Context, token string, pkg map[string][]byte,
	steps []*capability.Step, oldNetworkID string, networkID string) (map[string][]byte, []*capability.Item, error) {
	copies, released, err := capability.Apply(ctx, s.efa, token, steps, oldNetworkID, networkID)
	if err != nil {
		return nil, nil, err
	}
	rewritten, err := rewriteToCopies(pkg, oldNetworkID, networkID, copies)
	if err != nil {
		return nil, nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Lineage_InvalidDefinitions).
			WithErrorDetails(err.Error())
	}
	return rewritten, released, nil
}

// rewriteToCopies points the package at the copies and checks that no
// reference the rewrite did not reach would leave the new network on an original.
func rewriteToCopies(pkg map[string][]byte, oldNetworkID string, networkID string,
	copies []*capability.Copy) (map[string][]byte, error) {
	rewritten, err := capability.RewritePackage(pkg, oldNetworkID, networkID, copies)
	if err != nil {
		return nil, err
	}
	network, err := loadNetwork(rewritten)
	if err != nil {
		return nil, err
	}
	boxIDs, skillIDs := capability.References(network)
	for _, c := range copies {
		if slices.Contains(boxIDs, c.OldID) || slices.Contains(skillIDs, c.OldID) {
			return nil, fmt.Errorf("%s %s was copied to %s, but the definitions still reference it", c.Kind, c.OldID, c.NewID)
		}
	}
	return rewritten, nil
}

// loadNetwork reads a definition set the way bkn-backend import does.
func loadNetwork(files map[string][]byte) (*bkn.BknNetwork, error) {
	fsys := bkn.NewMemoryFileSystem()
	fsys.AddFiles(files)
	return bkn.LoadNetworkWithFS(fsys, ".")
}

// tagRef names a tag in full, so a branch of the same name never shadows it.
func tagRef(name string) string {
	return plumbing.NewTagReferenceName(name).String()
}

// releaseNetworkID is <lineage_id>_v<major>_<minor>_<patch>. bkn-backend allows
// 128 characters of [a-z0-9_-], so a long lineage id is cut, and the "_" or "-"
// the cut leaves at its end dropped. The version always survives whole.
func releaseNetworkID(lineageID string, version string) string {
	suffix := "_v" + strings.ReplaceAll(version, ".", "_")
	prefix := lineageID
	if keep := maxNetworkIDLength - len(suffix); len(prefix) > keep {
		prefix = strings.TrimRight(prefix[:keep], "_-")
	}
	return prefix + suffix
}

// releaseNetworkName is <base name> · <version>, at most 128 characters, the base
// name cut to fit. The base name is the branch's network name without the
// version an earlier release appended to it.
func releaseNetworkName(name string, version string) string {
	suffix := networkNameSeparator + version
	base := strings.TrimSpace(releaseNameSuffix.ReplaceAllString(name, ""))
	if keep := maxNetworkNameRunes - utf8.RuneCountInString(suffix); utf8.RuneCountInString(base) > keep {
		base = strings.TrimSpace(string([]rune(base)[:keep]))
	}
	return base + suffix
}

// buildPackage gives the definitions of a release their network id and name and
// checks that the BKN loader reads the result as that network.
func buildPackage(files map[string][]byte, networkID string, version string) (map[string][]byte, error) {
	text, ok := files[bkn.RootFileName]
	if !ok {
		return nil, fmt.Errorf("%s not found", bkn.RootFileName)
	}
	current, err := bkn.ParseNetworkFile(string(text), bkn.RootFileName)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", bkn.RootFileName, err)
	}
	name := releaseNetworkName(current.Name, version)
	rewritten, err := setNetworkHeader(string(text), networkID, name)
	if err != nil {
		return nil, err
	}

	pkg := make(map[string][]byte, len(files))
	for p, data := range files {
		pkg[p] = data
	}
	pkg[bkn.RootFileName] = []byte(rewritten)

	network, err := loadNetwork(pkg)
	if err != nil {
		return nil, err
	}
	if network.ID != networkID || network.Name != name {
		return nil, fmt.Errorf("%s reads as %q (%q) after the rewrite, not %q (%q)",
			bkn.RootFileName, network.ID, network.Name, networkID, name)
	}
	return pkg, nil
}

// setNetworkHeader replaces the lines of the id and name keys in the frontmatter
// of network.bkn and keeps every other byte. Serializing the parsed network
// instead would rebuild the whole file: the SDK serializer writes 2.x
// frontmatter and needs every definition loaded to write the overview tables.
func setNetworkHeader(text string, id string, name string) (string, error) {
	return common.EditFrontmatter(text, bkn.RootFileName, func(root *yaml.Node, set common.FrontmatterSetter) error {
		for key, replacement := range map[string]string{"id": id, "name": name} {
			k, v := common.MappingValue(root, key)
			if k == nil {
				return fmt.Errorf("%s frontmatter has no id or name", bkn.RootFileName)
			}
			if err := set(k, v, replacement); err != nil {
				return err
			}
		}
		return nil
	})
}
