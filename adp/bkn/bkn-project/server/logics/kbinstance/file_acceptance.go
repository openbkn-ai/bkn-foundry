// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"context"
	"errors"
	"time"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// ErrNothingToAccept refuses an acceptance that would release nothing or that
// does not apply to this build.
var ErrNothingToAccept = errors.New("build has no files to accept or none that succeeded")

// AcceptFileExclusion checks and applies a person's decision to release a full
// build without the files it lists. The caller fences it by revision, so what the
// person saw is exactly what is recorded. It changes only process metadata; the
// worker removed those files' rows when the build ended.
func AcceptFileExclusion(build *interfaces.KBBuild, by interfaces.AccountInfo, now int64) error {
	if build == nil || build.Mode != "full" || build.State != "needs_review" || now <= 0 {
		return ErrNothingToAccept
	}
	listed, usable := []interfaces.KBAcceptedFile{}, 0
	for index := range build.Files {
		file := &build.Files[index]
		if file.State == "evidence_only" || file.State == "succeeded" {
			usable++
			continue
		}
		reason := file.Reason
		if reason == "" {
			reason = "not_processed"
		}
		listed = append(listed, interfaces.KBAcceptedFile{File: file.File, Reason: reason})
		file.State, file.Reason = interfaces.KBFileExcluded, reason
	}
	for _, skipped := range build.Skipped {
		listed = append(listed, interfaces.KBAcceptedFile{File: skipped.File, Reason: skipped.Reason})
	}
	if len(listed) == 0 || usable == 0 {
		return ErrNothingToAccept
	}
	build.Acceptance = &interfaces.KBFileAcceptance{By: by, Time: now, Files: listed}
	build.State = "succeeded"
	return nil
}

// AcceptFiles records the owner's acceptance of the listed files at the revision
// they were shown.
func (s *BuildService) AcceptFiles(ctx context.Context, token, project, id string, revision int64, caller interfaces.AccountInfo) (*interfaces.KBBuild, error) {
	if _, err := s.owner(ctx, token, project, caller); err != nil {
		return nil, err
	}
	store, ok := s.Store.(interfaces.KBFileAcceptanceAccess)
	if !ok {
		return nil, kb.ErrUnavailable
	}
	if revision < 1 {
		return nil, &kb.SourceError{StatusCode: 400}
	}
	return store.AcceptFiles(ctx, project, id, revision, func(build *interfaces.KBBuild) error {
		return AcceptFileExclusion(build, caller, time.Now().UnixMilli())
	})
}
