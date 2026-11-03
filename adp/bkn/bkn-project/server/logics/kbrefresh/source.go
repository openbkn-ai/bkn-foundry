// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbrefresh

import (
	"context"
	"errors"
	"slices"
	"strings"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbresource"
)

var (
	ErrSourceChanged  = errors.New("knowledge-base sources changed during enumeration")
	ErrSourceNotReady = errors.New("knowledge-base files are awaiting a readable content version")
)

// SourceSnapshot is process metadata, never text or a prediction. Cursors name
// the complete scan's change boundary; they advance only after a batch commits.
type SourceSnapshot struct {
	Files   []interfaces.KBResourceFile
	Cursors map[string]string
}

type scopedScan struct {
	kb.Source
	network  string
	guard    func(context.Context) error
	requests int
	files    map[string]bool
}

func (s *scopedScan) scope(ctx context.Context, scope kb.SourceScope) (kb.SourceScope, error) {
	if scope.NetworkID != "" && scope.NetworkID != s.network {
		return scope, ErrInvalidManifest
	}
	if err := s.guard(ctx); err != nil {
		return scope, err
	}
	s.requests++
	// A finite scan must fail explicitly rather than manufacture an empty tail.
	if s.requests > 1000 {
		return scope, ErrInvalidManifest
	}
	scope.NetworkID = s.network
	return scope, nil
}
func (s *scopedScan) ListCatalogs(ctx context.Context, scope kb.SourceScope, query kb.CatalogQuery) (kb.CatalogPage, error) {
	scope, err := s.scope(ctx, scope)
	if err != nil {
		return kb.CatalogPage{}, err
	}
	return s.Source.ListCatalogs(ctx, scope, query)
}
func (s *scopedScan) ListFiles(ctx context.Context, scope kb.SourceScope, query kb.FileQuery) (kb.FilePage, error) {
	scope, err := s.scope(ctx, scope)
	if err != nil {
		return kb.FilePage{}, err
	}
	page, err := s.Source.ListFiles(ctx, scope, query)
	if err != nil {
		return kb.FilePage{}, err
	}
	for _, file := range page.Entries {
		if file.FileID == "" || file.ContentVersionID == "" {
			return kb.FilePage{}, ErrSourceNotReady
		}
		s.files[scope.KnowledgeBaseID+"/"+file.FileID] = true
		if len(s.files) > 1000 {
			return kb.FilePage{}, ErrInvalidManifest
		}
	}
	return page, nil
}

// Scan captures each KB's change watermark before and after complete enumeration.
// Any concurrent source edit, incomplete page or lost permission yields no usable
// manifest, so it cannot be mistaken for file removal. The next batch consumes
// changes after the captured cursor; it need not reread change history.
func Scan(ctx context.Context, source kb.ChangeSource, network string, ranges []interfaces.KBSourceRange, guard func(context.Context) error) (*SourceSnapshot, error) {
	if source == nil || strings.TrimSpace(network) == "" || len(ranges) == 0 || guard == nil {
		return nil, ErrInvalidManifest
	}
	ids := []string{}
	for _, scope := range ranges {
		if strings.TrimSpace(scope.KnowledgeBaseID) == "" {
			return nil, ErrInvalidManifest
		}
		ids = append(ids, scope.KnowledgeBaseID)
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	cursors := map[string]string{}
	for _, id := range ids {
		if err := guard(ctx); err != nil {
			return nil, err
		}
		cursor, err := source.CurrentCursor(ctx, kb.SourceScope{NetworkID: network, KnowledgeBaseID: id})
		if err != nil {
			return nil, err
		}
		if len(cursor) > 1024 {
			return nil, ErrInvalidManifest
		}
		cursors[id] = cursor
	}
	scoped := &scopedScan{Source: source, network: network, guard: guard, files: map[string]bool{}}
	active := slices.Clone(ranges)
	files := []interfaces.KBResourceFile{}
	for len(active) > 0 {
		current, err := kbresource.ScanFiles(ctx, scoped, active)
		if err == nil {
			files = current
			break
		}
		var missing *kbresource.MissingCatalogError
		if !errors.As(err, &missing) {
			return nil, err
		}
		remaining := slices.DeleteFunc(active, func(scope interfaces.KBSourceRange) bool {
			return scope.KnowledgeBaseID == missing.KnowledgeBaseID && scope.CatalogID == missing.CatalogID
		})
		if len(remaining) == len(active) {
			return nil, err
		}
		active = remaining
		// Discard all partial results before rescanning the remaining ranges.
		// Final KB watermark reads below still recheck access to removed ranges.
	}
	for _, id := range ids {
		if err := guard(ctx); err != nil {
			return nil, err
		}
		cursor, err := source.CurrentCursor(ctx, kb.SourceScope{NetworkID: network, KnowledgeBaseID: id})
		if err != nil {
			return nil, err
		}
		if cursor != cursors[id] {
			return nil, ErrSourceChanged
		}
	}
	if err := guard(ctx); err != nil {
		return nil, err
	}
	return &SourceSnapshot{Files: files, Cursors: cursors}, nil
}
