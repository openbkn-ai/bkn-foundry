// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"fmt"
	"slices"

	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// attachBuiltTimes gives each table of index when its local index was last built, read as the
// account from the build tasks vega-backend finished, whoever started them: a table already
// indexed when the model was confirmed started no build of the project's, yet was built. A catalog
// whose tasks the account may not list, or cannot be read, leaves its tables without one.
func (s *draftService) attachBuiltTimes(ctx context.Context, index *interfaces.ProjectIndex,
	account interfaces.AccountInfo) {
	catalogIDs := []string{}
	for _, item := range index.ObjectTypes {
		if item.Table.CatalogID != "" && !slices.Contains(catalogIDs, item.Table.CatalogID) {
			catalogIDs = append(catalogIDs, item.Table.CatalogID)
		}
	}
	slices.Sort(catalogIDs)
	built := map[string]int64{}
	for _, catalogID := range catalogIDs {
		times, visible, err := s.va.ListLastBuilds(ctx, account, catalogID)
		if err != nil {
			otellog.LogWarn(ctx, fmt.Sprintf("List finished index builds of catalog %s: %v", catalogID, err))
		}
		if err != nil || !visible {
			continue
		}
		for resourceID, time := range times {
			built[resourceID] = time
		}
	}
	for i := range index.ObjectTypes {
		index.ObjectTypes[i].BuiltTime = built[index.ObjectTypes[i].Table.ResourceID]
	}
}
