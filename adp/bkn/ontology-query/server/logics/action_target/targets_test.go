// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_target

import (
	"context"
	"net/http"
	"strings"
	"testing"

	cond "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common/condition"
	oerrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	omock "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"go.uber.org/mock/gomock"
)

func TestCreateRejectsMissingOrNullMappedProperty(t *testing.T) {
	tests := []struct {
		name        string
		objectInput map[string]any
		property    string
	}{
		{name: "missing primary key", objectInput: map[string]any{"name": "x"}, property: "plan_id"},
		{name: "null primary key", objectInput: map[string]any{"plan_id": nil, "name": "x"}, property: "plan_id"},
		{name: "missing mapped field", objectInput: map[string]any{"plan_id": "P1"}, property: "name"},
		{name: "null mapped field", objectInput: map[string]any{"plan_id": "P1", "name": nil}, property: "name"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			models := omock.NewMockOntologyManagerAccess(ctrl)
			object := interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
				OTID: "plans", PrimaryKeys: []string{"plan_id"}, DataProperties: []cond.DataProperty{
					{Name: "plan_id", Type: "string"}, {Name: "name", Type: "string"},
				},
			}}
			models.EXPECT().GetObjectType(gomock.Any(), "kn", "main", "plans").Return(object, true, nil)
			action := &interfaces.ActionType{ActionType: "add", ObjectTypeID: "plans", OperationConfig: &interfaces.ActionOperationConfig{Type: "object"}, Parameters: []interfaces.Parameter{
				{Name: "plan_id", ValueFrom: "property", Value: "plan_id"},
				{Name: "name", ValueFrom: "property", Value: "name"},
			}}
			req := &interfaces.ActionExecutionRequest{KNID: "kn", Branch: "main", ObjectInput: test.objectInput}
			instances, data, err := (&Resolver{Models: models}).Resolve(context.Background(), action, req)
			httpErr, ok := err.(*httperrors.HTTPError)
			if !ok || httpErr.HTTPCode != http.StatusBadRequest || httpErr.ErrorCode != oerrors.OntologyQuery_ActionExecution_InvalidParameter || instances != nil || data != nil {
				t.Fatalf("expected invalid-parameter 400 before execution, instances=%#v data=%#v err=%v", instances, data, err)
			}
			if !strings.Contains(err.Error(), test.property) {
				t.Fatalf("error should identify %q: %v", test.property, err)
			}
		})
	}
}

func TestResolveRejectsEmptySearchValuesBeforeEvaluatingTargets(t *testing.T) {
	for _, actionType := range []string{"add", "modify", "delete"} {
		for _, operation := range []string{"like", "not_like", "prefix", "not_prefix", "regex"} {
			for _, source := range []string{"", "const", "property", "input"} {
				t.Run(actionType+"/"+operation+"/"+source, func(t *testing.T) {
					ctrl := gomock.NewController(t)
					models := omock.NewMockOntologyManagerAccess(ctrl)
					objects := omock.NewMockObjectTypeService(ctrl)
					object := interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
						OTID: "products", PrimaryKeys: []string{"id"}, DataProperties: []cond.DataProperty{
							{Name: "id", Type: "string"}, {Name: "name", Type: "string"}, {Name: "pattern", Type: "string"},
						},
					}}
					models.EXPECT().GetObjectType(gomock.Any(), "kn", "main", "products").Return(object, true, nil)
					data := map[string]any{"id": "1", "name": "alpha", "pattern": ""}
					req := &interfaces.ActionExecutionRequest{KNID: "kn", Branch: "main", DynamicParams: map[string]any{"pattern": ""}}
					if actionType == "add" {
						req.ObjectInput = data
					} else {
						req.InstanceIdentities = []map[string]any{{"id": "1"}}
						// Only the identity lookup is allowed; no invalid condition may reach a query.
						objects.EXPECT().GetObjectsByObjectTypeID(gomock.Any(), gomock.Any()).Return(interfaces.Objects{Datas: []map[string]any{data}}, nil).Times(1)
					}
					condition := &cond.CondCfg{Name: "name", Operation: operation, ValueFrom: source, Value: ""}
					if source == "property" || source == "input" {
						condition.Value = "pattern"
					}
					action := &interfaces.ActionType{
						ActionType: actionType, ObjectTypeID: "products", OperationConfig: &interfaces.ActionOperationConfig{Type: "object"},
						Condition: condition, Parameters: []interfaces.Parameter{
							{Name: "id", ValueFrom: "property", Value: "id"},
							{Name: "name", ValueFrom: "property", Value: "name"},
							{Name: "pattern", ValueFrom: "property", Value: "pattern"},
						},
					}
					instances, datas, err := (&Resolver{Models: models, Objects: objects}).Resolve(context.Background(), action, req)
					httpErr, ok := err.(*httperrors.HTTPError)
					if !ok || httpErr.HTTPCode != http.StatusBadRequest || httpErr.ErrorCode != oerrors.OntologyQuery_ActionExecution_InvalidParameter || instances != nil || datas != nil {
						t.Fatalf("expected invalid-parameter 400 before target evaluation, instances=%#v datas=%#v err=%v", instances, datas, err)
					}
					if !strings.Contains(err.Error(), operation) || !strings.Contains(err.Error(), "non-empty string") {
						t.Fatalf("missing empty-pattern context: %v", err)
					}
					if source == "property" || source == "input" {
						if !strings.Contains(err.Error(), source) || !strings.Contains(err.Error(), "pattern") {
							t.Fatalf("missing reference context: %v", err)
						}
					}
				})
			}
		}
	}
}
