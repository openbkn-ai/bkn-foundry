// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the repository root.

package kbinstance

import (
	"context"
	"errors"
	"fmt"
	"testing"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

type previewSnapshot struct {
	rows  map[string][]map[string]any
	calls []kb.DatasetFileScope
	fail  string
}

func (s *previewSnapshot) ReadFileRows(ctx context.Context, _ interfaces.DraftDatasetSpec, scope kb.DatasetFileScope, guard func(context.Context) error) ([]map[string]any, error) {
	if err := guard(ctx); err != nil {
		return nil, err
	}
	s.calls = append(s.calls, scope)
	if scope.FileID == s.fail {
		return nil, errors.New("incomplete native snapshot")
	}
	return append([]map[string]any(nil), s.rows[scope.FileID]...), nil
}
func TestNativeDatasetPreviewKeepsFileScopeAndStablePagination(t *testing.T) {
	build, draft, _, _ := extractionFixture(t, "inspection", false)
	build.Files[0].State = "succeeded"
	second := build.Files[0]
	second.File.FileID, second.File.ContentVersionID = "second", "second-version"
	heldout := second
	heldout.File.FileID, heldout.File.SampleRole = "heldout", "acceptance"
	build.Files = append(build.Files, second, heldout)
	spec := draft.Items.DatasetPlans[0].Datasets[0]
	scope, err := previewScope(spec, build, "")
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string][]map[string]any{}
	for file, count := range map[string]int{build.Files[0].File.FileID: 60, "second": 10} {
		for i := count; i > 0; i-- {
			rows[file] = append(rows[file], map[string]any{"_id": fmt.Sprintf("%064d", i), "business_value": "unchanged"})
		}
	}
	for _, offset := range []int{0, 55} {
		snapshot := &previewSnapshot{rows: rows}
		got, err := previewDatasetRows(context.Background(), snapshot, spec, build, scope, offset, func(context.Context) error { return nil })
		want, calls := 51, 1
		if offset == 55 {
			want, calls = 15, 2
		}
		if err != nil || len(got) != want || len(snapshot.calls) != calls {
			t.Fatal(offset, err, len(got), len(snapshot.calls))
		}
		first := 1
		if offset == 55 {
			first = 56
		}
		if got[0]["_id"] != fmt.Sprintf("%064d", first) {
			t.Fatal("unstable native order", got[0])
		}
		for _, call := range snapshot.calls {
			if call.FileID == "heldout" || call.ModelFingerprint != build.ModelFingerprint || call.ContentVersionID == "" || call.DocumentID == "" {
				t.Fatal("preview lost exact file/model scope", call)
			}
		}
		got[0]["business_value"] = "display only"
		if rows[build.Files[0].File.FileID][60-first]["business_value"] != "unchanged" {
			t.Fatal("preview mutated shared Dataset rows")
		}
	}
}
func TestNativeDatasetPreviewDiscardsPartialReadsAndHonorsRevocation(t *testing.T) {
	build, draft, _, _ := extractionFixture(t, "patent", false)
	build.Files[0].State = "succeeded"
	second := build.Files[0]
	second.File.FileID = "second"
	build.Files = append(build.Files, second)
	spec := draft.Items.DatasetPlans[0].Datasets[0]
	scope, err := previewScope(spec, build, "")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := &previewSnapshot{rows: map[string][]map[string]any{build.Files[0].File.FileID: {{"_id": "first"}}}, fail: "second"}
	got, err := previewDatasetRows(context.Background(), snapshot, spec, build, scope, 0, func(context.Context) error { return nil })
	if err == nil || got != nil {
		t.Fatal("partial snapshot returned as complete", got, err)
	}
	denied := errors.New("source access revoked")
	got, err = previewDatasetRows(context.Background(), snapshot, spec, build, scope, 0, func(context.Context) error { return denied })
	if !errors.Is(err, denied) || got != nil {
		t.Fatal("source revocation ignored", got, err)
	}
}
