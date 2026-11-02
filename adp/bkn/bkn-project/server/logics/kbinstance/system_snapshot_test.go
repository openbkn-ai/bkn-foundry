// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

type systemAccess struct {
	interfaces.VegaAccess
	tables []*interfaces.VegaTable
	rows   []map[string]any
	query  string
	caller interfaces.AccountInfo
	reads  int
}

func (a *systemAccess) GetTables(_ context.Context, caller interfaces.VegaAccount, _ []string) ([]*interfaces.VegaTable, error) {
	a.caller = caller
	return a.tables, nil
}
func (a *systemAccess) QuerySnapshot(ctx context.Context, caller interfaces.VegaAccount, sql string, _ int, _ int, guard func(context.Context) error) ([]map[string]any, error) {
	a.reads++
	a.query = sql
	if caller != a.caller {
		return nil, ErrInvalidBuild
	}
	if err := guard(ctx); err != nil {
		return nil, err
	}
	return a.rows, nil
}
func TestSystemSnapshotUsesTheOperatorAndKeepsBusinessNumbersSeparateFromPrimaryKeys(t *testing.T) {
	target := &interfaces.DraftObjectType{ID: "contract", Adopted: true, Table: interfaces.DraftTable{ResourceID: "source"}, PrimaryKeys: []string{"id"}, Properties: []*interfaces.DraftProperty{{Name: "id", Type: "unsigned integer", MappedField: "system`id"}, {Name: "number", Type: "string", MappedField: "contract_number"}, {Name: "amount", Type: "decimal", MappedField: "total"}}}
	access := &systemAccess{tables: []*interfaces.VegaTable{{ID: "source", Columns: []interfaces.VegaColumn{{Name: "system`id", Type: "unsigned integer"}, {Name: "contract_number", Type: "string"}, {Name: "total", Type: "decimal"}}}}, rows: []map[string]any{{"bkn_f_0": "25.50", "bkn_f_1": json.Number("18446744073709551615"), "bkn_f_2": "C-001"}}}
	operator := interfaces.AccountInfo{ID: "modeler", Type: "user"}
	snapshot, err := ReadSystemSnapshot(context.Background(), access, operator, target, "number", []string{"amount"}, func(context.Context) error { return nil })
	if err != nil || len(snapshot.Targets) != 1 || snapshot.Targets[0].RecordID != json.Number("18446744073709551615") || snapshot.Records[0].Values["amount"] != json.Number("25.50") || len(snapshot.Digest) != 64 {
		t.Fatal("system snapshot changed business identity or precision", snapshot, err)
	}
	if access.caller != operator || !strings.Contains(access.query, "`system``id`") || !strings.Contains(access.query, "FROM {{source}}") {
		t.Fatal("operator or schema-bound SQL changed")
	}
	access.rows[0]["bkn_f_0"] = "2.55e1"
	equivalent, err := ReadSystemSnapshot(context.Background(), access, operator, target, "number", []string{"amount"}, func(context.Context) error { return nil })
	if err != nil || equivalent.Digest != snapshot.Digest {
		t.Fatal("numeric formatting created a false source change", err)
	}

}
func TestSystemSnapshotRejectsDriftDuplicatesNullsAndIncompleteRows(t *testing.T) {
	for _, scenario := range []string{"schema_changed", "duplicate_business", "duplicate_primary", "missing_field", "blank_business", "null_business"} {
		t.Run(scenario, func(t *testing.T) {
			target := &interfaces.DraftObjectType{Adopted: true, Table: interfaces.DraftTable{ResourceID: "source"}, PrimaryKeys: []string{"id"}, Properties: []*interfaces.DraftProperty{{Name: "id", Type: "integer", MappedField: "id"}, {Name: "number", Type: "string", MappedField: "number"}}}
			access := &systemAccess{tables: []*interfaces.VegaTable{{ID: "source", Columns: []interfaces.VegaColumn{{Name: "id", Type: "integer"}, {Name: "number", Type: "string"}}}}, rows: []map[string]any{{"bkn_f_0": json.Number("1"), "bkn_f_1": "A"}, {"bkn_f_0": json.Number("2"), "bkn_f_1": "B"}}}
			switch scenario {
			case "schema_changed":
				access.tables[0].Columns[1].Type = "integer"
			case "duplicate_business":
				access.rows[1]["bkn_f_1"] = "A"
			case "duplicate_primary":
				access.rows[1]["bkn_f_0"] = json.Number("1")
			case "missing_field":
				delete(access.rows[1], "bkn_f_1")
			case "blank_business":
				access.rows[1]["bkn_f_1"] = " "
			case "null_business":
				access.rows[1]["bkn_f_1"] = nil
			}
			snapshot, err := ReadSystemSnapshot(context.Background(), access, interfaces.AccountInfo{ID: "u", Type: "user"}, target, "number", nil, func(context.Context) error { return nil })
			if err == nil || snapshot != nil {
				t.Fatal("invalid full target snapshot accepted")
			}
			if scenario == "schema_changed" && access.reads != 0 {
				t.Fatal("changed schema was queried")
			}
		})
	}
}

func TestSystemSnapshotKeepsRowsBeyondTheSinglePageLimit(t *testing.T) {
	target := &interfaces.DraftObjectType{Adopted: true, Table: interfaces.DraftTable{ResourceID: "source"}, PrimaryKeys: []string{"id"}, Properties: []*interfaces.DraftProperty{{Name: "id", Type: "integer", MappedField: "id"}, {Name: "number", Type: "string", MappedField: "number"}}}
	access := &systemAccess{tables: []*interfaces.VegaTable{{ID: "source", Columns: []interfaces.VegaColumn{{Name: "id", Type: "integer"}, {Name: "number", Type: "string"}}}}}
	for i := 1; i <= 1001; i++ {
		access.rows = append(access.rows, map[string]any{"bkn_f_0": json.Number(fmt.Sprint(i)), "bkn_f_1": fmt.Sprintf("C-%04d", i)})
	}
	snapshot, err := ReadSystemSnapshot(context.Background(), access, interfaces.AccountInfo{ID: "u", Type: "user"}, target, "number", nil, func(context.Context) error { return nil })
	if err != nil || len(snapshot.Targets) != 1001 || len(snapshot.Records) != 1001 {
		t.Fatal("single-page truncation escaped as a complete snapshot", err)
	}
}
