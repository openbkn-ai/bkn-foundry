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

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/drift"
)

const (
	// maxNetworkIDLength and maxNetworkNameRunes are the bkn-backend limits on a network.
	maxNetworkIDLength  = 40
	maxNetworkNameRunes = 40
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
// main moves only after the release is recorded. Until then a failed attempt
// leaves main and the release table as they were, and only the tag behind: a
// retry of the same version from the same branch head reuses that tag, and the
// import overwrites the network the failed attempt may have created.
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
	if diff := drift.DiffFiles(stored, live); !diff.Empty() {
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Release_Drift).
			WithErrorDetails(&interfaces.DriftReport{LineageID: lineageID, KNID: liveID, PathDiff: diff})
	}

	if !retry {
		if err := gs.Tag(ctx, lineageID, tag, branchHead); err != nil {
			return nil, err
		}
	}
	files, err := gs.ReadTree(ctx, lineageID, tagRef(tag))
	if err != nil {
		return nil, err
	}
	pkg, err := buildPackage(files, networkID, req.Version)
	if err != nil {
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Lineage_InvalidDefinitions).
			WithErrorDetails(err.Error())
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

	return &interfaces.PublishReleaseResult{Release: release, NetworkID: networkID, Tag: tag, Commit: commit}, nil
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

	fsys := bkn.NewMemoryFileSystem()
	fsys.AddFiles(pkg)
	network, err := bkn.LoadNetworkWithFS(fsys, ".")
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
	const delimiter = "---"
	bom := ""
	if strings.HasPrefix(text, "\ufeff") {
		bom, text = "\ufeff", strings.TrimPrefix(text, "\ufeff")
	}
	open := strings.Index(text, "\n")
	if !strings.HasPrefix(text, delimiter) || open < 0 {
		return "", fmt.Errorf("%s has no frontmatter", bkn.RootFileName)
	}
	end := strings.Index(text[open:], "\n"+delimiter)
	if end < 0 {
		return "", fmt.Errorf("%s has no frontmatter end", bkn.RootFileName)
	}
	frontmatter := text[open+1 : open+end+1]

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(frontmatter), &doc); err != nil {
		return "", fmt.Errorf("parse %s frontmatter: %w", bkn.RootFileName, err)
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return "", fmt.Errorf("%s frontmatter is not a mapping", bkn.RootFileName)
	}
	values := map[string]string{"id": id, "name": name}
	lines := strings.Split(frontmatter, "\n")
	pairs := doc.Content[0].Content
	for i := 0; i+1 < len(pairs); i += 2 {
		key, value := pairs[i], pairs[i+1]
		replacement, ok := values[key.Value]
		if !ok {
			continue
		}
		// Only a value on the key's own line can be swapped line for line.
		if value.Kind != yaml.ScalarNode || value.Line != key.Line ||
			value.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
			return "", fmt.Errorf("%s frontmatter %s is not a one-line value", bkn.RootFileName, key.Value)
		}
		encoded, err := yaml.Marshal(map[string]string{key.Value: replacement})
		if err != nil {
			return "", err
		}
		line := strings.TrimSuffix(string(encoded), "\n")
		if strings.HasSuffix(lines[key.Line-1], "\r") {
			line += "\r"
		}
		lines[key.Line-1] = line
		delete(values, key.Value)
	}
	if len(values) > 0 {
		return "", fmt.Errorf("%s frontmatter has no id or name", bkn.RootFileName)
	}
	return bom + text[:open+1] + strings.Join(lines, "\n") + text[open+end+1:], nil
}
