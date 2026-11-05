// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"

// ReuseCompleted carries finished files from earlier builds of the same Dataset
// generation into a new build, so a new build only extracts what is missing. The
// rows are already in place under stable ids. prior must be newest first: only
// the newest build that touched a file says what its rows are, since a later
// failed or cancelled attempt may have replaced or removed them.
//
// An acceptance build measures fresh output and never reuses.
func ReuseCompleted(build *interfaces.KBBuild, prior []*interfaces.KBBuild) {
	if build == nil || build.Mode == "acceptance" {
		return
	}
	for index := range build.Files {
		file := &build.Files[index]
		for _, candidate := range prior {
			if candidate == nil || candidate.ID == build.ID || candidate.GenerationID != build.GenerationID {
				continue
			}
			step := touchedStep(candidate, file.File)
			if step == nil {
				continue
			}
			if reusable(build, candidate, step) {
				file.State, file.Reason = step.State, step.Reason
				file.AgentTaskID, file.AgentVersion, file.PromptVersion, file.ModelID, file.Model = step.AgentTaskID, step.AgentVersion, step.PromptVersion, step.ModelID, step.Model
				file.InputDigest, file.RowsDigest, file.RowsWritten, file.DurationMillis = step.InputDigest, step.RowsDigest, step.RowsWritten, step.DurationMillis
				file.ReusedFrom = candidate.ID
			}
			break
		}
	}
}

func touchedStep(build *interfaces.KBBuild, file interfaces.KBResourceFile) *interfaces.KBFileBuild {
	for i := range build.Files {
		step := &build.Files[i]
		if step.File.Key() == file.Key() && step.State != "pending" {
			if step.File.ContentVersionID != file.ContentVersionID || step.File.IncludeOriginal != file.IncludeOriginal {
				return &interfaces.KBFileBuild{State: "failed"}
			}
			return step
		}
	}
	return nil
}

func reusable(build, candidate *interfaces.KBBuild, step *interfaces.KBFileBuild) bool {
	if candidate.ModelFingerprint != build.ModelFingerprint || candidate.AgentVersion != build.AgentVersion || len(step.InputDigest) != 64 || len(step.RowsDigest) != 64 {
		return false
	}
	if step.State == "evidence_only" {
		return true
	}
	if step.State != "succeeded" || step.AgentTaskID == "" || step.ModelID == "" || step.PromptVersion == nil || step.AgentVersion == nil {
		return false
	}
	return sameExecution(build, step)
}

// sameExecution compares with the build's pinned execution; unpinned is settled
// when the build finishes, by settleReusedExecution.
func sameExecution(build *interfaces.KBBuild, step *interfaces.KBFileBuild) bool {
	if build.ModelID != "" && build.ModelID != step.ModelID {
		return false
	}
	return build.PromptVersion == nil || step.PromptVersion == nil || *build.PromptVersion == *step.PromptVersion
}

// settleReusedExecution runs once all files have ended. Agent versions may keep
// mutable model defaults, so a reused result can only be checked against what the
// build actually ran. It returns whether a file went back to be extracted again.
func settleReusedExecution(build *interfaces.KBBuild) bool {
	requeued := false
	for index := range build.Files {
		file := &build.Files[index]
		if file.ReusedFrom == "" || file.State != "succeeded" {
			continue
		}
		if build.ModelID == "" {
			build.ModelID, build.Model = file.ModelID, file.Model
			if file.PromptVersion != nil {
				prompt := *file.PromptVersion
				build.PromptVersion = &prompt
			}
		}
		if !sameExecution(build, file) {
			resetFileExecution(file)
			requeued = true
		}
	}
	return requeued
}
