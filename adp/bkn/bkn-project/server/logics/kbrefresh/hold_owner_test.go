// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
package kbrefresh

import (
	"context"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"testing"
)

type generationPublications struct {
	interfaces.KBPublicationAccess
	values map[string]*interfaces.KBPublication
}

func (p *generationPublications) Get(_ context.Context, network string) (*interfaces.KBPublication, error) {
	return p.values[network], nil
}
func TestRefreshReleasesPreviousHoldsWithTheirRecordedNetworkOwner(t *testing.T) {
	ctx := context.Background()
	service, _, pubs, _, previous, sources := refreshPreparationFixture(t)
	for i := range previous.Files {
		previous.Files[i].File.ContentVersionID = "old-version"
	}
	cleanup := &cleanupSources{preparedSources: sources}
	service.Sources = cleanup
	journal := &memoryRefreshJournal{}
	service.Journal = journal
	beginReviewed(t, service, ctx, "base", "batch", "worker")
	old := *pubs.value
	old.NetworkID = "previous-network"
	service.Publications = &generationPublications{values: map[string]*interfaces.KBPublication{pubs.value.NetworkID: pubs.value, old.NetworkID: &old}}
	journal.value.Holds = []interfaces.KBHoldGroup{{ID: "old-hold", KnowledgeBaseID: "1"}}
	journal.value.HoldNetworkID = old.NetworkID
	journal.value.State = "finishing"
	record, err := journal.Get(ctx, "base")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.finish(ctx, record); err != nil {
		t.Fatal(err)
	}
	if len(cleanup.networks) != 1 || cleanup.networks[0] != old.NetworkID || !journal.value.Holds[0].Released || journal.value.Pending == nil {
		t.Fatal("old hold released as current network or cleanup checkpoint lost")
	}
	current, err := journal.Get(ctx, "base")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.finish(ctx, current); err != nil {
		t.Fatal(err)
	}
	if journal.value.Pending != nil || journal.value.HoldNetworkID != pubs.value.NetworkID || len(journal.value.Holds) != 1 || journal.value.Holds[0].ID == "old-hold" {
		t.Fatal("new holds were not committed under current owner")
	}
}
