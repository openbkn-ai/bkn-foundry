// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// draftRulesVersion names the drafting rules; it moves whenever a change to them could draft the
// same tables differently, so drafts made under other rules are told apart.
const draftRulesVersion = 1

// draftInputsOf records what a draft is made from: the rules and their settings, the catalogs
// and the tables read, listed by resource ID.
func draftInputsOf(setting common.DraftSetting, catalogIDs []string, tables []*interfaces.VegaTable,
	unreadable []*interfaces.DraftTable) *interfaces.DraftInputs {
	inputs := &interfaces.DraftInputs{
		RulesVersion: draftRulesVersion,
		Settings: interfaces.DraftRuleSettings{
			CodeTableMaxRows:    setting.CodeTableMaxRows,
			CodeTableMaxColumns: setting.CodeTableMaxColumns,
			AdoptHitRate:        setting.AdoptHitRate,
			SuggestHitRate:      setting.SuggestHitRate,
			PrivacyKeywords:     setting.PrivacyKeywords,
			NameFieldKeywords:   setting.NameFieldKeywords,
		},
		CatalogIDs: append([]string{}, catalogIDs...),
		Tables:     make([]interfaces.DraftInputTable, 0, len(tables)),
		Partial:    len(unreadable) > 0,
	}
	for _, table := range tables {
		inputs.Tables = append(inputs.Tables, interfaces.DraftInputTable{
			ResourceID: table.ID, Fingerprint: fingerprint(table), Rows: table.Rows, RowsCounted: table.RowsCounted,
		})
	}
	slices.SortFunc(inputs.Tables, func(a, b interfaces.DraftInputTable) int {
		return strings.Compare(a.ResourceID, b.ResourceID)
	})
	return inputs
}

// fingerprint hashes what drafting reads of a table's structure. The row count is left out: it
// changes without the structure changing.
func fingerprint(table *interfaces.VegaTable) string {
	structure := *table
	structure.Rows, structure.RowsCounted = 0, false
	// A struct marshals its fields in declaration order, so the same structure hashes the same.
	data, err := json.Marshal(structure)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
