// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package kbrefresh refreshes instances under a released model. Source scans
// must complete and current publication permissions must be checked before a
// manifest is planned; planning itself grants no right to read or write data.
package kbrefresh

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

var ErrInvalidManifest = errors.New("refresh manifest is incomplete or unacknowledged")

// ManifestPlan contains process metadata only. Removed scopes are retired after
// every replacement is acknowledged; changed file scopes also prune their own
// obsolete rows using the new write keep-set. No extracted values enter it.
type ManifestPlan struct {
	Baseline *interfaces.KBBuild
	Build    *interfaces.KBBuild
	Changed  []int
	Removed  []interfaces.KBResourceFile
	Reused   int
}

// Plan preserves the actual task/prompt/model and row receipts for unchanged
// versions. Renaming a file does not cause another model invocation or row write.
// A version or original-inclusion policy change requires a new validated result.
func Plan(id string, previous *interfaces.KBBuild, current []interfaces.KBResourceFile) (*ManifestPlan, error) {
	if strings.TrimSpace(id) == "" || previous == nil || previous.ID == "" || previous.Mode != "full" || previous.State != "succeeded" || len(previous.Files) > 1000 || len(current) > 1000 {
		return nil, ErrInvalidManifest
	}
	validFile := func(file interfaces.KBResourceFile) bool {
		return strings.TrimSpace(file.KnowledgeBaseID) != "" && strings.TrimSpace(file.FileID) != "" && strings.TrimSpace(file.ContentVersionID) != ""
	}
	old := map[string]interfaces.KBFileBuild{}
	for _, step := range previous.Files {
		if !validFile(step.File) || !step.Completed() {
			return nil, ErrInvalidManifest
		}
		if _, duplicate := old[step.File.Key()]; duplicate {
			return nil, ErrInvalidManifest
		}
		old[step.File.Key()] = step
	}
	// Deep-copy all execution receipts, including pointer-valued model/assessment
	// metadata. The next journal must never mutate the preceding release's proof.
	raw, err := json.Marshal(previous)
	if err != nil {
		return nil, ErrInvalidManifest
	}
	var build interfaces.KBBuild
	if json.Unmarshal(raw, &build) != nil {
		return nil, ErrInvalidManifest
	}
	copied := map[string]interfaces.KBFileBuild{}
	for _, step := range build.Files {
		copied[step.File.Key()] = step
	}
	now := time.Now().UnixMilli()
	build.ID, build.State, build.Revision = id, "preparing", 1
	build.CreateTime, build.UpdateTime = now, now
	linking, assessment := build.Linking, build.Assessment
	build.Linking, build.Assessment = nil, nil
	build.Files = []interfaces.KBFileBuild{}
	build.Skipped, build.Acceptance = nil, nil
	var baseline interfaces.KBBuild
	if json.Unmarshal(raw, &baseline) != nil {
		return nil, ErrInvalidManifest
	}
	result := &ManifestPlan{Baseline: &baseline, Build: &build, Changed: []int{}, Removed: []interfaces.KBResourceFile{}}
	files := slices.Clone(current)
	slices.SortFunc(files, func(a, b interfaces.KBResourceFile) int { return strings.Compare(a.Key(), b.Key()) })
	seen := map[string]bool{}
	for _, file := range files {
		key := file.Key()
		if !validFile(file) || seen[key] {
			return nil, ErrInvalidManifest
		}
		seen[key] = true
		before, exists := copied[key]
		// A file left out of the release has no result to carry; try it again.
		if exists && before.State != interfaces.KBFileExcluded && before.File.ContentVersionID == file.ContentVersionID && before.File.IncludeOriginal == file.IncludeOriginal {
			before.File = file
			build.Files = append(build.Files, before)
			result.Reused++
		} else {
			result.Changed = append(result.Changed, len(build.Files))
			build.Files = append(build.Files, interfaces.KBFileBuild{File: file, State: "pending"})
			if exists && before.State != interfaces.KBFileExcluded && before.File.ContentVersionID != file.ContentVersionID {
				result.Removed = append(result.Removed, before.File)
			}
		}
	}
	for key, before := range old {
		if !seen[key] && before.State != interfaces.KBFileExcluded {
			result.Removed = append(result.Removed, before.File)
		}
	}
	slices.SortFunc(result.Removed, func(a, b interfaces.KBResourceFile) int { return strings.Compare(a.Key(), b.Key()) })
	if len(result.Changed) == 0 && len(result.Removed) == 0 {
		build.Linking, build.Assessment = linking, assessment
	}
	return result, nil
}
