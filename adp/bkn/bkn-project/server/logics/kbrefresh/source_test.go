// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbrefresh

import (
	"context"
	"errors"
	"testing"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

type snapshotSource struct {
	kb.UnavailableSource
	network                string
	scenario               string
	cursorReads, fileReads int
	revoked                bool
}

func (s *snapshotSource) expectedNetwork() string {
	if s.network != "" {
		return s.network
	}
	return "released-network"
}

func (s *snapshotSource) CurrentCursor(_ context.Context, scope kb.SourceScope) (string, error) {
	if scope.NetworkID != s.expectedNetwork() || scope.KnowledgeBaseID != "1" {
		return "", errors.New("wrong published network scope")
	}
	s.cursorReads++
	if s.scenario == "missing_kb" {
		return "", &kb.SourceError{StatusCode: 404}
	}
	if s.scenario == "denied" {
		return "", &kb.SourceError{StatusCode: 403}
	}
	if s.scenario == "changed" && s.cursorReads > 1 {
		return "after", nil
	}
	return "before", nil
}
func (s *snapshotSource) ListCatalogs(_ context.Context, scope kb.SourceScope, _ kb.CatalogQuery) (kb.CatalogPage, error) {
	if scope.NetworkID != s.expectedNetwork() || scope.KnowledgeBaseID != "1" {
		return kb.CatalogPage{}, errors.New("wrong catalog scope")
	}
	if s.scenario == "catalog_failed" {
		return kb.CatalogPage{}, &kb.SourceError{StatusCode: 503}
	}
	if s.scenario == "missing_catalog" {
		return kb.CatalogPage{Entries: []kb.Catalog{}}, nil
	}
	return kb.CatalogPage{Entries: []kb.Catalog{{ID: "catalog"}}}, nil
}
func (s *snapshotSource) ListFiles(_ context.Context, scope kb.SourceScope, query kb.FileQuery) (kb.FilePage, error) {
	if scope.NetworkID != s.expectedNetwork() || scope.KnowledgeBaseID != "1" {
		return kb.FilePage{}, errors.New("proxy identity escaped its network")
	}
	s.fileReads++
	if s.scenario == "empty" {
		return kb.FilePage{Entries: []kb.File{}}, nil
	}
	if s.scenario == "revoked" {
		s.revoked = true
	}
	if s.scenario == "partial" && query.Cursor != "" {
		return kb.FilePage{}, &kb.SourceError{StatusCode: 503}
	}
	page := kb.FilePage{Entries: []kb.File{{VersionRef: kb.VersionRef{FileID: "first", ContentVersionID: "v1"}, ChunkCount: 1}}}
	if s.scenario == "unparsed" {
		page.Entries[0].ContentVersionID = ""
	}
	if query.Cursor == "" || s.scenario == "repeated" {
		page.NextCursor = "next"
	} else {
		page.Entries[0].FileID = "second"
	}
	return page, nil
}
func TestPublishedSourceScanBindsCompleteManifestToChangeWatermarks(t *testing.T) {
	for _, scenario := range []string{"stable", "changed", "partial", "repeated", "denied", "revoked", "empty", "unparsed", "missing_catalog", "missing_kb", "catalog_failed"} {
		t.Run(scenario, func(t *testing.T) {
			source := &snapshotSource{scenario: scenario}
			guard := func(context.Context) error {
				if source.revoked {
					return errors.New("refresh cancelled")
				}
				return nil
			}
			snapshot, err := Scan(context.Background(), source, "released-network", []interfaces.KBSourceRange{{KnowledgeBaseID: "1", CatalogID: "catalog", IncludeOriginal: true}}, guard)
			succeeded := scenario == "stable" || scenario == "empty" || scenario == "missing_catalog"
			if (err == nil) != succeeded {
				t.Fatal("incorrect snapshot outcome", err)
			}
			if !succeeded && snapshot != nil {
				t.Fatal("partial source became a removal manifest")
			}
			if scenario == "stable" && (len(snapshot.Files) != 2 || snapshot.Cursors["1"] != "before" || !snapshot.Files[0].IncludeOriginal || source.cursorReads != 2) {
				t.Fatal("source scope or scan watermark lost")
			}
			if (scenario == "empty" || scenario == "missing_catalog") && (len(snapshot.Files) != 0 || source.cursorReads != 2) {
				t.Fatal("genuine complete empty source was not distinguished from failure")
			}
			if scenario == "denied" && source.fileReads != 0 {
				t.Fatal("files read before current permission check")
			}
		})
	}
}

func TestRemovedCatalogDoesNotHideOtherSelectedCatalogFiles(t *testing.T) {
	source := &snapshotSource{scenario: "stable"}
	snapshot, err := Scan(context.Background(), source, "released-network", []interfaces.KBSourceRange{{KnowledgeBaseID: "1", CatalogID: "catalog"}, {KnowledgeBaseID: "1", CatalogID: "gone"}}, func(context.Context) error { return nil })
	if err != nil || snapshot == nil || len(snapshot.Files) != 2 {
		t.Fatal("a removed catalog erased another selected catalog", err)
	}
}
