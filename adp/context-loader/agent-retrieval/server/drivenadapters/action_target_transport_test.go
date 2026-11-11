// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"encoding/json"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/mocks"
	"go.uber.org/mock/gomock"
	"reflect"
	"testing"
)

func TestActionTransportForwardsExplicitTargetsAndPreciseValues(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := mocks.NewMockHTTPClient(ctrl)
	logger := mocks.NewMockLogger(ctrl)
	logger.EXPECT().WithContext(gomock.Any()).Return(logger).AnyTimes()
	logger.EXPECT().Debugf(gomock.Any(), gomock.Any()).AnyTimes()
	service := &ontologyQueryClient{httpClient: client, logger: logger, baseURL: "http://query"}
	target := &interfaces.ActionRelationTarget{SourceIdentity: map[string]any{"id": json.Number("9223372036854775807")}, TargetIdentity: map[string]any{"code": "0001"}}
	for _, execute := range []bool{false, true} {
		client.EXPECT().PostBytes(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, _ string, headers map[string]string, body any) (int, []byte, error) {
			values := body.(map[string]any)
			if !reflect.DeepEqual(values["relation_target"], target) {
				t.Fatalf("lost relation target: %#v", values)
			}
			if execute && values["dynamic_params"].(map[string]any)["notify"] != false {
				t.Fatal("lost false dynamic value")
			}
			if execute && headers["x-http-method-override"] != "" {
				t.Fatal("execution must be a real POST")
			}
			if !execute && headers["x-http-method-override"] != "GET" {
				t.Fatal("discovery must use method override")
			}
			return 200, []byte(`{"execution_id":"e1","execution_schema":{"type":"object","properties":{}}}`), nil
		})
		if execute {
			_, err := service.ExecuteActions(context.Background(), &interfaces.ExecuteActionsRequest{KnID: "kn", AtID: "a", RelationTarget: target, DynamicParams: map[string]any{"notify": false}})
			if err != nil {
				t.Fatal(err)
			}
		} else {
			response, err := service.QueryActions(context.Background(), &interfaces.QueryActionsRequest{KnID: "kn", AtID: "a", RelationTarget: target})
			if err != nil || response.ExecutionSchema == nil {
				t.Fatalf("schema=%#v err=%v", response, err)
			}
		}
	}
}
