// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	omock "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces/mock"
)

func TestActionFactsRequirePublishedDefinitionAndCurrentTarget(t *testing.T) {
	snapshot := map[string]any{"id": "pay", "revision": "v1"}
	encoded, _ := json.Marshal(snapshot)
	digest := sha256.Sum256(encoded)
	check := &interfaces.ActionFactCheck{KNID: "kn", ActionTypeID: "pay", DefinitionDigest: hex.EncodeToString(digest[:]),
		TargetIdentity: map[string]any{"order_id": "O1"}}
	action := interfaces.ActionType{ATID: "pay", ActionType: "modify", ObjectTypeID: "orders", OperationConfig: &interfaces.ActionOperationConfig{Type: "object"},
		ActionSource: interfaces.ActionSource{Type: interfaces.ActionSourceTypeMCP, McpID: "mcp", ToolName: "pay"}}

	for _, sample := range []struct {
		name   string
		stale  bool
		found  bool
		status int
	}{
		{"current target", false, true, 0},
		{"missing target", false, false, http.StatusBadRequest},
		{"stale definition", true, true, http.StatusConflict},
	} {
		t.Run(sample.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			models := omock.NewMockOntologyManagerAccess(ctrl)
			objects := omock.NewMockObjectTypeService(ctrl)
			models.EXPECT().GetActionType(gomock.Any(), "kn", interfaces.MAIN_BRANCH, "pay").Return(action, snapshot, true, nil)
			permissions := &actionPermissionStub{}
			svc := &actionSchedulerService{omAccess: models, ots: objects, permissions: permissions, proxy: &actionProxyResolverStub{}}
			actual := *check
			if sample.stale {
				actual.DefinitionDigest = hex.EncodeToString(make([]byte, 32))
			} else {
				models.EXPECT().GetObjectType(gomock.Any(), "kn", interfaces.MAIN_BRANCH, "orders").Return(actionTargetObject("orders", "order_id"), true, nil).Times(2)
				objects.EXPECT().GetObjectsByObjectTypeID(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, query *interfaces.ObjectQueryBaseOnObjectType) (interfaces.Objects, error) {
					if len(permissions.requirements) == 0 {
						t.Fatal("target data read before permission check")
					}
					if query.ObjectTypeID != "orders" || query.ActualCondition == nil {
						t.Fatalf("target was not resolved by identity: %+v", query)
					}
					if sample.found {
						return interfaces.Objects{Datas: []map[string]any{{"order_id": "O1", "status": "pending"}}}, nil
					}
					return interfaces.Objects{}, nil
				})
			}
			err := svc.CheckActionFacts(context.Background(), &actual)
			if sample.status == 0 {
				if err != nil {
					t.Fatalf("current target rejected: %v", err)
				}
				return
			}
			httpErr, ok := err.(*httperrors.HTTPError)
			if !ok || httpErr.HTTPCode != sample.status {
				t.Fatalf("check = %v, want HTTP %d", err, sample.status)
			}
		})
	}
}

func TestActionFactsDoNotReadTargetAfterPermissionRevocation(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	objects := omock.NewMockObjectTypeService(ctrl)
	snapshot := map[string]any{"revision": "v1"}
	encoded, _ := json.Marshal(snapshot)
	digest := sha256.Sum256(encoded)
	models.EXPECT().GetActionType(gomock.Any(), "kn", interfaces.MAIN_BRANCH, "pay").Return(interfaces.ActionType{
		ATID: "pay", ActionType: "modify", ObjectTypeID: "orders", OperationConfig: &interfaces.ActionOperationConfig{Type: "object"},
	}, snapshot, true, nil)
	models.EXPECT().GetObjectType(gomock.Any(), "kn", interfaces.MAIN_BRANCH, "orders").Return(actionTargetObject("orders", "order_id"), true, nil)
	denied := httperrors.NewHTTPError(context.Background(), http.StatusForbidden, httperrors.PublicError_Forbidden)
	svc := &actionSchedulerService{omAccess: models, ots: objects, permissions: &actionPermissionStub{err: denied}}
	err := svc.CheckActionFacts(context.Background(), &interfaces.ActionFactCheck{KNID: "kn", ActionTypeID: "pay",
		DefinitionDigest: hex.EncodeToString(digest[:]), TargetIdentity: map[string]any{"order_id": "O1"}})
	if err != denied {
		t.Fatalf("permission denial = %v, want %v", err, denied)
	}
}
