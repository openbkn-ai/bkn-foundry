// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kntools

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

func TestExecuteFunctionEvidenceUsesAuthorizedExecutionDescriptor(t *testing.T) {
	for _, scenario := range []struct{ version, want string }{
		{version: " v4 ", want: "v4"},
		{want: "unversioned"},
	} {
		t.Run(scenario.want, func(t *testing.T) {
			spoof := map[string]any{"version": "caller-spoof", "kn_id": "other-network"}
			body := map[string]any{"exit_code": 0, "result": map[string]any{"value": 42}}
			operator := &executionEvidenceOperator{fakeOperator: &fakeOperator{
				toolsByBox: map[string]*interfaces.ListPublishedToolsResponse{"box-1": tools("box-1", "t1")},
				execResp:   map[string]any{"status_code": 200, "body": body, "execution_evidence": spoof},
			}, version: scenario.version}
			backend := &fakeBkn{refs: functionRefs("box-1/t1")}
			authorization := &fakeKnAuthz{}
			tracer := &fakeManagedFunctionTrace{}
			service := NewKnToolsServiceWithManagedTrace(operator, backend, authorization, tracer)
			result, err := service.ExecuteTool(context.Background(), &ExecuteToolReq{KnID: "kn1", ToolboxID: "box-1", ToolID: "t1", Arguments: map[string]any{"id": "A"}})
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"kn_id": "kn1", "toolbox_id": "box-1", "tool_id": "t1", "metadata_type": "function", "version": scenario.want}
			if !reflect.DeepEqual(result["execution_evidence"], want) || result["status_code"] != 200 || !reflect.DeepEqual(result["body"], body) {
				t.Fatalf("execution evidence or business envelope changed: %#v", result)
			}
			if !reflect.DeepEqual(operator.execResp["execution_evidence"], spoof) {
				t.Fatal("execution metadata mutated the upstream response")
			}
			if operator.lifecycleReads != 1 || operator.executionCount != 1 || authorization.gotKNID != "kn1" || operator.gotProxy == nil {
				t.Fatalf("execution bypassed authorization or lifecycle: reads=%d executes=%d auth=%s proxy=%#v", operator.lifecycleReads, operator.executionCount, authorization.gotKNID, operator.gotProxy)
			}
			if tracer.calls != 0 || operator.gotOperationID != "" {
				t.Fatal("contextless execution created Trace")
			}
		})
	}
}

func TestExecuteFunctionEvidencePreservesErrorsAndOtherCapabilities(t *testing.T) {
	failure := errors.New("execution refused")
	for _, scenario := range []struct {
		name, metadataType string
		err                error
	}{
		{name: "function failure", metadataType: "function", err: failure},
		{name: "api response", metadataType: "api"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			upstream := map[string]any{"status_code": 503, "body": "upstream response"}
			operator := &executionEvidenceOperator{fakeOperator: &fakeOperator{
				toolsByBox:      map[string]*interfaces.ListPublishedToolsResponse{"box-1": tools("box-1", "t1")},
				boxMetadataType: map[string]string{"box-1": scenario.metadataType},
				execResp:        upstream, execErr: scenario.err,
			}, version: "v4"}
			service := NewKnToolsServiceWith(operator, &fakeBkn{refs: functionRefs("box-1/t1")}, &fakeKnAuthz{})
			result, err := service.ExecuteTool(context.Background(), &ExecuteToolReq{KnID: "kn1", ToolboxID: "box-1", ToolID: "t1"})
			if err != scenario.err || !reflect.DeepEqual(result, upstream) || result["execution_evidence"] != nil {
				t.Fatalf("response or error changed: result=%#v err=%v", result, err)
			}
		})
	}
}

type executionEvidenceOperator struct {
	*fakeOperator
	version        string
	lifecycleReads int
}

func (operator *executionEvidenceOperator) ToolBoxLifecycle(ctx context.Context, box string) (*interfaces.ToolBoxLifecycle, error) {
	operator.lifecycleReads++
	state, err := operator.fakeOperator.ToolBoxLifecycle(ctx, box)
	if err == nil && state != nil {
		descriptor := state.EnabledToolDescriptors["t1"]
		descriptor.Version = operator.version
		state.EnabledToolDescriptors["t1"] = descriptor
	}
	return state, err
}
