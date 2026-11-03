// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
package kbrefresh

import (
	"context"
	"encoding/json"
	"errors"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbconcept"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbdataset"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbpublication"
	"testing"
)

type fixedModelStorage struct {
	kb.PublicationDatasets
	row   map[string]any
	reads int
}

func (s *fixedModelStorage) ReadPublishedModel(context.Context, interfaces.DraftDatasetSpec, string) (map[string]any, error) {
	s.reads++
	return s.row, nil
}

type fixedBuildStorage struct {
	interfaces.KBBuildAccess
	value *interfaces.KBBuild
}

func (s *fixedBuildStorage) Get(context.Context, string, string) (*interfaces.KBBuildRecord, error) {
	return &interfaces.KBBuildRecord{Build: s.value}, nil
}

type preparedSource struct {
	*snapshotSource
	kb.EvidenceSource
}

func (s *preparedSource) Check(context.Context) error { return nil }

type preparedSources struct {
	source kb.PublishedSource
	guard  func(context.Context) error
}

func (s *preparedSources) OpenPublished(ctx context.Context, _ *interfaces.KBPublication, guard func(context.Context) error) (kb.PublishedSource, error) {
	s.guard = guard
	return s.source, guard(ctx)
}
func refreshPreparationFixture(t *testing.T, generations ...string) (*Service, *refreshReleases, *refreshPublications, *fixedModelStorage, *interfaces.KBBuild, *preparedSources) {
	t.Helper()
	generation := "generation"
	if len(generations) > 0 {
		generation = generations[0]
	}
	doc := &interfaces.DraftDocumentModel{Role: "entity", Layer: "entity", SelectionID: "selection", SelectionRevision: 1, Citations: []interfaces.DraftSourceCitation{{KnowledgeBaseID: "1", FileID: "first", ContentVersionID: "v1", BlockID: "block", Start: 0, End: 5, Quote: "asset"}}}
	draft := &interfaces.Draft{ProjectID: "archived-project", Status: interfaces.DRAFT_STATUS_READY, UpdateTime: 12, Items: interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{{ID: "asset_doc", Name: "Asset", Adopted: true, Document: doc, PrimaryKeys: []string{"number"}, Properties: []*interfaces.DraftProperty{{Name: "_id", Type: "string", ReadOnly: true, SourceOfRecord: "document"}, {Name: "number", Type: "string", SourceOfRecord: "document", Extraction: &interfaces.DraftExtractionField{Instruction: "Read the explicit asset number", Examples: doc.Citations}}}}}}}

	for _, field := range []struct{ name, kind string }{{"entity_id", "string"}, {"document_id", "string"}, {"content_version_id", "string"}, {"block_id", "string"}, {"source_start", "integer"}, {"source_end", "integer"}, {"source_quote", "text"}, {"extraction_version", "string"}, {"model_fingerprint", "string"}} {
		draft.Items.ObjectTypes[0].Properties = append(draft.Items.ObjectTypes[0].Properties, &interfaces.DraftProperty{Name: field.name, Type: field.kind, ReadOnly: true, SourceOfRecord: "document"})
	}
	evidence, err := kbconcept.BuildEvidence(&kbconcept.SampleInput{SelectionID: "selection", SelectionRevision: 1, Request: kb.ConceptRequest{NamingLanguage: "en-US"}}, draft.Items)
	if err != nil {
		t.Fatal(err)
	}
	for _, object := range evidence.Model.ObjectTypes {
		object.Adopted = true
		draft.Items.ObjectTypes = append(draft.Items.ObjectTypes, object)
	}
	for _, relation := range evidence.Model.RelationTypes {
		relation.Adopted = true
		draft.Items.RelationTypes = append(draft.Items.RelationTypes, relation)
	}
	plan, err := kbdataset.NewPlan(draft, generation, "catalog", interfaces.AccountInfo{ID: "owner", Type: "user"})
	if err != nil {
		t.Fatal(err)
	}
	plan.State = "ready"
	for i := range plan.Datasets {
		plan.Datasets[i].Ready = true
	}
	draft.Items.DatasetPlans = []*interfaces.DraftDatasetPlan{plan}
	draft.Items.Confirmation = &interfaces.DraftConfirmation{UpdateTime: 12, Commit: "confirmed"}
	bundle, err := kbpublication.Freeze(draft, plan)
	if err != nil {
		t.Fatal(err)
	}
	publisher := interfaces.AccountInfo{ID: "owner", Type: "user"}
	record := &interfaces.KBPublication{NetworkID: "released-network", ProjectID: draft.ProjectID, LineageID: "base", Publisher: publisher, State: "active", GenerationID: generation, FullBuildID: "full", ModelFingerprint: bundle.ModelFingerprint, RuntimeRowID: bundle.RuntimeDigest, RuntimeDigest: bundle.RuntimeDigest, Revision: 3, Ranges: []interfaces.KBSourceRange{{KnowledgeBaseID: "1", CatalogID: "catalog"}}}
	releases := &refreshReleases{sw: &interfaces.Switch{LineageID: "base", ToNetworkID: record.NetworkID}, release: &interfaces.Release{NetworkID: record.NetworkID, ProjectID: draft.ProjectID, LineageID: "base", Publisher: publisher}}
	pubs := &refreshPublications{value: record}
	storage := &fixedModelStorage{row: map[string]any{"_id": bundle.RuntimeDigest, "model_fingerprint": bundle.ModelFingerprint, "extraction_version": "published-model-v1", "runtime_model": string(bundle.Runtime)}}
	prompt := 2
	agent := 3
	previous := &interfaces.KBBuild{ID: "full", NetworkID: "base", ProjectID: draft.ProjectID, Mode: "full", State: "succeeded", Revision: 4, GenerationID: generation, ModelFingerprint: bundle.ModelFingerprint, AgentVersion: agent, PromptVersion: &prompt, ModelID: "fixed-model-id", Model: "fixed-model"}
	for _, id := range []string{"first", "second"} {
		previous.Files = append(previous.Files, interfaces.KBFileBuild{File: interfaces.KBResourceFile{KnowledgeBaseID: "1", FileID: id, ContentVersionID: "v1"}, State: "succeeded", AgentTaskID: "actual-task-" + id, AgentVersion: &agent, RowsDigest: "actual-ack-" + id, RowsWritten: 2})
	}
	sources := &preparedSources{source: &preparedSource{snapshotSource: &snapshotSource{}}}
	service := &Service{Releases: releases, Publications: pubs, Builds: &fixedBuildStorage{value: previous}, Storage: storage, Sources: sources}
	return service, releases, pubs, storage, previous, sources
}
func TestRefreshPreparationUsesPrivatePublishedModelAndActualUnchangedReceipts(t *testing.T) {
	ctx := context.Background()
	service, releases, pubs, storage, previous, sources := refreshPreparationFixture(t)
	record := pubs.value
	prepared, err := service.Prepare(ctx, "base", "refresh-one", nil)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Plan.Reused != 2 || len(prepared.Plan.Changed) != 0 || len(prepared.Plan.Removed) != 0 || storage.reads != 1 || prepared.Plan.Build.NetworkID != record.LineageID || prepared.Plan.Build.Assessment != nil {
		t.Fatal("unchanged files were re-extracted or fixed model lost")
	}
	if prepared.Plan.Build.Files[0].AgentTaskID != "actual-task-first" || previous.ID != "full" {
		t.Fatal("actual task proof rewritten")
	}
	// An unchanged scan journals its watermark without inference or Dataset writes.
	journal := &memoryRefreshJournal{}
	service.Journal = journal
	queued, err := service.Begin(ctx, "base", "refresh-two", "worker")
	if err != nil || queued.State != "idle" || queued.Pending != nil || queued.Previous == nil || journal.saves != 1 || queued.Cursors["1"] != "before" {
		t.Fatal("unchanged scan did not durably advance its manifest", err)
	}
	// A changed content version creates a durable intent before any external action.
	journal.value.NextCheck = 0 // Make the next scheduled source check due.
	service.Sources = &preparedSources{source: &preparedSource{snapshotSource: &snapshotSource{scenario: "empty"}}}
	queued = beginReviewed(t, service, ctx, "base", "refresh-three", "worker")
	err = nil
	if err != nil || queued.State != "running" || queued.Pending == nil || len(queued.Pending.Removed) != 2 || queued.Pending.Build.ID != "refresh-three" {
		t.Fatal("removed-file batch was not journaled", err)
	}
	// Resuming an existing intent must not create another batch or scan again.
	queued, err = service.Begin(ctx, "base", "must-not-be-used", "worker")
	if err != nil || queued.Pending.Build.ID != "refresh-three" || journal.saves != 3 {
		t.Fatal("resume replaced existing intent", err)
	}
	// Archival never enters the read boundary. A consumer switch, however, fences
	// the prepared plan before any inference or write can proceed.
	releases.sw = &interfaces.Switch{LineageID: "base", ToNetworkID: "different-network"}
	if sources.guard(ctx) == nil || prepared.Guard(ctx) == nil {
		t.Fatal("consumer switch failed to invalidate prepared refresh")
	}
}

type memoryRefreshJournal struct {
	interfaces.KBRefreshAccess
	value               *interfaces.KBRefreshRecord
	saves               int
	loseAcknowledgement bool
}

func (j *memoryRefreshJournal) Ensure(_ context.Context, lineage string, now int64) error {
	if j.value == nil {
		j.value = &interfaces.KBRefreshRecord{LineageID: lineage, State: "idle", Revision: 1, UpdateTime: now}
	}
	return nil
}
func (j *memoryRefreshJournal) Get(context.Context, string) (*interfaces.KBRefreshRecord, error) {
	raw, _ := json.Marshal(j.value)
	var copy interfaces.KBRefreshRecord
	_ = json.Unmarshal(raw, &copy)
	copy.LeaseID, copy.LeaseUntil = j.value.LeaseID, j.value.LeaseUntil
	return &copy, nil
}
func (j *memoryRefreshJournal) Claim(ctx context.Context, lineage, lease string, now, until int64) (*interfaces.KBRefreshRecord, error) {
	if j.value.State == "attention" || j.value.NextCheck > now || (j.value.LeaseID != "" && j.value.LeaseID != lease && j.value.LeaseUntil > now) {
		return nil, nil
	}
	j.value.LeaseID, j.value.LeaseUntil = lease, until
	return j.Get(ctx, lineage)
}
func (j *memoryRefreshJournal) Save(_ context.Context, r *interfaces.KBRefreshRecord, revision int64, lease string, now, until int64) (bool, error) {
	if j.value.Revision != revision || j.value.LeaseID != lease {
		return false, nil
	}
	raw, _ := json.Marshal(r)
	var copy interfaces.KBRefreshRecord
	_ = json.Unmarshal(raw, &copy)
	copy.LeaseID, copy.LeaseUntil = lease, until
	j.value = &copy
	j.saves++
	if j.loseAcknowledgement {
		return false, errors.New("save acknowledgement lost")
	}
	return true, nil
}

func (j *memoryRefreshJournal) ClaimReview(ctx context.Context, lineage, lease string, revision, now, until int64) (*interfaces.KBRefreshRecord, error) {
	if j.value.State != "attention" || j.value.Revision != revision {
		return nil, nil
	}
	j.value.LeaseID, j.value.LeaseUntil = lease, until
	return j.Get(ctx, lineage)
}
func beginReviewed(t *testing.T, service *Service, ctx context.Context, lineage, id, lease string) *interfaces.KBRefreshRecord {
	t.Helper()
	r, err := service.Begin(ctx, lineage, id, lease)
	if err != nil || r == nil {
		t.Fatal("cannot prepare refresh", err)
	}
	if r.State == "attention" && r.Reason == "batch_review_required" {
		// This test explicitly exercises the owner's exceptional-batch decision;
		// it does not disable or silently bypass production's confirmed defaults.
		if err := service.ReleaseBatch(ctx, lineage, lease, r.Revision, interfaces.AccountInfo{ID: "owner", Type: "user"}); err != nil {
			t.Fatal(err)
		}
		r, err = service.Journal.Get(ctx, lineage)
		if err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func TestRefreshBaselineCannotOverrideNewExtractionsPublishedConfiguration(t *testing.T) {
	for _, field := range []string{"agent", "prompt", "model_id", "model"} {
		t.Run(field, func(t *testing.T) {
			service, _, _, _, original, _ := refreshPreparationFixture(t)
			raw, _ := json.Marshal(original)
			var previous interfaces.KBBuild
			if err := json.Unmarshal(raw, &previous); err != nil {
				t.Fatal(err)
			}
			previous.ID = "previous-refresh"
			switch field {
			case "agent":
				previous.AgentVersion++
			case "prompt":
				value := *previous.PromptVersion + 1
				previous.PromptVersion = &value
			case "model_id":
				previous.ModelID = "another-model-id"
			case "model":
				previous.Model = "another-model"
			}
			prepared, err := service.Prepare(context.Background(), "base", "refresh", &previous)
			if err != nil {
				t.Fatal(err)
			}
			build := prepared.Plan.Build
			if build.AgentVersion != original.AgentVersion || *build.PromptVersion != *original.PromptVersion || build.ModelID != original.ModelID || build.Model != original.Model || prepared.Plan.Reused != 2 {
				t.Fatal("baseline overrode new extraction defaults or discarded actual unchanged receipts")
			}
			if original.ID != "full" || original.AgentVersion != 3 || *original.PromptVersion != 2 {
				t.Fatal("publication proof mutated")
			}
		})
	}
}

func TestConsumerRollbackRestoresActualGenerationManifestAndHoldOwner(t *testing.T) {
	ctx := context.Background()
	service, releases, pubs, storage, original, source := refreshPreparationFixture(t)
	_, otherReleases, otherPubs, otherStorage, otherOriginal, _ := refreshPreparationFixture(t, "other-generation")
	otherPubs.value.NetworkID = "other-network"
	otherReleases.release.NetworkID = "other-network"
	firstPublication, firstRelease, firstRow := pubs.value, releases.release, storage.row
	journal := &memoryRefreshJournal{}
	service.Journal = journal
	if _, err := service.Begin(ctx, "base", "first-check", "worker"); err != nil {
		t.Fatal(err)
	}
	journal.value.Previous.ID = "actual-first-refresh"
	journal.value.Previous.Files[0].AgentTaskID = "actual-refreshed-task"
	journal.value.Holds = []interfaces.KBHoldGroup{{ID: "first-hold", KnowledgeBaseID: "1"}}
	journal.value.HoldNetworkID = firstPublication.NetworkID
	selectTarget := func(publication *interfaces.KBPublication, release *interfaces.Release, row map[string]any, build *interfaces.KBBuild) {
		pubs.value, releases.release, storage.row = publication, release, row
		releases.sw.ToNetworkID = publication.NetworkID
		source.source.(*preparedSource).network = publication.NetworkID
		service.Builds = &fixedBuildStorage{value: build}
		journal.value.NextCheck = 0
	}
	selectTarget(otherPubs.value, otherReleases.release, otherStorage.row, otherOriginal)
	next, err := service.Begin(ctx, "base", "other-check", "worker")
	if err != nil {
		t.Fatal(err)
	}
	if next.Previous.GenerationID != "other-generation" || len(next.Holds) != 0 || next.Generations["generation"].Holds[0].ID != "first-hold" {
		t.Fatal("switch reused or dropped another generation's evidence")
	}
	journal.value.Holds = []interfaces.KBHoldGroup{{ID: "other-hold", KnowledgeBaseID: "1"}}
	journal.value.HoldNetworkID = "other-network"
	selectTarget(firstPublication, firstRelease, firstRow, original)
	restored, err := service.Begin(ctx, "base", "rollback-check", "worker")
	if err != nil {
		t.Fatal(err)
	}
	if restored.Previous.ID != "actual-first-refresh" || restored.Previous.Files[0].AgentTaskID != "actual-refreshed-task" || restored.HoldNetworkID != firstPublication.NetworkID || restored.Holds[0].ID != "first-hold" || restored.Generations["other-generation"].Holds[0].ID != "other-hold" {
		t.Fatal("rollback lost actual refreshed receipts or retained-source ownership")
	}
}
