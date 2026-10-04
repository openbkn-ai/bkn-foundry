// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package mcp

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

func TestActualPythonStubCarriesNestedTraceGapToOuterMetadata(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	toolkit := ptcTestToolkit(t)
	program := toolkit.Stub + `
_ensure_session = lambda: None
def _rpc(method, params, notify=False):
    return {"result": {"content": [{"type": "text", "text": "{\"body\":\"actual business\"}"}, {"type": "text", "text": "[BKN_TRACE]{\"bkn_trace\":{\"available\":false,\"recorded\":false,\"partial_reasons\":[\"trace_call_unrecorded:execute_tool:req_nested_1\"]}}"}]}}
sys.stderr.write("actual user warning\n")
print(json.dumps(_call("execute_tool", {})))
`
	command := exec.Command(python, "-c", program)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("real stub failed: %v stderr=%s", err, &stderr)
	}
	if !strings.Contains(stderr.String(), "[BKN_TRACE_GAP]") {
		t.Fatalf("nested Trace metadata disappeared: stderr=%q", stderr.String())
	}
	executor := &fakeExecutor{resp: &interfaces.ExecuteFunctionResponse{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: 0}}
	ctx := bkntrace.WithTraceAvailability(context.Background())
	result, err := handlePTCExecute(executor, toolkit, ptcToolByName(t, "run_code"))(ctx, ptcCallRequest("run_code", map[string]any{"code": "print('business')"}))
	if err != nil || result.IsError || result.Meta == nil || result.Meta.AdditionalFields[traceAvailabilityMetaKey] == nil {
		t.Fatalf("outer lost nested gap: %#v err=%v", result, err)
	}
	value, ok := result.StructuredContent.(map[string]any)
	if !ok || value["stdout"] != stdout.String() || value["stderr"] != "actual user warning\n" || value["exit_code"] != 0 {
		t.Fatalf("gap transport changed actual business output: %#v", value)
	}
	reasons := bkntrace.TracePartialReasons(ctx)
	if len(reasons) != 1 || reasons[0] != "trace_call_unrecorded:execute_tool:req_nested_1" {
		t.Fatalf("gap did not reach finish reasons: %v", reasons)
	}
}

func TestPTCWorkspaceScopeCannotBeSuppliedInBusinessContext(t *testing.T) {
	executor := &fakeExecutor{}
	toolkit := ptcTestToolkit(t)
	result, err := handlePTCExecute(executor, toolkit, ptcToolByName(t, "run_shell"))(context.Background(), ptcCallRequest("run_shell", map[string]any{
		"command": "printf safe", "bkn_context": map[string]any{"conversation_id": "conv-1", "interaction_id": "int-1", "_workspace_scope": "../../other-owner"},
	}))
	if err != nil || result.IsError || executor.last == nil {
		t.Fatalf("handler failed: %#v err=%v", result, err)
	}
	if executor.last.WorkingDirectory != "conv-conv-1" || strings.Contains(executor.last.Code, "other-owner") {
		t.Fatalf("caller controlled internal namespace: %#v", executor.last)
	}
}
