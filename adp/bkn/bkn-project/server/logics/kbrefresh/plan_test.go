// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbrefresh

import (
	"encoding/json"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func TestRefreshPlanReusesActualUnchangedReceiptsAcrossDomains(t *testing.T) {
	for _, domain := range []string{"supply", "patent", "equipment"} {
		t.Run(domain, func(t *testing.T) {
			version := 3
			oldFile := interfaces.KBResourceFile{KnowledgeBaseID: "1", FileID: domain, ContentVersionID: "v1", Name: "previous name", IncludeOriginal: false}
			previous := &interfaces.KBBuild{ID: "full", ProjectID: "project", NetworkID: "lineage", Owner: interfaces.AccountInfo{ID: "owner", Type: "user"}, Mode: "full", State: "succeeded", ModelFingerprint: "model", GenerationID: "generation", AgentVersion: version, ModelID: "model-id", Model: "model-name", PromptVersion: &version, Files: []interfaces.KBFileBuild{{File: oldFile, State: "succeeded", AgentTaskID: "acknowledged-task", AgentVersion: &version, PromptVersion: &version, ModelID: "model-id", InputDigest: "input-proof", RowsDigest: "rows-proof", RowsWritten: 7}}}
			encoded, _ := json.Marshal(previous)
			current := oldFile
			current.Name = "renamed file"
			plan, err := Plan("refresh", previous, []interfaces.KBResourceFile{current})
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Changed) != 0 || len(plan.Removed) != 0 || plan.Reused != 1 || len(plan.Build.Files) != 1 || plan.Build.Files[0].AgentTaskID != "acknowledged-task" || plan.Build.Files[0].RowsDigest != "rows-proof" {
				t.Fatal("unchanged content was scheduled again or lost its actual execution proof")
			}
			after, _ := json.Marshal(previous)
			if string(encoded) != string(after) {
				t.Fatal("planning mutated the prior release receipt")
			}
			plan.Build.Files[0].AgentTaskID = "mutated"
			if previous.Files[0].AgentTaskID != "acknowledged-task" {
				t.Fatal("plan aliases the acknowledged manifest")
			}
		})
	}
}

func TestRefreshPlanSeparatesReplacementRemovalAndOriginalPolicyChanges(t *testing.T) {
	old := &interfaces.KBBuild{ID: "full", Mode: "full", State: "succeeded", Files: []interfaces.KBFileBuild{
		{File: interfaces.KBResourceFile{KnowledgeBaseID: "1", FileID: "changed", ContentVersionID: "v1"}, State: "succeeded"},
		{File: interfaces.KBResourceFile{KnowledgeBaseID: "1", FileID: "removed", ContentVersionID: "v1"}, State: "succeeded"},
		{File: interfaces.KBResourceFile{KnowledgeBaseID: "1", FileID: "originals", ContentVersionID: "v1"}, State: "succeeded"},
	}}
	current := []interfaces.KBResourceFile{
		{KnowledgeBaseID: "1", FileID: "new", ContentVersionID: "v1"},
		{KnowledgeBaseID: "1", FileID: "changed", ContentVersionID: "v2"},
		{KnowledgeBaseID: "1", FileID: "originals", ContentVersionID: "v1", IncludeOriginal: true},
	}
	plan, err := Plan("refresh", old, current)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changed) != 3 || len(plan.Removed) != 2 || plan.Reused != 0 {
		t.Fatal("replacement/removal scopes were not retained", plan)
	}
	for _, index := range plan.Changed {
		if plan.Build.Files[index].State != "pending" || plan.Build.Files[index].AgentTaskID != "" {
			t.Fatal("changed file inherited an old task")
		}
	}
	empty, err := Plan("empty", old, nil)
	if err != nil || len(empty.Removed) != 3 || len(empty.Changed) != 0 || len(empty.Build.Files) != 0 {
		t.Fatal("complete empty source did not retire old file scopes", err)
	}
}

func TestRefreshPlanRefusesAmbiguousOrUnacknowledgedManifests(t *testing.T) {
	good := interfaces.KBResourceFile{KnowledgeBaseID: "1", FileID: "file", ContentVersionID: "v1"}
	old := &interfaces.KBBuild{ID: "full", Mode: "full", State: "succeeded", Files: []interfaces.KBFileBuild{{File: good, State: "succeeded"}}}
	for _, current := range [][]interfaces.KBResourceFile{{good, good}, {{KnowledgeBaseID: "1", FileID: "file"}}} {
		if _, err := Plan("refresh", old, current); err == nil {
			t.Fatal("invalid current manifest accepted")
		}
	}
	old.Files[0].State = "uncertain"
	if _, err := Plan("refresh", old, []interfaces.KBResourceFile{good}); err == nil {
		t.Fatal("uncertain prior writes reused")
	}
}
