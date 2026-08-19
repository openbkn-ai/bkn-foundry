package common

import (
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

func TestBuildFunctionProxyExecutionEnv(t *testing.T) {
	Convey("Function proxy execution context should separate task and capability identifiers", t, func() {
		version := "11111111-1111-4111-8111-111111111111"

		env := buildFunctionProxyExecutionEnv(version)

		So(env["source"], ShouldEqual, "function_proxy")
		So(env["function_version_id"], ShouldEqual, version)
		So(env["task_id"], ShouldNotEqual, version)
		So(strings.HasPrefix(env["task_id"].(string), "function_proxy_"), ShouldBeTrue)
		So(env["capability_id"], ShouldNotEqual, version)
		So(env["capability_id"], ShouldEqual, "function_version:"+version)
	})
}

func TestNewFunctionExecuteResp(t *testing.T) {
	Convey("Sandbox execution details should reach the caller", t, func() {
		resp := &interfaces.ExecuteCodeResp{
			Stdout:        "hello\n",
			Stderr:        "warn\n",
			ReturnValue:   map[string]any{"ok": true},
			Metrics:       map[string]any{"duration_ms": 12},
			ExitCode:      1,
			ErrorMessage:  "boom",
			ExecutionTime: 1234,
			Artifacts:     []string{"out.csv"},
			SessionID:     "sess-1",
		}

		got := newFunctionExecuteResp(resp)

		Convey("output streams are preserved", func() {
			So(got.Stdout, ShouldEqual, "hello\n")
			So(got.Stderr, ShouldEqual, "warn\n")
			So(got.Result, ShouldResemble, map[string]any{"ok": true})
		})

		Convey("diagnostics that used to be dropped are now exposed", func() {
			So(got.ExitCode, ShouldEqual, 1)
			So(got.ErrorMessage, ShouldEqual, "boom")
			So(got.ExecutionTimeMS, ShouldEqual, int64(1234))
			So(got.Artifacts, ShouldResemble, []string{"out.csv"})
			So(got.SessionID, ShouldEqual, "sess-1")
		})
	})

	Convey("A successful run reports exit code zero", t, func() {
		got := newFunctionExecuteResp(&interfaces.ExecuteCodeResp{Stdout: "ok\n"})

		So(got.ExitCode, ShouldEqual, 0)
		So(got.ErrorMessage, ShouldBeEmpty)
	})
}

// bkn_token is a real credential, which is different from the user_id fields that are "only for tracking marks" and must be converted to.
// The process-level environment variable - sandbox_sdk.bkn on the sandbox side is only taken from here. If it is missing, it means "the configuration will not take effect".
func TestBuildFunctionExecutionEnvCarriesBKNContext(t *testing.T) {
	env := buildFunctionExecutionEnv(&interfaces.FunctionProxyExecuteCodeReq{
		BKNToken:          "tok",
		BKNConversationID: "conv_1",
		BKNInteractionID:  "int_1",
		UserID:            "u1",
	})

	if env["BKN_TOKEN"] != "tok" {
		t.Fatalf("BKN_TOKEN 未注入: %v", env["BKN_TOKEN"])
	}
	if env["BKN_CONVERSATION_ID"] != "conv_1" || env["BKN_INTERACTION_ID"] != "int_1" {
		t.Fatalf("会话上下文未注入: %v", env)
	}
	// Tracking labels are still in lowercase, so don’t mix the two sets of names.
	if env["user_id"] != "u1" {
		t.Fatalf("追踪标记丢了: %v", env)
	}
}

// If not passed, the empty string must be explicitly sent instead of leaving the key absent.
//
// Sessions are pooled and reused: caller A brings bkn_token to trigger slot creation, and the token then falls into the container-level env; subsequently.
// Caller B reuses the same session and does not pass the token. If there is no BKN_TOKEN key in this env, the remaining.
// It is A's token, and B's code accesses BKN as A. This is also the executionEnvKeys list.
// Reason for existence - "A complete set is issued for each execution, and the unknown is left blank.".
func TestBuildFunctionExecutionEnvClearsAbsentBKNContext(t *testing.T) {
	env := buildFunctionExecutionEnv(&interfaces.FunctionProxyExecuteCodeReq{})
	for _, key := range []string{"BKN_TOKEN", "BKN_CONVERSATION_ID", "BKN_INTERACTION_ID"} {
		value, ok := env[key]
		if !ok {
			t.Fatalf("%s 必须在场（缺席会让上一个调用方的值留下）: %v", key, env)
		}
		if value != "" {
			t.Fatalf("%s 未传时应为空串，得到 %v", key, value)
		}
	}
}

// The three keys must be listed in executionEnvKeys: newExecutionEnv relies on it to preset a null value. Missing registration means.
// Returning to conditional writing, the serial number remains the same.
func TestExecutionEnvKeysCoverBKNContext(t *testing.T) {
	keys := map[string]bool{}
	for _, k := range executionEnvKeys() {
		keys[k] = true
	}
	for _, want := range []string{"BKN_TOKEN", "BKN_CONVERSATION_ID", "BKN_INTERACTION_ID"} {
		if !keys[want] {
			t.Fatalf("%s 未登记进 executionEnvKeys", want)
		}
	}
	// Deriving schema will also execute user code, and the entire set must also be covered.
	inferred := inferSchemaExecutionEnv()
	for _, want := range []string{"BKN_TOKEN", "BKN_CONVERSATION_ID", "BKN_INTERACTION_ID"} {
		if value, ok := inferred[want]; !ok || value != "" {
			t.Fatalf("inferSchemaExecutionEnv 未覆盖 %s: %v", want, inferred)
		}
	}
}
