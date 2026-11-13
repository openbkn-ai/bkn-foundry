// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logics

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// ProjectCatalogs tells which data catalogs a project reads. On a network the project did not
// create, the catalogs of the tables the network's object types read now are the project's too:
// they follow the network, read each time and never stored, so a network that moves to another
// catalog takes the project with it. The catalogs the owner added are kept beside them. A nil
// ProjectCatalogs leaves a project's catalogs as stored.
type ProjectCatalogs struct {
	BBA interfaces.BknBackendAccess
	VA  interfaces.VegaAccess
	DA  interfaces.DraftAccess
}

// NewProjectCatalogs reads catalogs through the service's driven adapters.
func NewProjectCatalogs() *ProjectCatalogs {
	return &ProjectCatalogs{BBA: BBA, VA: VA, DA: DA}
}

// NetworkCatalogs returns the catalogs of the tables the network's object types read, in the
// order the network lists them, as the caller sees them. The object types the project's own draft
// models are left out: on a network the project created they are all its own, so it has none.
func (pc *ProjectCatalogs) NetworkCatalogs(ctx context.Context, token string, caller interfaces.AccountInfo,
	project *interfaces.Project) ([]string, error) {
	objectTypes, err := pc.BBA.ListObjectTypeSources(ctx, token, project.KNID)
	if err != nil {
		return nil, fmt.Errorf("list object types of network %s: %w", project.KNID, err)
	}
	own := map[string]bool{}
	draft, exist, err := pc.DA.GetDraft(ctx, project.ID)
	if err != nil {
		return nil, fmt.Errorf("get draft: %w", err)
	}
	if exist {
		for _, ot := range draft.Items.ObjectTypes {
			if !ot.Existing {
				own[ot.ID] = true
			}
		}
		// What its confirmations wrote stays the project's while it drafts again, or after that
		// failed, when the draft holds no items.
		if c := draft.Items.Confirmation; c != nil {
			for _, path := range append(slices.Clone(c.Files), c.Pending...) {
				if id, ok := objectTypeOfPath(path); ok {
					own[id] = true
				}
			}
		}
	}
	resourceIDs := []string{}
	for _, ot := range objectTypes {
		source := ot.DataSource
		if own[ot.ID] || interfaces.IsSystemObjectType(ot.ID) || source == nil || source.ID == "" ||
			source.Type != "" && source.Type != interfaces.KN_DATA_SOURCE_RESOURCE {
			continue
		}
		if !slices.Contains(resourceIDs, source.ID) {
			resourceIDs = append(resourceIDs, source.ID)
		}
	}
	if len(resourceIDs) == 0 {
		return []string{}, nil
	}
	tables, err := pc.VA.GetTables(ctx, caller, resourceIDs)
	if err != nil {
		return nil, fmt.Errorf("read the tables of network %s: %w", project.KNID, err)
	}
	catalogOf := map[string]string{}
	for _, table := range tables {
		catalogOf[table.ID] = table.CatalogID
	}
	catalogs := []string{}
	for _, id := range resourceIDs {
		if c := catalogOf[id]; c != "" && !slices.Contains(catalogs, c) {
			catalogs = append(catalogs, c)
		}
	}
	return catalogs, nil
}

// objectTypeOfPath is the id of the object type a definition path holds, object_types/<id>.bkn.
func objectTypeOfPath(path string) (string, bool) {
	id, ok := strings.CutPrefix(path, "object_types/")
	if !ok {
		return "", false
	}
	id, ok = strings.CutSuffix(id, ".bkn")
	return id, ok && id != "" && !strings.Contains(id, "/")
}

// Resolve sets the entry's catalogs to those the project reads: the network's, then those the
// owner added. A catalog the project stored that is gone is dropped without a word there: the
// project cannot tell one it took from the network when it started from one added by hand, and a
// draft that read tables from it still tells those tables are gone. A network whose object types
// read no catalog, as one the project created, leaves the stored catalogs as they are, and so
// does a read that fails.
func (pc *ProjectCatalogs) Resolve(ctx context.Context, token string, caller interfaces.AccountInfo,
	entry *interfaces.ProjectEntry) {
	if pc == nil || entry == nil || entry.Project == nil {
		return
	}
	network, err := pc.NetworkCatalogs(ctx, token, caller, entry.Project)
	if err != nil {
		otellog.LogWarn(ctx, fmt.Sprintf("Read the catalogs the network of project %s reads: %v", entry.ID, err))
		return
	}
	if len(network) == 0 {
		return
	}
	added, err := pc.Added(ctx, caller, network, entry.CatalogIDs)
	if err != nil {
		otellog.LogWarn(ctx, fmt.Sprintf("Read the catalogs of project %s: %v", entry.ID, err))
	}
	entry.CatalogIDs = append(slices.Clone(network), added...)
	entry.NetworkCatalogIDs = network
}

// Added are the catalogs of stored outside network that still exist, in their order. When vega
// cannot tell which are gone, all are kept.
func (pc *ProjectCatalogs) Added(ctx context.Context, caller interfaces.AccountInfo, network []string,
	stored []string) ([]string, error) {
	added := slices.DeleteFunc(slices.Clone(stored), func(id string) bool { return slices.Contains(network, id) })
	if len(added) == 0 {
		return []string{}, nil
	}
	missing, err := pc.VA.MissingCatalogs(ctx, caller, added)
	if err != nil {
		return added, err
	}
	return slices.DeleteFunc(added, func(id string) bool { return slices.Contains(missing, id) }), nil
}
