// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbpublication

import (
	"context"
	"errors"
	"strings"
	"testing"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbconcept"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbdataset"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbinstance"
)

type coordinatorJournal struct {
	interfaces.KBPublicationAccess
	record   *interfaces.KBPublication
	prepares int
}

func (j *coordinatorJournal) Get(context.Context, string) (*interfaces.KBPublication, error) {
	return j.record, nil
}
func (j *coordinatorJournal) Prepare(_ context.Context, record *interfaces.KBPublication) (bool, error) {
	j.prepares++
	if j.record != nil {
		return false, errors.New("duplicate prepare")
	}
	j.record = record
	return true, nil
}
func (j *coordinatorJournal) Seal(context.Context, string, string, int64) (bool, error) {
	j.record.State = "sealed"
	j.record.Snapshot.Ready = true
	return true, nil
}
func (j *coordinatorJournal) MarkMounted(context.Context, string, string, int64) (bool, error) {
	j.record.State = "mounted"
	return true, nil
}
func (j *coordinatorJournal) Activate(context.Context, string, string, int64) (bool, error) {
	j.record.State = "active"
	return true, nil
}

type coordinatorSource struct {
	kb.UnavailableSource
	journal *coordinatorJournal
	holds   map[string]bool
	denied  bool
}

func (s *coordinatorSource) SummarizeFiles(ctx context.Context, scope kb.SourceScope, _ kb.FileQuery) (kb.FileSummary, error) {
	if s.denied {
		return kb.FileSummary{}, &kb.SourceError{StatusCode: 403}
	}
	if kb.OperatorAuthorization(ctx) != "Bearer publisher" || scope.NetworkID != "" || scope.KnowledgeBaseID != "1" {
		return kb.FileSummary{}, errors.New("incorrect operator scope")
	}
	return kb.FileSummary{}, nil
}
func (s *coordinatorSource) Hold(ctx context.Context, scope kb.SourceScope, request kb.HoldRequest) error {
	if s.journal.record == nil || len(s.journal.record.Holds) != 1 || s.journal.record.Holds[0].ID != request.ID {
		return errors.New("hold preceded durable intent")
	}
	if _, err := s.SummarizeFiles(ctx, scope, kb.FileQuery{}); err != nil {
		return err
	}
	if len(request.Versions) != 2 {
		return errors.New("incomplete version holds")
	}
	s.holds[request.ID] = true
	return nil
}

type coordinatorStorage struct {
	modelStorage
	kb.DatasetFileSnapshot
}

func (*coordinatorStorage) Prune(context.Context, interfaces.DraftDatasetSpec, kb.DatasetFileScope, []string, func(context.Context) error) error {
	return errors.New("publication must not prune")
}

// Evidence-only native templates have no business claims to read. An unexpected
// read is a test failure rather than an invented extracted result.
func (*coordinatorStorage) ReadFileRows(context.Context, interfaces.DraftDatasetSpec, kb.DatasetFileScope, func(context.Context) error) ([]map[string]any, error) {
	return nil, errors.New("unexpected claim read")
}

type coordinatorBuilds struct {
	interfaces.KBBuildQueue
	full       *interfaces.KBBuildRecord
	acceptance *interfaces.KBBuild
}

func (b *coordinatorBuilds) List(context.Context, string, int) ([]*interfaces.KBBuild, error) {
	return []*interfaces.KBBuild{b.full.Build, b.acceptance}, nil
}
func (b *coordinatorBuilds) Get(context.Context, string, string) (*interfaces.KBBuildRecord, error) {
	return b.full, nil
}

type coordinatorReferences struct {
	interfaces.KBReferenceAccess
	reference *interfaces.KBReference
}

func (r *coordinatorReferences) Get(_ context.Context, _, id string) (*interfaces.KBReference, error) {
	r.reference.ID = id
	return r.reference, nil
}

type coordinatorGit struct{ publicationGit }

func (*coordinatorGit) ReadTree(context.Context, string, string) (map[string][]byte, error) {
	return map[string][]byte{"object_types/source_document.bkn": []byte("confirmed BKN")}, nil
}

type coordinatorNetwork struct{ mounts int }

func (n *coordinatorNetwork) MountKnowledgeBases(_ context.Context, token, network string, ids []string) error {
	if token != "Bearer publisher" || network != "lineage_1.0.0" || len(ids) != 1 || ids[0] != "1" {
		return errors.New("incorrect mount target")
	}
	n.mounts++
	return nil
}

func coordinatorFixture(t *testing.T) (*Service, Target, *coordinatorSource, *coordinatorStorage, *coordinatorJournal, *coordinatorNetwork) {
	t.Helper()
	owner := interfaces.AccountInfo{ID: "owner", Type: "user"}
	templates, err := kbconcept.BuildEvidence(&kbconcept.SampleInput{SelectionID: "selection", SelectionRevision: 1}, interfaces.DraftItems{})
	if err != nil {
		t.Fatal(err)
	}
	draft := &interfaces.Draft{ProjectID: "p", Status: interfaces.DRAFT_STATUS_READY, UpdateTime: 12, Items: interfaces.DraftItems{ObjectTypes: templates.Model.ObjectTypes, RelationTypes: templates.Model.RelationTypes}}
	for _, object := range draft.Items.ObjectTypes {
		object.Adopted = true
	}
	for _, relation := range draft.Items.RelationTypes {
		relation.Adopted = true
	}
	plan, err := kbdataset.NewPlan(draft, "generation", "catalog", owner)
	if err != nil {
		t.Fatal(err)
	}
	plan.State = "ready"
	for i := range plan.Datasets {
		plan.Datasets[i].Ready = true
	}
	draft.Items.DatasetPlans = []*interfaces.DraftDatasetPlan{plan}
	draft, err = kbdataset.BindPreview(draft, plan)
	if err != nil {
		t.Fatal(err)
	}
	draft.Items.Confirmation = &interfaces.DraftConfirmation{UpdateTime: 12, Commit: "confirmed", Files: []string{"object_types/source_document.bkn"}}
	selection := &interfaces.KBSelection{ID: "selection", ProjectID: "p", State: "active", Revision: 1, Owner: owner, Ranges: []interfaces.KBSourceRange{{KnowledgeBaseID: "1"}}, Files: []interfaces.KBResourceFile{{KnowledgeBaseID: "1", FileID: "debug", ContentVersionID: "v-debug", SampleRole: "debug"}, {KnowledgeBaseID: "1", FileID: "acceptance", ContentVersionID: "v-acceptance", SampleRole: "acceptance"}}}
	full, err := kbinstance.PlanFileBuild("full", "lineage", "full", 3, draft, selection)
	if err != nil {
		t.Fatal(err)
	}
	full.State = "succeeded"
	prompt := 2
	full.PromptVersion, full.ModelID, full.Model = &prompt, "synthetic-model", "synthetic-model"
	for i := range full.Files {
		full.Files[i].State = "succeeded"
	}
	references := &coordinatorReferences{reference: &interfaces.KBReference{ProjectID: "p", Owner: owner, ModelFingerprint: full.ModelFingerprint, SelectionID: full.SelectionID, SelectionRevision: full.SelectionRevision, GenerationID: full.GenerationID, Revision: 1, Files: []interfaces.KBReferenceFile{{File: selection.Files[1], RowID: "reviewed-row", InputDigest: strings.Repeat("a", 64), ReviewHash: strings.Repeat("b", 64), Reviewer: owner, ReviewedAt: 1, ReviewRevision: 1}}}}
	if err := (&kbinstance.ReferenceService{Store: references}).Ready(context.Background(), full); err != nil {
		t.Fatal(err)
	}
	// These synthetic receipts exercise publication's existing assessment gate;
	// they do not score real PDFs or replace the independent assessment tests.
	acceptance := *full
	acceptance.ID, acceptance.Mode = "acceptance", "acceptance"
	acceptance.Files = []interfaces.KBFileBuild{{File: selection.Files[1], State: "succeeded", AgentTaskID: "synthetic-task", AgentVersion: &full.AgentVersion, PromptVersion: &prompt, ModelID: full.ModelID, InputDigest: strings.Repeat("c", 64), RowsDigest: strings.Repeat("d", 64), Assessment: &interfaces.KBAcceptanceCounts{Expected: 1, Predicted: 1, Correct: 1, ExpectedLinks: 1, PredictedLinks: 1, CorrectLinks: 1}}}
	builds := &coordinatorBuilds{full: &interfaces.KBBuildRecord{Build: full, Identity: interfaces.KBBuildIdentity{Cleaned: true}}, acceptance: &acceptance}
	rules, err := Definition(draft.Items)
	if err != nil {
		t.Fatal(err)
	}
	journal := &coordinatorJournal{}
	source := &coordinatorSource{journal: journal, holds: map[string]bool{}}
	storage := &coordinatorStorage{}
	network := &coordinatorNetwork{}
	service := &Service{Git: &coordinatorGit{publicationGit{rules: rules}}, Projects: &publicationProject{value: &interfaces.Project{ID: "p", KNID: "lineage", Branch: "project/p", Owner: owner}}, Drafts: &publicationDraft{value: draft}, Selections: &publicationSelection{value: selection}, Builds: builds, References: references, Store: journal, Source: source, Storage: storage, Vega: &struct{ interfaces.VegaAccess }{}, Network: network}
	target := Target{ProjectID: "p", LineageID: "lineage", Branch: "project/p", BranchCommit: "head", NetworkID: "lineage_1.0.0", Publisher: owner}
	return service, target, source, storage, journal, network
}

func TestPublicationCoordinatorRetainsIdenticalIntentAndRechecksMountPermission(t *testing.T) {
	service, target, source, storage, journal, network := coordinatorFixture(t)
	ctx := context.Background()
	storage.uncertain = true
	if _, err := service.Prepare(ctx, "Bearer publisher", target); err == nil {
		t.Fatal("uncertain snapshot reported as sealed")
	}
	if journal.record == nil || journal.record.State != "preparing" || storage.writes != 1 || len(source.holds) != 1 {
		t.Fatal("resumable intent was not retained")
	}
	storage.uncertain = false
	record, err := service.Prepare(ctx, "Bearer publisher", target)
	if err != nil {
		t.Fatal("identical retry failed", err)
	}
	if record.State != "sealed" || journal.prepares != 1 || storage.writes != 1 || len(source.holds) != 1 {
		t.Fatal("retry rewrote snapshot or created different holds")
	}
	source.denied = true
	if err := service.Mount(ctx, "Bearer publisher", record); err == nil || network.mounts != 0 || record.State != "sealed" {
		t.Fatal("revoked publisher mounted a network")
	}
	source.denied = false
	if err := service.Mount(ctx, "Bearer publisher", record); err != nil {
		t.Fatal(err)
	}
	if err := service.Activate(ctx, record); err != nil {
		t.Fatal(err)
	}
	if record.State != "active" || network.mounts != 1 {
		t.Fatal("complete publication was not activated")
	}
}
