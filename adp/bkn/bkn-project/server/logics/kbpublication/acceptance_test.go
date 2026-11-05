// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbpublication

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func acceptedFile(id string) interfaces.KBResourceFile {
	return interfaces.KBResourceFile{KnowledgeBaseID: "kb", FileID: id, ContentVersionID: id + "-v1"}
}

func TestReleaseRefusesAFileLeftOutWithoutARecordedDecision(t *testing.T) {
	excluded := interfaces.KBFileBuild{File: acceptedFile("bad"), State: interfaces.KBFileExcluded}
	skipped := interfaces.KBFileBuild{File: interfaces.KBResourceFile{KnowledgeBaseID: "kb", FileID: "unversioned"}, State: "failed"}
	good := interfaces.KBFileBuild{File: acceptedFile("good"), State: "succeeded"}
	listed := func(files ...interfaces.KBResourceFile) *interfaces.KBFileAcceptance {
		result := &interfaces.KBFileAcceptance{}
		for _, file := range files {
			result.Files = append(result.Files, interfaces.KBAcceptedFile{File: file})
		}
		return result
	}
	for _, tc := range []struct {
		name  string
		build *interfaces.KBBuild
		ok    bool
	}{
		{"nothing left out", &interfaces.KBBuild{Files: []interfaces.KBFileBuild{good}}, true},
		{"all listed", &interfaces.KBBuild{Files: []interfaces.KBFileBuild{good, excluded}, Skipped: []interfaces.KBFileBuild{skipped}, Acceptance: listed(excluded.File, skipped.File)}, true},
		{"excluded without decision", &interfaces.KBBuild{Files: []interfaces.KBFileBuild{good, excluded}}, false},
		{"skipped without decision", &interfaces.KBBuild{Files: []interfaces.KBFileBuild{good}, Skipped: []interfaces.KBFileBuild{skipped}}, false},
		{"another file listed", &interfaces.KBBuild{Files: []interfaces.KBFileBuild{good, excluded}, Acceptance: listed(good.File)}, false},
		{"list longer than what is left out", &interfaces.KBBuild{Files: []interfaces.KBFileBuild{good, excluded}, Acceptance: listed(excluded.File, good.File)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := verifyFileAcceptance(tc.build)
			if tc.ok != (err == nil) || (err != nil && !errors.Is(err, ErrInvalidSnapshot)) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestReleaseKeepsOnlyTheVersionsItPublishes(t *testing.T) {
	build := &interfaces.KBBuild{Files: []interfaces.KBFileBuild{{File: acceptedFile("good"), State: "succeeded"}, {File: acceptedFile("scan"), State: "evidence_only"}, {File: acceptedFile("bad"), State: interfaces.KBFileExcluded}}}
	files := releasedFiles(build)
	if len(files) != 2 || files[0].FileID != "good" || files[1].FileID != "scan" {
		t.Fatalf("%+v", files)
	}
	holds := publicationHolds(files)
	if len(holds) != 1 || len(holds[0].VersionIDs) != 2 {
		t.Fatalf("%+v", holds)
	}
}

func leaveOutFirstFile(t *testing.T, service *Service) (*interfaces.KBBuild, int) {
	t.Helper()
	full := service.Builds.(*coordinatorBuilds).full.Build
	for i := range full.Files {
		if full.Files[i].File.SampleRole != "acceptance" {
			full.Files[i].State, full.Files[i].Reason = interfaces.KBFileExcluded, "source_requires_review"
			return full, i
		}
	}
	t.Fatal("fixture has no ordinary file")
	return nil, 0
}

func TestInspectRefusesFileLeftOutWithoutAPerson(t *testing.T) {
	service, target, _, _, _, _ := coordinatorFixture(t)
	leaveOutFirstFile(t, service)
	if _, err := service.Inspect(context.Background(), "Bearer publisher", target); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("err = %v", err)
	}
}

func TestReleaseRecordKeepsTheAcceptedListAndHoldsOnlyWhatIsPublished(t *testing.T) {
	service, target, source, _, _, _ := coordinatorFixture(t)
	source.versions = 1
	full, index := leaveOutFirstFile(t, service)
	left := full.Files[index].File
	full.Acceptance = &interfaces.KBFileAcceptance{By: target.Publisher, Time: 5, Files: []interfaces.KBAcceptedFile{{File: left, Reason: "source_requires_review"}}}
	record, err := service.Prepare(context.Background(), "Bearer publisher", target)
	if err != nil {
		t.Fatal(err)
	}
	if record.Acceptance == nil || len(record.Acceptance.Files) != 1 || record.Acceptance.Files[0].File.FileID != left.FileID || record.Acceptance.By != target.Publisher {
		t.Fatalf("%+v", record.Acceptance)
	}
	for _, hold := range record.Holds {
		if slices.Contains(hold.VersionIDs, left.ContentVersionID) {
			t.Fatal("a file left out must not be held for the release")
		}
	}
}
