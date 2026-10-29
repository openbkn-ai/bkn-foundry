// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"cmp"
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
// Version 2: ids split camelCase into words, and a physical name that is not ASCII gives the kind's
// prefix and a short hash of the name instead of field.
// Version 3: a physical name that is not ASCII takes its id from the project's registry, else from
// the short name semantic understanding gave, and the hash stands only until there is one.
const draftRulesVersion = 3

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
			MinInferredDistinct: setting.MinInferredDistinct,
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

// fingerprintedTable is what drafting reads of a table's structure, field by field: a field vega
// adds for something else does not change every fingerprint. Indices and foreign keys are kept in
// one order, since discovery may list them in any. The row count is left out: it changes without
// the structure changing.
type fingerprintedTable struct {
	Name         string                      `json:"name"`
	Description  string                      `json:"description"`
	SourceSchema string                      `json:"source_schema"`
	SourceName   string                      `json:"source_name"`
	APIName      string                      `json:"api_name,omitempty"`
	Columns      []interfaces.VegaColumn     `json:"columns"`
	PrimaryKeys  []string                    `json:"primary_keys"`
	Indices      []interfaces.VegaIndex      `json:"indices"`
	ForeignKeys  []interfaces.VegaForeignKey `json:"foreign_keys"`
}

// fingerprint hashes what drafting reads of a table's structure.
func fingerprint(table *interfaces.VegaTable) string {
	structure := fingerprintedTable{
		Name: table.Name, Description: table.Description, SourceSchema: table.SourceSchema,
		SourceName: table.SourceName, APIName: table.APIName, Columns: table.Columns, PrimaryKeys: table.PrimaryKeys,
		Indices:     slices.Clone(table.Indices),
		ForeignKeys: slices.Clone(table.ForeignKeys),
	}
	slices.SortFunc(structure.Indices, func(a, b interfaces.VegaIndex) int {
		return cmp.Or(slices.Compare(a.Columns, b.Columns), compareBool(a.Primary, b.Primary),
			compareBool(a.Unique, b.Unique))
	})
	slices.SortFunc(structure.ForeignKeys, func(a, b interfaces.VegaForeignKey) int {
		return cmp.Or(slices.Compare(a.Columns, b.Columns), strings.Compare(a.RefTable, b.RefTable),
			slices.Compare(a.RefColumns, b.RefColumns))
	})
	data, err := json.Marshal(structure)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func compareBool(a bool, b bool) int {
	switch {
	case a == b:
		return 0
	case !a:
		return -1
	}
	return 1
}
