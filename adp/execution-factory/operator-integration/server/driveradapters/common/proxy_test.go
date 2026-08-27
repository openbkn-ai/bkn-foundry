package common

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

func TestBuildFunctionProxyExecutionEnv(t *testing.T) {
	Convey("Function proxy execution context should separate task and capability identifiers", t, func() {
		version := "11111111-1111-4111-8111-111111111111"

		env := buildFunctionProxyExecutionEnv(nil, version)

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
	env := buildFunctionExecutionEnv(nil, &interfaces.FunctionProxyExecuteCodeReq{
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
	env := buildFunctionExecutionEnv(nil, &interfaces.FunctionProxyExecuteCodeReq{})
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

// newRequestContext builds a gin context standing in for an authenticated inbound
// request: the bearer credential and the auth context both middlewares put on the
// request.
func newRequestContext(token, accountID string) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/function/execute", nil)
	if token != "" {
		c.Request.Header.Set("Authorization", "Bearer "+token)
	}
	if accountID != "" {
		ctx := common.SetAccountAuthContextToCtx(c.Request.Context(),
			&interfaces.AccountAuthContext{AccountID: accountID})
		c.Request = c.Request.WithContext(ctx)
	}
	return c
}

// A caller that states nothing still reaches the sandbox with an identity.
//
// This is what run_code has always had and functions did not: Context Loader
// assembles its own execution, while the execution factory used to copy body
// fields and blank the rest. The Studio debug panel sends none of them, so every
// function debugged there saw an unconfigured BKN.
func TestBuildFunctionExecutionEnvFallsBackToRequest(t *testing.T) {
	env := buildFunctionExecutionEnv(
		newRequestContext("tok-req", "acct-req"),
		&interfaces.FunctionProxyExecuteCodeReq{},
	)

	if env["BKN_TOKEN"] != "tok-req" {
		t.Fatalf("BKN_TOKEN 未从请求兜底: %v", env["BKN_TOKEN"])
	}
	if env["user_id"] != "acct-req" {
		t.Fatalf("user_id 未从鉴权上下文兜底: %v", env["user_id"])
	}
}

// Backfill must never overwrite what the caller stated. A caller acting on behalf
// of another identity has to be able to say so, and silently replacing its values
// with the request's would run the code as the wrong account.
func TestBuildFunctionExecutionEnvKeepsExplicitFields(t *testing.T) {
	env := buildFunctionExecutionEnv(
		newRequestContext("tok-req", "acct-req"),
		&interfaces.FunctionProxyExecuteCodeReq{
			BKNToken:          "tok-body",
			BKNConversationID: "conv_body",
			BKNInteractionID:  "int_body",
			UserID:            "acct-body",
		},
	)

	for key, want := range map[string]string{
		"BKN_TOKEN":           "tok-body",
		"BKN_CONVERSATION_ID": "conv_body",
		"BKN_INTERACTION_ID":  "int_body",
		"user_id":             "acct-body",
	} {
		if env[key] != want {
			t.Fatalf("%s 被请求兜底覆盖了，期望 %s，得到 %v", key, want, env[key])
		}
	}
}

// Session context is never derived from the request, even when the inbound call
// carries the lifecycle correlation headers. #1161 rules out inferring function
// invocation context from HTTP trace headers alone; only bkn_conversation_id /
// bkn_interaction_id in the body set it. The keys still ship blank rather than
// absent, so a pooled container cannot keep the previous caller's session.
func TestBuildFunctionExecutionEnvDoesNotDeriveSessionContext(t *testing.T) {
	c := newRequestContext("tok-req", "acct-req")
	c.Request.Header.Set("bkn-conversation-id", "conv_header")
	c.Request.Header.Set("bkn-interaction-id", "int_header")

	env := buildFunctionExecutionEnv(c, &interfaces.FunctionProxyExecuteCodeReq{})

	for _, key := range []string{"BKN_CONVERSATION_ID", "BKN_INTERACTION_ID"} {
		value, ok := env[key]
		if !ok {
			t.Fatalf("%s 必须在场: %v", key, env)
		}
		if value != "" {
			t.Fatalf("%s 不该从请求头推导，得到 %v", key, value)
		}
	}
}

// The proxy path carries no body fields at all: a registered function is invoked
// by version, so the acting account has to come from the request.
func TestBuildFunctionProxyExecutionEnvFallsBackToRequestAccount(t *testing.T) {
	env := buildFunctionProxyExecutionEnv(
		newRequestContext("tok-req", "acct-req"),
		"11111111-1111-4111-8111-111111111111",
	)

	if env["user_id"] != "acct-req" {
		t.Fatalf("user_id 未从鉴权上下文兜底: %v", env["user_id"])
	}
}

// The proxy path must never hand the sandbox the caller's credential.
//
// It runs code registered by a third party, and its route authenticates by
// trusted header (hydra.GenerateVisitor) rather than by introspection, so the
// Authorization value is an unverified passthrough. Injecting it would let a
// function author read and exfiltrate the invoking user's live credential —
// and the sandbox has outbound network.
func TestBuildFunctionProxyExecutionEnvWithholdsCredential(t *testing.T) {
	env := buildFunctionProxyExecutionEnv(
		newRequestContext("tok-req", "acct-req"),
		"11111111-1111-4111-8111-111111111111",
	)

	value, ok := env["BKN_TOKEN"]
	if !ok {
		t.Fatalf("BKN_TOKEN 必须在场（缺席会让上一个调用方的值留下）: %v", env)
	}
	if value != "" {
		t.Fatalf("代理路径不得注入调用方凭据，得到 %v", value)
	}
}

// Routes that only ran the header-auth middleware have no auth context object,
// but both middlewares mirror the account onto the user_id header.
func TestRequestAccountIDFallsBackToHeader(t *testing.T) {
	c := newRequestContext("tok-req", "")
	c.Request.Header.Set(string(interfaces.HeaderUserID), "acct-header")

	if got := requestAccountID(c); got != "acct-header" {
		t.Fatalf("未从 user_id 头兜底: %v", got)
	}
}

// Schema derivation imports the module to read a signature. Handing that import a
// live credential widens what it can reach for no gain, so it stays blank even
// though the request carries one.
func TestInferSchemaExecutionEnvCarriesNoCredential(t *testing.T) {
	env := inferSchemaExecutionEnv()

	for _, key := range []string{"BKN_TOKEN", "BKN_CONVERSATION_ID", "BKN_INTERACTION_ID", "user_id"} {
		if value, ok := env[key]; !ok || value != "" {
			t.Fatalf("%s 不应带凭据: %v", key, env)
		}
	}
}
