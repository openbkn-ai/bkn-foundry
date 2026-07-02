package common

import (
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
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
