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

// gpython 钉在 v0.2.0，只到 Python 3.4 级语法。沙箱跑的是 3.11，
// 这些写法解析不了但完全合法，判定必须放行而不是挡在保存之外。
func TestEntryPointBeyondGpythonSyntax(t *testing.T) {
	ctx := context.Background()

	cases := map[string]string{
		"f-string":        "def handler(event):\n    return f\"hi {event}\"",
		"async def":       "async def handler(event):\n    return event",
		"pydantic 类体注解": "from sandbox_sdk import tool\nfrom pydantic import BaseModel\nclass Req(BaseModel):\n    name: str\n@tool\ndef f(r: Req) -> dict:\n    return {}",
		"变量注解":            "x: int = 1\ndef handler(event):\n    return event",
		"walrus":          "def handler(event):\n    if (n := len(event)) > 0:\n        return n\n    return 0",
	}

	Convey("解析不了的新语法仍按有入口放行", t, func() {
		for name, code := range cases {
			Convey(name, func() {
				So(checkRegexpHandler(ctx, code), ShouldBeNil)
			})
		}
	})

	Convey("解析不了且确实没有入口时仍然报错", t, func() {
		So(checkRegexpHandler(ctx, "x: int = 1\ny = f\"{x}\""), ShouldNotBeNil)
	})
}
