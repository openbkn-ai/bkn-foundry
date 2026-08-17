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

// bkn_token 是真凭据，与 user_id 那几个「仅作追踪标记」的字段不同，必须确实转成
// 进程级环境变量——沙箱侧的 sandbox_sdk.bkn 只从这里取，漏了就是「配了不生效」。
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
	// 追踪标记照旧走小写键，两套命名不要混。
	if env["user_id"] != "u1" {
		t.Fatalf("追踪标记丢了: %v", env)
	}
}

// 不传时必须显式下发空串，而不是让键缺席。
//
// 会话是池化复用的：调用方 A 带 bkn_token 触发建槽，令牌随之落进容器级 env；随后
// 调用方 B 复用同一会话且不传令牌，若本次 env 里没有 BKN_TOKEN 这个键，合并后留下的
// 就是 A 的令牌，B 的代码便以 A 的身份访问 BKN。这也是 executionEnvKeys 那份清单
// 存在的理由——「每次执行下发全套、未知置空」。
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

// 三个键必须列进 executionEnvKeys：newExecutionEnv 靠它预置空值，漏登记就等于
// 回到条件写入，串号照旧。
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
	// 推导 schema 也会执行用户代码，同样要覆盖全套。
	inferred := inferSchemaExecutionEnv()
	for _, want := range []string{"BKN_TOKEN", "BKN_CONVERSATION_ID", "BKN_INTERACTION_ID"} {
		if value, ok := inferred[want]; !ok || value != "" {
			t.Fatalf("inferSchemaExecutionEnv 未覆盖 %s: %v", want, inferred)
		}
	}
}
