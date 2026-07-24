package metadata

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces"
)

func TestStripLegacyTimeoutParameter(t *testing.T) {
	timeoutParam := func() *interfaces.Parameter {
		return &interfaces.Parameter{Name: "timeout", In: "query", Description: "函数执行超时时间，单位毫秒"}
	}

	Convey("剥掉遗留的 timeout 查询参数", t, func() {
		spec := &interfaces.APISpec{Parameters: []*interfaces.Parameter{timeoutParam()}}
		stripLegacyTimeoutParameter(spec)
		So(len(spec.Parameters), ShouldEqual, 0)
	})

	Convey("同名但不在 query 的业务参数保留", t, func() {
		spec := &interfaces.APISpec{Parameters: []*interfaces.Parameter{
			{Name: "timeout", In: "header"},
		}}
		stripLegacyTimeoutParameter(spec)
		So(len(spec.Parameters), ShouldEqual, 1)
	})

	Convey("其余参数不受影响", t, func() {
		spec := &interfaces.APISpec{Parameters: []*interfaces.Parameter{
			{Name: "page", In: "query"},
			timeoutParam(),
			{Name: "token", In: "header"},
		}}
		stripLegacyTimeoutParameter(spec)
		names := []string{}
		for _, p := range spec.Parameters {
			names = append(names, p.Name)
		}
		So(names, ShouldResemble, []string{"page", "token"})
	})

	Convey("空值不 panic", t, func() {
		So(func() { stripLegacyTimeoutParameter(nil) }, ShouldNotPanic)
		So(func() { stripLegacyTimeoutParameter(&interfaces.APISpec{}) }, ShouldNotPanic)
		So(func() {
			stripLegacyTimeoutParameter(&interfaces.APISpec{Parameters: []*interfaces.Parameter{nil}})
		}, ShouldNotPanic)
	})
}
