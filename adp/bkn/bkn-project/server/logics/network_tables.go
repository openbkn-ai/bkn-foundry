// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logics

import (
	"context"
	"fmt"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/bkn-specification/bkn"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// A network bound again to its tables, as after a sample created its catalog anew, reads other
// resource ids than the definitions the lineage keeps were written with. What the network reads now
// is what runs: the project shows the network's items with it, checks a release by it, and a
// release writes it back rather than the ids that are gone.

// NetworkTables reads, by object type id, the table each object type of the network reads now.
func NetworkTables(ctx context.Context, bba interfaces.BknBackendAccess, token string,
	knID string) (map[string]string, error) {
	objectTypes, err := bba.ListObjectTypeSources(ctx, token, knID)
	if err != nil {
		return nil, fmt.Errorf("list object types of network %s: %w", knID, err)
	}
	tables := map[string]string{}
	for _, ot := range objectTypes {
		source := ot.DataSource
		if source == nil || source.ID == "" || source.Type != "" && source.Type != interfaces.KN_DATA_SOURCE_RESOURCE {
			continue
		}
		tables[ot.ID] = source.ID
	}
	return tables, nil
}

// WithNetworkTables returns files with each object type that base defines just as files do reading
// the table tables give for it, when that is another: a release that does not change an object type
// keeps what the network reads. One the files change is the project's to bind, and stays as
// written. files itself is not changed.
func WithNetworkTables(files map[string][]byte, base map[string][]byte, tables map[string]string) map[string][]byte {
	if len(tables) == 0 {
		return files
	}
	out := make(map[string][]byte, len(files))
	for p, data := range files {
		out[p] = data
		if !strings.HasPrefix(p, "object_types/") || string(base[p]) != string(data) {
			continue
		}
		ot, err := bkn.ParseObjectTypeFile(string(data), p)
		if err != nil || ot.DataSource == nil || ot.DataSource.ID == "" {
			continue
		}
		now := tables[ot.ID]
		if now == "" || now == ot.DataSource.ID {
			continue
		}
		// The id stands in the data source's row as a table cell.
		rewritten := strings.Replace(string(data), "| "+ot.DataSource.ID+" |", "| "+now+" |", 1)
		if again, err := bkn.ParseObjectTypeFile(rewritten, p); err == nil && again.DataSource != nil &&
			again.DataSource.ID == now {
			out[p] = []byte(rewritten)
		}
	}
	return out
}
