// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package toolbox

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

// The bundle creates tools straight in the toolbox through CreateTool; no other resource is
// registered on the way.
func TestRegisterOpenApiBundleCreatesToolsInExistingToolBox(t *testing.T) {
	svc := &ToolServiceImpl{}
	var got *interfaces.CreateToolReq
	patches := gomonkey.ApplyMethod(reflect.TypeOf(svc), "CreateTool",
		func(_ *ToolServiceImpl, _ context.Context, req *interfaces.CreateToolReq) (*interfaces.CreateToolResp, error) {
			got = req
			return &interfaces.CreateToolResp{
				BoxID:        req.BoxID,
				SuccessCount: 2,
				SuccessIDs:   []string{"tool-1", "tool-2"},
				FailureCount: 1,
				Failures:     []interfaces.CreateToolFailureResult{{ToolName: "dup", Error: errors.New("tool exists")}},
			}, nil
		})
	defer patches.Reset()

	resp, err := svc.RegisterOpenApiBundle(context.Background(), &interfaces.RegisterOpenApiBundleReq{
		UserID:     "user-1",
		BoxID:      "box-1",
		BoxSvcURL:  "https://example.test",
		UseRule:    "rule",
		Data:       `{"openapi":"3.0.0"}`,
		ExtendInfo: map[string]interface{}{"k": "v"},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got == nil || got.BoxID != "box-1" || got.UserID != "user-1" || got.MetadataType != interfaces.MetadataTypeAPI ||
		got.UseRule != "rule" || got.OpenAPIInput == nil || string(got.OpenAPIInput.Data) != `{"openapi":"3.0.0"}` ||
		got.ExtendInfo["k"] != "v" {
		t.Fatalf("CreateTool request = %+v", got)
	}
	if resp.BoxID != "box-1" || !reflect.DeepEqual(resp.ToolIDs, []string{"tool-1", "tool-2"}) ||
		!reflect.DeepEqual(resp.Links, []interfaces.OpenApiBundleLink{{ToolID: "tool-1"}, {ToolID: "tool-2"}}) {
		t.Fatalf("resp = %+v", resp)
	}
	if resp.FailureCount != 1 || !reflect.DeepEqual(resp.Failures, []string{"dup: tool exists"}) {
		t.Fatalf("failures = %d %v", resp.FailureCount, resp.Failures)
	}
}

func TestRegisterOpenApiBundleCreatesToolBoxWhenOnlyNameGiven(t *testing.T) {
	svc := &ToolServiceImpl{}
	var boxReq *interfaces.CreateToolBoxReq
	patches := gomonkey.ApplyMethod(reflect.TypeOf(svc), "CreateToolBox",
		func(_ *ToolServiceImpl, _ context.Context, req *interfaces.CreateToolBoxReq) (*interfaces.CreateToolBoxResp, error) {
			boxReq = req
			return &interfaces.CreateToolBoxResp{BoxID: "new-box"}, nil
		})
	defer patches.Reset()
	patches.ApplyMethod(reflect.TypeOf(svc), "CreateTool",
		func(_ *ToolServiceImpl, _ context.Context, req *interfaces.CreateToolReq) (*interfaces.CreateToolResp, error) {
			if req.BoxID != "new-box" {
				t.Errorf("CreateTool box = %q, want new-box", req.BoxID)
			}
			return &interfaces.CreateToolResp{BoxID: req.BoxID, SuccessIDs: []string{"tool-1"}}, nil
		})

	resp, err := svc.RegisterOpenApiBundle(context.Background(), &interfaces.RegisterOpenApiBundleReq{
		UserID: "user-1", BoxName: "box", BoxSvcURL: "https://example.test", Category: "other_category", Data: "{}",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if boxReq == nil || boxReq.BoxName != "box" || boxReq.MetadataType != interfaces.MetadataTypeAPI {
		t.Fatalf("CreateToolBox request = %+v", boxReq)
	}
	if resp.BoxID != "new-box" || len(resp.ToolIDs) != 1 {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestRegisterOpenApiBundleRejectsBundleWithoutTools(t *testing.T) {
	svc := &ToolServiceImpl{}
	patches := gomonkey.ApplyMethod(reflect.TypeOf(svc), "CreateTool",
		func(_ *ToolServiceImpl, _ context.Context, req *interfaces.CreateToolReq) (*interfaces.CreateToolResp, error) {
			return &interfaces.CreateToolResp{BoxID: req.BoxID, FailureCount: 1,
				Failures: []interfaces.CreateToolFailureResult{{ToolName: "a", Error: errors.New("bad")}}}, nil
		})
	defer patches.Reset()

	resp, err := svc.RegisterOpenApiBundle(context.Background(), &interfaces.RegisterOpenApiBundleReq{
		UserID: "user-1", BoxID: "box-1", BoxSvcURL: "https://example.test", Data: "{}",
	})
	var httpErr *httperrors.HTTPError
	if resp != nil || !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusBadRequest {
		t.Fatalf("resp = %+v err = %v, want 400", resp, err)
	}
}

func TestRegisterOpenApiBundleRequiresToolBox(t *testing.T) {
	svc := &ToolServiceImpl{}
	_, err := svc.RegisterOpenApiBundle(context.Background(), &interfaces.RegisterOpenApiBundleReq{
		UserID: "user-1", BoxSvcURL: "https://example.test", Data: "{}",
	})
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusBadRequest {
		t.Fatalf("err = %v, want 400", err)
	}
}
