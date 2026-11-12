// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"sort"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/bkn-specification/bkn"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

const relationTypesDir = "relation_types/"

// GetExisting reads the project's branch: the object and relation types there that the draft
// did not write are the network's own.
func (s *draftService) GetExisting(ctx context.Context, token string, projectID string) (*interfaces.DraftExisting, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "GetExistingDefinitions")
	defer span.End()

	entry, err := s.ps.GetProject(ctx, token, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get project failed")
		return nil, err
	}
	var owned []string
	if draft, exist, err := s.da.GetDraft(ctx, projectID); err != nil {
		span.SetStatus(codes.Error, "Get draft failed")
		return nil, internalError(ctx, "Get draft failed", err)
	} else if exist {
		owned = ownedPaths(draft.Items.Confirmation)
	}
	existing, err := s.readExisting(ctx, entry.KNID, entry.Branch, owned)
	if err != nil {
		span.SetStatus(codes.Error, "Read branch failed")
		return nil, internalError(ctx, "Read branch failed", err)
	}
	span.SetStatus(codes.Ok, "")
	return existing, nil
}

// ownedPaths are the paths a draft's confirmations wrote or were about to write.
func ownedPaths(confirmation *interfaces.DraftConfirmation) []string {
	if confirmation == nil {
		return nil
	}
	return unionSorted(confirmation.Files, confirmation.Pending)
}

// readExisting parses the definitions of the branch outside owned. A file that does not parse is
// left out, as the release review leaves it.
func (s *draftService) readExisting(ctx context.Context, lineageID string, branch string,
	owned []string) (*interfaces.DraftExisting, error) {
	files, err := s.gs.ReadTree(ctx, lineageID, "refs/heads/"+branch)
	if err != nil {
		return nil, err
	}
	return existingOf(files, owned), nil
}

func existingOf(files map[string][]byte, owned []string) *interfaces.DraftExisting {
	skip := make(map[string]bool, len(owned))
	for _, p := range owned {
		skip[p] = true
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		if !skip[p] {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	existing := &interfaces.DraftExisting{
		ObjectTypes:   []*interfaces.DraftExistingObjectType{},
		RelationTypes: []*interfaces.DraftExistingRelationType{},
	}
	for _, p := range paths {
		switch {
		case strings.HasPrefix(p, objectTypesDir):
			ot, err := bkn.ParseObjectTypeFile(string(files[p]), p)
			if err != nil || ot.ID == "" {
				continue
			}
			item := &interfaces.DraftExistingObjectType{
				ID: ot.ID, Name: ot.Name, Aliases: ot.Aliases, Description: ot.Description,
				PrimaryKeys: ot.PrimaryKeys, PropertyCount: len(ot.DataProperties),
			}
			if ot.DataSource != nil {
				item.TableID = ot.DataSource.ID
			}
			if item.PrimaryKeys == nil {
				item.PrimaryKeys = []string{}
			}
			existing.ObjectTypes = append(existing.ObjectTypes, item)
		case strings.HasPrefix(p, relationTypesDir):
			rt, err := bkn.ParseRelationTypeFile(string(files[p]), p)
			if err != nil || rt.ID == "" {
				continue
			}
			existing.RelationTypes = append(existing.RelationTypes, &interfaces.DraftExistingRelationType{
				ID: rt.ID, Name: rt.Name, Aliases: rt.Aliases, Source: rt.Endpoint.Source, Target: rt.Endpoint.Target,
			})
		}
	}
	return existing
}

// existingTables reads which tables the network's existing object types read, for drafting to
// leave out. A project without a git store, as in tests, has none.
func (s *draftService) existingTables(ctx context.Context, entry *interfaces.ProjectEntry,
	owned []string) (map[string]bool, error) {
	if s.gs == nil || entry == nil || entry.Project == nil {
		return map[string]bool{}, nil
	}
	existing, err := s.readExisting(ctx, entry.KNID, entry.Branch, owned)
	if err != nil {
		return map[string]bool{}, err
	}
	return existingTableIDs(existing), nil
}

// existingTablesOf reads the tables existing object types read, leaving out what the project's
// current draft wrote.
func (s *draftService) existingTablesOf(ctx context.Context, entry *interfaces.ProjectEntry) (map[string]bool, error) {
	if s.gs == nil || entry == nil || entry.Project == nil {
		return map[string]bool{}, nil
	}
	var owned []string
	draft, exist, err := s.da.GetDraft(ctx, entry.ID)
	if err != nil {
		return nil, err
	}
	if exist {
		owned = ownedPaths(draft.Items.Confirmation)
	}
	return s.existingTables(ctx, entry, owned)
}

// existingTableIDs are the tables the existing object types read.
func existingTableIDs(existing *interfaces.DraftExisting) map[string]bool {
	ids := map[string]bool{}
	if existing == nil {
		return ids
	}
	for _, ot := range existing.ObjectTypes {
		if ot.TableID != "" {
			ids[ot.TableID] = true
		}
	}
	return ids
}

// withoutExistingTables leaves out the tables an existing object type reads, so drafting does not
// model them a second time, and names the ones it left out.
func withoutExistingTables(tables []*interfaces.VegaTable, unreadable []*interfaces.DraftTable,
	used map[string]bool) ([]*interfaces.VegaTable, []*interfaces.DraftTable, []string) {
	if len(used) == 0 {
		return tables, unreadable, nil
	}
	kept := make([]*interfaces.VegaTable, 0, len(tables))
	left := []string{}
	for _, t := range tables {
		if used[t.ID] {
			left = append(left, t.Name)
			continue
		}
		kept = append(kept, t)
	}
	keptUnreadable := make([]*interfaces.DraftTable, 0, len(unreadable))
	for _, t := range unreadable {
		if used[t.ResourceID] {
			continue
		}
		keptUnreadable = append(keptUnreadable, t)
	}
	sort.Strings(left)
	return kept, keptUnreadable, left
}
