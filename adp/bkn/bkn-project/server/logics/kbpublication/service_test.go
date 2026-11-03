// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbpublication

import (
	"context"
	"errors"
	"testing"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

type publicationGit struct {
	interfaces.GitStore
	rules []byte
}

func (g *publicationGit) ReadModelRules(context.Context, string, string) ([]byte, error) {
	return g.rules, nil
}

func (g *publicationGit) CommitModel(context.Context, string, string, map[string][]byte, []byte, string, interfaces.GitAuthor) (string, error) {
	panic("inspection must not commit")
}

type publicationProject struct {
	interfaces.ProjectAccess
	value *interfaces.Project
}

func (p *publicationProject) GetProject(context.Context, string) (*interfaces.Project, bool, error) {
	return p.value, p.value != nil, nil
}

type publicationDraft struct {
	interfaces.DraftAccess
	value *interfaces.Draft
}

func (d *publicationDraft) GetDraft(context.Context, string) (*interfaces.Draft, bool, error) {
	return d.value, d.value != nil, nil
}

type publicationSelection struct {
	interfaces.KBResourceAccess
	value *interfaces.KBSelection
}

func (s *publicationSelection) Read(context.Context, string) (*interfaces.KBResourceState, error) {
	return &interfaces.KBResourceState{Current: s.value}, nil
}

type publicationSource struct {
	kb.UnavailableSource
	reads int
}

func (s *publicationSource) SummarizeFiles(ctx context.Context, scope kb.SourceScope, _ kb.FileQuery) (kb.FileSummary, error) {
	s.reads++
	if kb.OperatorAuthorization(ctx) != "Bearer publisher" || scope.NetworkID != "" || scope.KnowledgeBaseID != "1" {
		return kb.FileSummary{}, errors.New("publisher identity not forwarded")
	}
	return kb.FileSummary{}, &kb.SourceError{StatusCode: 403}
}
func TestPublicationChecksCurrentPublisherBeforeStorageOrNetworkWrites(t *testing.T) {
	draft := frozenFixture(t)
	owner := draft.Items.DatasetPlans[0].Owner
	rules, err := Definition(draft.Items)
	if err != nil {
		t.Fatal(err)
	}
	source := &publicationSource{}
	service := &Service{Git: &publicationGit{rules: rules}, Projects: &publicationProject{value: &interfaces.Project{ID: "p", KNID: "lineage", Branch: "project/p", Owner: owner}}, Drafts: &publicationDraft{value: draft}, Selections: &publicationSelection{value: &interfaces.KBSelection{ID: "private-selection", ProjectID: "p", Owner: owner, State: "active", Revision: 1, Ranges: []interfaces.KBSourceRange{{KnowledgeBaseID: "1"}}}}, Source: source}
	target := Target{ProjectID: "p", LineageID: "lineage", Branch: "project/p", BranchCommit: "head", NetworkID: "lineage_1_0_0", Publisher: owner}
	if _, err := service.Inspect(context.Background(), "Bearer publisher", target); err == nil {
		t.Fatal("publisher without query permission passed")
	} else {
		var sourceErr *kb.SourceError
		if !errors.As(err, &sourceErr) || sourceErr.StatusCode != 403 {
			t.Fatal("permission failure was masked", err)
		}
	}
	if source.reads != 1 {
		t.Fatal("source permission was not checked")
	}
}
func TestTableOnlyPublicationDoesNotRequireKnowledgeBaseProviders(t *testing.T) {
	service := &Service{Git: &publicationGit{}}
	plan, err := service.Inspect(context.Background(), "Bearer publisher", Target{ProjectID: "p", LineageID: "lineage", Branch: "project/p", BranchCommit: "head"})
	if err != nil || plan != nil {
		t.Fatal("table-only release required knowledge-base infrastructure", err)
	}
}

func TestPublicationReviewSignatureTracksAcknowledgedExecution(t *testing.T) {
	if ReviewSignature("base", nil) != "base" {
		t.Fatal("table-only signature changed")
	}
	build := &interfaces.KBBuild{ID: "full", Revision: 1, GenerationID: "generation", ModelFingerprint: "model", ReferenceID: "reference", ReferenceRevision: 1, Linking: &interfaces.KBLinkingReceipt{Signature: "linked"}}
	plan := &Plan{Full: &interfaces.KBBuildRecord{Build: build}}
	original := ReviewSignature("base", plan)
	changes := []func(){
		func() { build.Revision++ },
		func() { build.GenerationID = "other-generation" },
		func() { build.ReferenceRevision++ },
		func() { build.Linking.Signature = "other-linking" },
	}
	for _, change := range changes {
		before := ReviewSignature("base", plan)
		change()
		if ReviewSignature("base", plan) == before {
			t.Fatal("changed execution retained review signature")
		}
	}
	if original == "base" || ReviewSignature("other-base", plan) == ReviewSignature("base", plan) {
		t.Fatal("base review was not bound")
	}
}
