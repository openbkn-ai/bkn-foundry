package parsers

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestCheckRegexpHandler(t *testing.T) {
	ctx := context.Background()

	Convey("handler(event) 是合法入口", t, func() {
		So(checkRegexpHandler(ctx, "def handler(event):\n    return event"), ShouldBeNil)
	})

	Convey("@tool 装饰的函数是合法入口", t, func() {
		Convey("裸装饰器", func() {
			code := "from sandbox_sdk import tool\n@tool\ndef add(a: int, b: int) -> int:\n    return a + b"
			So(checkRegexpHandler(ctx, code), ShouldBeNil)
		})
		Convey("带参数的装饰器", func() {
			code := "@tool(name=\"add\")\ndef add(a, b):\n    return a + b"
			So(checkRegexpHandler(ctx, code), ShouldBeNil)
		})
		Convey("限定名写法", func() {
			code := "import sandbox_sdk\n@sandbox_sdk.tool\ndef add(a, b):\n    return a + b"
			So(checkRegexpHandler(ctx, code), ShouldBeNil)
		})
	})

	Convey("两种入口都没有时报错", t, func() {
		err := checkRegexpHandler(ctx, "x = 1")
		So(err, ShouldNotBeNil)
	})

	Convey("字符串里提到 @tool 不算入口", t, func() {
		err := checkRegexpHandler(ctx, "msg = \"use @tool to register\"")
		So(err, ShouldNotBeNil)
	})
}
