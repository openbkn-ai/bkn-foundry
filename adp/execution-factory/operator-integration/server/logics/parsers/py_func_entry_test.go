package parsers

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

// 保存期判定必须与沙箱执行期一致：放行了执行时找不到入口，或拒掉了沙箱支持的写法，
// 两种错都会让用户在两个环节看到相反的结论。
func TestCheckRegexpHandler(t *testing.T) {
	ctx := context.Background()

	Convey("handler(event) 是合法入口", t, func() {
		So(checkRegexpHandler(ctx, "def handler(event):\n    return event"), ShouldBeNil)
	})

	Convey("sandbox_sdk 的 @tool 是合法入口", t, func() {
		Convey("裸装饰器", func() {
			code := "from sandbox_sdk import tool\n@tool\ndef add(a, b):\n    return a + b"
			So(checkRegexpHandler(ctx, code), ShouldBeNil)
		})
		Convey("带参数的装饰器", func() {
			code := "from sandbox_sdk import tool\n@tool(name=\"add\")\ndef add(a, b):\n    return a + b"
			So(checkRegexpHandler(ctx, code), ShouldBeNil)
		})
		Convey("限定名写法", func() {
			code := "import sandbox_sdk\n@sandbox_sdk.tool\ndef add(a, b):\n    return a + b"
			So(checkRegexpHandler(ctx, code), ShouldBeNil)
		})
		Convey("as 别名（SDK 支持并有测试的写法）", func() {
			code := "from sandbox_sdk import tool as register\n@register\ndef add(a, b):\n    return a + b"
			So(checkRegexpHandler(ctx, code), ShouldBeNil)
		})
	})

	Convey("名字叫 tool 但不是 SDK 的,不算入口", t, func() {
		Convey("来自其它库", func() {
			code := "from langchain_core.tools import tool\n@tool\ndef search(q):\n    return q"
			So(checkRegexpHandler(ctx, code), ShouldNotBeNil)
		})
		Convey("用户自定义的同名装饰器", func() {
			code := "def tool(f):\n    return f\n@tool\ndef helper(x):\n    return x"
			So(checkRegexpHandler(ctx, code), ShouldNotBeNil)
		})
		Convey("配了 handler 时仍按 handler 放行", func() {
			code := "from langchain_core.tools import tool\n@tool\ndef s(q):\n    return q\ndef handler(event):\n    return s(event)"
			So(checkRegexpHandler(ctx, code), ShouldBeNil)
		})
	})

	Convey("注释或字符串里的 @tool 不算入口", t, func() {
		So(checkRegexpHandler(ctx, "# 用 @tool 注册\nx = 1"), ShouldNotBeNil)
		So(checkRegexpHandler(ctx, "msg = \"@tool\"\nx = 1"), ShouldNotBeNil)
	})

	Convey("两种入口都没有时报错", t, func() {
		So(checkRegexpHandler(ctx, "x = 1"), ShouldNotBeNil)
	})
}
