package parsers

import (
	"testing"

	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces"
	. "github.com/smartystreets/goconvey/convey"
)

// 参数定义经「展开成 OpenAPI 规格 -> 反解回参数定义」一个来回后应保持原样,
// 这样调用方读到的形状与提交时一致。
func roundTrip(input *interfaces.FunctionInput) (inputs, outputs []*interfaces.ParameterDef) {
	pathItem := convertToPathItemContent(input)
	return FunctionParamsFromAPISpec(pathItem.APISpec.ToJSON())
}

func TestFunctionParamsFromAPISpec(t *testing.T) {
	Convey("扁平参数往返后保持类型与必填标记", t, func() {
		inputs, outputs := roundTrip(&interfaces.FunctionInput{
			Name: "add",
			Inputs: []*interfaces.ParameterDef{
				{Name: "a", Type: interfaces.ParameterTypeNumber, Required: true, Description: "加数"},
				{Name: "b", Type: interfaces.ParameterTypeString, Required: false},
			},
			Outputs: []*interfaces.ParameterDef{
				{Name: "sum", Type: interfaces.ParameterTypeNumber, Required: true},
			},
		})

		So(len(inputs), ShouldEqual, 2)
		So(inputs[0].Name, ShouldEqual, "a")
		So(inputs[0].Type, ShouldEqual, interfaces.ParameterTypeNumber)
		So(inputs[0].Required, ShouldBeTrue)
		So(inputs[0].Description, ShouldEqual, "加数")
		So(inputs[1].Name, ShouldEqual, "b")
		So(inputs[1].Required, ShouldBeFalse)

		So(len(outputs), ShouldEqual, 1)
		So(outputs[0].Name, ShouldEqual, "sum")
		So(outputs[0].Required, ShouldBeTrue)
	})

	Convey("嵌套对象的子参数与其必填标记不丢失", t, func() {
		inputs, _ := roundTrip(&interfaces.FunctionInput{
			Name: "create",
			Inputs: []*interfaces.ParameterDef{
				{
					Name: "profile", Type: interfaces.ParameterTypeObject, Required: true,
					SubParameters: []*interfaces.ParameterDef{
						{Name: "city", Type: interfaces.ParameterTypeString, Required: true},
						{Name: "zipcode", Type: interfaces.ParameterTypeString, Required: false},
					},
				},
			},
		})

		So(len(inputs), ShouldEqual, 1)
		profile := inputs[0]
		So(profile.Type, ShouldEqual, interfaces.ParameterTypeObject)
		So(len(profile.SubParameters), ShouldEqual, 2)
		So(profile.SubParameters[0].Name, ShouldEqual, "city")
		So(profile.SubParameters[0].Required, ShouldBeTrue)
		So(profile.SubParameters[1].Name, ShouldEqual, "zipcode")
		So(profile.SubParameters[1].Required, ShouldBeFalse)
	})

	Convey("数组元素结构还原成单个 items 子参数", t, func() {
		_, outputs := roundTrip(&interfaces.FunctionInput{
			Name: "search",
			Outputs: []*interfaces.ParameterDef{
				{
					Name: "matched", Type: interfaces.ParameterTypeArray, Required: true,
					SubParameters: []*interfaces.ParameterDef{
						{
							Name: "items", Type: interfaces.ParameterTypeObject,
							SubParameters: []*interfaces.ParameterDef{
								{Name: "id", Type: interfaces.ParameterTypeString, Required: true},
							},
						},
					},
				},
			},
		})

		So(len(outputs), ShouldEqual, 1)
		matched := outputs[0]
		So(matched.Type, ShouldEqual, interfaces.ParameterTypeArray)
		So(len(matched.SubParameters), ShouldEqual, 1)
		item := matched.SubParameters[0]
		So(item.Type, ShouldEqual, interfaces.ParameterTypeObject)
		So(len(item.SubParameters), ShouldEqual, 1)
		So(item.SubParameters[0].Name, ShouldEqual, "id")
		So(item.SubParameters[0].Required, ShouldBeTrue)
	})

	Convey("规格缺失时返回空,不 panic", t, func() {
		inputs, outputs := FunctionParamsFromAPISpec("")
		So(inputs, ShouldBeNil)
		So(outputs, ShouldBeNil)

		inputs, outputs = FunctionParamsFromAPISpec("not json")
		So(inputs, ShouldBeNil)
		So(outputs, ShouldBeNil)
	})

	Convey("无参数函数往返后仍是空", t, func() {
		inputs, outputs := roundTrip(&interfaces.FunctionInput{Name: "noop"})
		So(len(inputs), ShouldEqual, 0)
		So(len(outputs), ShouldEqual, 0)
	})
}

func TestAPISpecCarriesOnlyDeclaredContract(t *testing.T) {
	Convey("api_spec 不宣告执行超时这类基础设施开关", t, func() {
		pathItem := convertToPathItemContent(&interfaces.FunctionInput{
			Name: "add",
			Inputs: []*interfaces.ParameterDef{
				{Name: "a", Type: interfaces.ParameterTypeNumber, Required: true},
			},
		})

		Convey("parameters 为空", func() {
			So(len(pathItem.APISpec.Parameters), ShouldEqual, 0)
		})

		Convey("业务入参仍在 request_body", func() {
			inputs, _ := FunctionParamsFromAPISpec(pathItem.APISpec.ToJSON())
			So(len(inputs), ShouldEqual, 1)
			So(inputs[0].Name, ShouldEqual, "a")
		})
	})
}

// 参数定义要经「展开成 OpenAPI 落库 → 读回来反解」一个来回，调用方拿到的是反解结果。
// 嵌套越深越容易在某一层丢结构，这里覆盖真实工具会用到的形状。
func TestNestedParamsSurviveRoundTrip(t *testing.T) {
	item := func(name string) *interfaces.ParameterDef {
		return &interfaces.ParameterDef{
			Name: name, Type: interfaces.ParameterTypeObject, Required: true,
			SubParameters: []*interfaces.ParameterDef{
				{Name: "id", Type: interfaces.ParameterTypeString, Required: true},
				{Name: "qty", Type: interfaces.ParameterTypeNumber, Required: false, Default: 1},
			},
		}
	}
	findParam := func(params []*interfaces.ParameterDef, name string) *interfaces.ParameterDef {
		for _, p := range params {
			if p.Name == name {
				return p
			}
		}
		return nil
	}

	Convey("对象数组的元素结构保留", t, func() {
		inputs, _ := roundTrip(&interfaces.FunctionInput{
			Name: "f",
			Inputs: []*interfaces.ParameterDef{{
				Name: "items", Type: interfaces.ParameterTypeArray, Required: true,
				SubParameters: []*interfaces.ParameterDef{item("items")},
			}},
		})

		So(len(inputs), ShouldEqual, 1)
		element := inputs[0].SubParameters[0]
		So(element.Type, ShouldEqual, interfaces.ParameterTypeObject)
		So(len(element.SubParameters), ShouldEqual, 2)
		So(findParam(element.SubParameters, "id").Required, ShouldBeTrue)
		qty := findParam(element.SubParameters, "qty")
		So(qty.Required, ShouldBeFalse)
		So(qty.Default, ShouldEqual, 1)
	})

	Convey("三层嵌套：对象里的数组里的对象", t, func() {
		inputs, _ := roundTrip(&interfaces.FunctionInput{
			Name: "f",
			Inputs: []*interfaces.ParameterDef{{
				Name: "envelope", Type: interfaces.ParameterTypeObject, Required: true,
				SubParameters: []*interfaces.ParameterDef{
					{Name: "no", Type: interfaces.ParameterTypeString, Required: true},
					{
						Name: "lines", Type: interfaces.ParameterTypeArray, Required: true,
						SubParameters: []*interfaces.ParameterDef{item("items")},
					},
				},
			}},
		})

		envelope := inputs[0]
		lines := findParam(envelope.SubParameters, "lines")
		So(lines, ShouldNotBeNil)
		So(lines.Type, ShouldEqual, interfaces.ParameterTypeArray)
		element := lines.SubParameters[0]
		So(findParam(element.SubParameters, "id").Type, ShouldEqual, interfaces.ParameterTypeString)
	})

	Convey("嵌套数组", t, func() {
		inputs, _ := roundTrip(&interfaces.FunctionInput{
			Name: "f",
			Inputs: []*interfaces.ParameterDef{{
				Name: "matrix", Type: interfaces.ParameterTypeArray, Required: true,
				SubParameters: []*interfaces.ParameterDef{{
					Name: "items", Type: interfaces.ParameterTypeArray,
					SubParameters: []*interfaces.ParameterDef{
						{Name: "items", Type: interfaces.ParameterTypeNumber},
					},
				}},
			}},
		})

		inner := inputs[0].SubParameters[0]
		So(inner.Type, ShouldEqual, interfaces.ParameterTypeArray)
		So(inner.SubParameters[0].Type, ShouldEqual, interfaces.ParameterTypeNumber)
	})

	Convey("输出参数的嵌套同样保留", t, func() {
		_, outputs := roundTrip(&interfaces.FunctionInput{
			Name: "f",
			Outputs: []*interfaces.ParameterDef{{
				Name: "matched", Type: interfaces.ParameterTypeArray, Required: true,
				SubParameters: []*interfaces.ParameterDef{item("items")},
			}},
		})

		element := outputs[0].SubParameters[0]
		So(len(element.SubParameters), ShouldEqual, 2)
	})
}
