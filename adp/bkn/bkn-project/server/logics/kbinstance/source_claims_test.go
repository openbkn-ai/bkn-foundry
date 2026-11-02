// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

type claimSnapshot struct {
	rows map[string][]map[string]any
	err  error
}

func (s *claimSnapshot) ReadFileRows(ctx context.Context, spec interfaces.DraftDatasetSpec, _ kb.DatasetFileScope, guard func(context.Context) error) ([]map[string]any, error) {
	if err := guard(ctx); err != nil {
		return nil, err
	}
	return s.rows[spec.ResourceID], s.err
}

func TestReadSourceClaimsUsesCurrentBuildEvidenceWithoutAnotherInference(t *testing.T) {
	for _, scenario := range []string{"valid", "replaced_task", "foreign_version", "unexpected_property", "original_disabled", "partial_read", "revoked"} {
		t.Run(scenario, func(t *testing.T) {
			build, draft, input, task := extractionFixture(t, "contract", false)
			writes, err := MaterializeFile(build, 0, draft, input, task)
			if err != nil {
				t.Fatal(err)
			}
			build.ModelID, build.Model = task.ModelID, task.Model
			step := &build.Files[0]
			step.State, step.AgentVersion, step.PromptVersion, step.ModelID = "succeeded", task.AgentVersion, task.PromptVersion, task.ModelID
			source := &claimSnapshot{rows: map[string][]map[string]any{}}
			for _, write := range writes {
				source.rows[write.Spec.ResourceID] = append(source.rows[write.Spec.ResourceID], write.Row)
			}
			for _, rows := range source.rows {
				for _, row := range rows {
					if row["property_name"] != "amount" {
						continue
					}
					switch scenario {
					case "replaced_task":
						row["extraction_version"] = `{"task_id":"newer"}`
					case "foreign_version":
						row["content_version_id"] = "other"
					case "unexpected_property":
						row["property_name"] = "invented"
					case "original_disabled":
						row["source_quote"] = "25.50"
					}
				}
			}
			stop := errors.New("source permission revoked")
			if scenario == "partial_read" {
				source.err = errors.New("incomplete cursor")
			}
			calls := 0
			guard := func(context.Context) error {
				calls++
				if scenario == "revoked" && calls > 1 {
					return stop
				}
				return nil
			}
			claims, err := ReadSourceClaims(context.Background(), source, build, draft, guard)
			if scenario != "valid" {
				if err == nil || claims != nil {
					t.Fatal("unverified source claims escaped", scenario, err)
				}
				return
			}
			if err != nil || len(claims) != 4 {
				t.Fatal("complete business claims lost", len(claims), err)
			}
			for _, claim := range claims {
				if claim.RecordedAt != task.CreateTime || claim.DocumentID == "" || len(claim.Evidence) != 1 || claim.Evidence[0].Quote != "" {
					t.Fatal("evidence or actual receipt lost", claim)
				}
				if claim.Property == "amount" && claim.Value != json.Number("25.50") {
					t.Fatal("decimal rounded", claim.Value)
				}
			}
		})
	}
}
