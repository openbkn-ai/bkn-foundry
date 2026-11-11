// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_type

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	sq "github.com/Masterminds/squirrel"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

func TestOperationFiltersAreSharedByCountAndPageQueries(t *testing.T) {
	query := interfaces.ActionTypesQueryParams{KNID: "kn", Branch: "main", OperationKind: "relation", RelationOperation: "delete"}
	for _, selectField := range []string{"COUNT(f_id)", "f_id"} {
		statement, values, err := processQueryCondition(query, sq.Select(selectField).From(AT_TABLE_NAME)).ToSql()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(statement, "JSON_VALUE(f_operation_config, '$.type') = ?") || !strings.Contains(statement, "JSON_VALUE(f_operation_config, '$.relation_operation') = ?") {
			t.Fatalf("missing server-side operation filter: %s", statement)
		}
		if len(values) != 4 || values[2] != "relation" || values[3] != "delete" {
			t.Fatalf("unexpected predicate values: %#v", values)
		}
	}
}

func TestOperationMetadataIsStoredForSingleAndBatchWrites(t *testing.T) {
	action := &interfaces.ActionType{ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{OperationConfig: &interfaces.ActionOperationConfig{Type: "relation", RelationTypeID: "supplier", RelationOperation: "create", AnchorRole: "source"}}}
	fields, err := serializeActionTypeFields(action)
	if err != nil {
		t.Fatal(err)
	}
	var restored interfaces.ActionOperationConfig
	if err := json.Unmarshal(fields.operationConfig, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.RelationTypeID != "supplier" || restored.AnchorRole != "source" {
		t.Fatalf("lost relation semantics: %#v", restored)
	}
	insert, err := actionTypeInsertValues(action)
	if err != nil {
		t.Fatal(err)
	}
	update, err := actionTypeUpdateValues(action)
	if err != nil {
		t.Fatal(err)
	}
	if string(insert[12].([]byte)) != string(fields.operationConfig) || string(update[9].([]byte)) != string(fields.operationConfig) {
		t.Fatal("batch columns must preserve operation metadata")
	}
}

func TestStoredActionNumbersDoNotRound(t *testing.T) {
	var parameters []interfaces.Parameter
	if err := unmarshalStoredActionJSON([]byte(`[{"name":"id","value":9223372036854775807}]`), &parameters); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(parameters[0].Value) != "9223372036854775807" {
		t.Fatalf("rounded value: %#v", parameters[0].Value)
	}
}
