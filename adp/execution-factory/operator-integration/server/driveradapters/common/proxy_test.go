package common

import (
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces"
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
