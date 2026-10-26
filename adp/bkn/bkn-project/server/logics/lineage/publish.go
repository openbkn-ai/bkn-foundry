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
	maxNetworkIDLength  = 40
	maxNetworkNameRunes = 40
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

	if err := checkProjectBranch(ctx, req.Branch); err != nil {
		span.SetStatus(codes.Error, "Invalid branch")
		return nil, err
	}
	if len(req.Version) > maxVersionLength || !versionPattern.MatchString(req.Version) {
		span.SetStatus(codes.Error, "Invalid version")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Release_InvalidVersion)
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

func (s *lineageService) publish(ctx context.Context, gs interfaces.GitStore, token string, lineageID string,
	req interfaces.PublishReleaseRequest, publisher interfaces.AccountInfo,
	author interfaces.GitAuthor) (*interfaces.PublishReleaseResult, error) {
	networkID := releaseNetworkID(lineageID, req.Version)
	tag := "v" + req.Version

	// A recorded release owns its network id: this version of the lineage, or
	// another lineage whose truncated id is the same.
	if owner, exist, err := s.ra.GetReleaseByNetworkID(ctx, networkID); err != nil {
		return nil, err
	} else if exist {
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Release_VersionExists).
			WithErrorDetails(map[string]any{"network_id": networkID, "lineage_id": owner.LineageID, "version": owner.Version})
	}

	branchHead, err := gs.ResolveRef(ctx, lineageID, branchRef(req.Branch))
	if err != nil {
		return nil, err
	}
	// A tag without a release row was left by a failed attempt. Only the same
	// branch head may reuse it; anything else would make the tag lie.
	retry := false
	tagHead, err := gs.ResolveRef(ctx, lineageID, tagRef(tag))
	switch {
	case err == nil && tagHead == branchHead:
		retry = true
	case err == nil:
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Release_VersionExists).
			WithErrorDetails(map[string]any{"tag": tag, "commit": tagHead})
	case !errors.Is(err, plumbing.ErrReferenceNotFound):
		return nil, err
	}

	// The baseline is the newest release; before the first one it is the lineage's own network.
	liveID, baseVersion := lineageID, ""
	latest, err := s.ra.ListReleases(ctx, interfaces.ReleasesQuery{LineageID: lineageID, Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(latest) > 0 {
		liveID, baseVersion = latest[0].NetworkID, latest[0].Version
	}

	mainHead, err := gs.ResolveRef(ctx, lineageID, branchRef(interfaces.GIT_MAIN_BRANCH))
	if err != nil {
		return nil, err
	}
	base, err := gs.MergeBase(ctx, lineageID, mainHead, branchHead)
	if err != nil {
		return nil, err
	}
	if base != mainHead {
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Release_BranchBehind).
			WithErrorDetails(map[string]any{"main": mainHead, "branch": branchHead})
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
	if diff := drift.DiffFiles(stored, live); !diff.Empty() || !capabilityDiff.Empty() {
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Release_Drift).
			WithErrorDetails(&interfaces.DriftReport{LineageID: lineageID, KNID: liveID, PathDiff: diff, Capabilities: capabilityDiff})
	}

	// The import runs in overwrite mode, so a network that already has this id and that no
	// release created must not be touched. On a retry the network is the one the failed
	// attempt created.
	if !retry {
		_, err := s.bba.ExportBKN(ctx, token, networkID)
		var backendErr *interfaces.BknBackendError
		switch {
		case err == nil:
			return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Release_NetworkExists).
				WithErrorDetails(map[string]any{"network_id": networkID})
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
	pkg, err := buildPackage(files, networkID, req.Version)
	if err != nil {
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Lineage_InvalidDefinitions).
			WithErrorDetails(err.Error())
	}
	if !retry {
		if err := gs.Tag(ctx, lineageID, tag, branchHead); err != nil {
			return nil, err
		}
	}
	// Functions and Skills follow the network: copies are made before the import
	// so the new network is created pointing at them.
	pkg, released, err := s.followCapabilities(ctx, token, pkg, previous, tracked, liveID, networkID)
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
		State:       interfaces.RELEASE_STATE_LIVE,
		CreateTime:  now,
		UpdateTime:  now,
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

	return &interfaces.PublishReleaseResult{Release: release, NetworkID: networkID, Tag: tag, Commit: commit}, nil
}

// followCapabilities reuses or copies each toolbox and Skill the package
// references (see capability.Plan) and points the package at the copies. It
// returns the package and the items the new network uses, as git keeps them.
// tracked are the live items the previous release recorded, already exported.
func (s *lineageService) followCapabilities(ctx context.Context, token string, pkg map[string][]byte,
	previous *capability.Manifest, tracked []*capability.Item, oldNetworkID string,
	networkID string) (map[string][]byte, []*capability.Item, error) {
	network, err := loadNetwork(pkg)
	if err != nil {
		return nil, nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Lineage_InvalidDefinitions).
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
		return nil, nil, err
	}
	items = append(items, exported...)

	copies, released, err := capability.Apply(ctx, s.efa, token, capability.Plan(previous, items, oldNetworkID),
		oldNetworkID, networkID)
	if err != nil {
		return nil, nil, err
	}
	rewritten, err := capability.RewritePackage(pkg, oldNetworkID, networkID, copies)
	if err == nil {
		network, err = loadNetwork(rewritten)
	}
	if err == nil {
		// A reference the rewrite did not reach would leave the new network on the original.
		boxIDs, skillIDs = capability.References(network)
		for _, c := range copies {
			if slices.Contains(boxIDs, c.OldID) || slices.Contains(skillIDs, c.OldID) {
				err = fmt.Errorf("%s %s was copied to %s, but the definitions still reference it", c.Kind, c.OldID, c.NewID)
				break
			}
		}
	}
	if err != nil {
		return nil, nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Lineage_InvalidDefinitions).
			WithErrorDetails(err.Error())
	}
	return rewritten, released, nil
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
// 40 characters of [a-z0-9_-], so a long lineage id is cut, and the "_" or "-"
// the cut leaves at its end dropped. The version always survives whole.
func releaseNetworkID(lineageID string, version string) string {
	suffix := "_v" + strings.ReplaceAll(version, ".", "_")
	prefix := lineageID
	if keep := maxNetworkIDLength - len(suffix); len(prefix) > keep {
		prefix = strings.TrimRight(prefix[:keep], "_-")
	}
	return prefix + suffix
}

// releaseNetworkName is <base name> · <version>, at most 40 characters, the base
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
