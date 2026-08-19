package parsers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"testing"

	jsoniter "github.com/json-iterator/go"
	myErr "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	. "github.com/smartystreets/goconvey/convey"
)

// Test API metadata parsing.
func TestSmokeJSONAPIAnalysis(t *testing.T) {
	Convey("TestSmokeJSONAPIAnalysis:测试Json API文档解析", t, func() {
		localPath := "../../tests/file/json/full_text_subdoc.json"
		content, err := os.ReadFile(localPath)
		So(err, ShouldBeNil)
		parser := &openAPIParser{
			Logger: logger.DefaultLogger(),
		}
		result, err := parser.getAllContent(context.Background(), content)
		So(err, ShouldBeNil)
		data, _ := jsoniter.Marshal(result.PathItems)
		err = os.WriteFile("TEST.json", data, 0644)
		So(err, ShouldBeNil)
		t.Logf("Successfully wrote API metadata to TEST.json")
		_ = os.Remove("TEST.json")
	})
}

func TestSmokeYAMLAPIAnalysis(t *testing.T) {
	Convey("TestSmokeYAMLAPIAnalysis: 测试yaml API文档解析", t, func() {
		localPath := "../../tests/file/yaml/default.yaml"
		content, err := os.ReadFile(localPath)
		So(err, ShouldBeNil)
		parser := &openAPIParser{
			Logger: logger.DefaultLogger(),
		}
		result, err := parser.getAllContent(context.Background(), content)
		So(err, ShouldBeNil)
		data, _ := jsoniter.Marshal(result.PathItems)
		err = os.WriteFile("TEST.yaml.json", data, 0644)
		So(err, ShouldBeNil)
		t.Logf("Successfully wrote API metadata to TEST.json")
		_ = os.Remove("TEST.yaml.json")
	})
}

func TestInvalidAPIAnalysis(t *testing.T) {
	parser := &openAPIParser{
		Logger: logger.DefaultLogger(),
	}
	type apiAnalysisTestCase struct {
		Name        string // test name.
		LocalPath   string // local file path.
		Content     []byte // File content.
		Code        string // Error code keyword.
		Description string // Error description keyword.
	}

	testcases := []apiAnalysisTestCase{
		{
			Name:        "文件格式错误",
			Content:     []byte("}"),
			Code:        myErr.ErrExtOpenAPISyntaxInvalid.String(),
			Description: "文件格式不正确，请检查是否符合OpenAPI 3.0规范",
		},
		{
			Name:        "Server URL 不存在",
			Content:     []byte(`{"openapi": "3.0.0","info": {"title": "Test API","version": "1.0.0"},"paths": {},"components": {}}`),
			Code:        myErr.ErrExtOpenAPIInvalidURLFormat.String(),
			Description: "URL格式错误，请检查URL是否符合规范",
		},
		{
			Name:        "Server URL格式错误",
			Content:     []byte(`{"openapi": "3.0.0","info": {"title": "Test API","version": "1.0.0"},"servers": [{"url": "sss" }],"paths": {},"components": {}}`),
			Code:        myErr.ErrExtOpenAPIInvalidURLFormat.String(),
			Description: "URL格式错误，请检查URL是否符合规范",
		},
		{
			Name:        "数据为空",
			Content:     nil,
			Code:        myErr.ErrExtOpenAPIInvalidSpecificationRequired.String(),
			Description: "缺少必需字段“openapi”，请检查是否有缺失字段",
		},
		{
			Name:        "Paths为空",
			Content:     []byte(`{ "openapi": "3.0.0", "info": { "title": "Test API", "version": "1.0.0" }, "servers": [ { "url": "http://localhost:8080" } ], "components": {} }`),
			Code:        myErr.ErrExtOpenAPIInvalidPath.String(),
			Description: "API路径定义缺失或格式错误，请检查路径定义是否正确",
		},
		{
			Name: "Method 错误",
			Content: []byte(`{ "openapi": "3.0.0", "info": { "title": "Test API", "version": "1.0.0" },
			"servers": [ { "url": "http://localhost:8080" } ],
			"paths": { "/test": { "aa": { "summary": "Test GET", "operationId": "testGet", "responses": { "200": { "description": "OK" } } } } }, "components": {} }`),
			Code:        myErr.ErrExtOpenAPIInvalidPath.String(),
			Description: "API路径定义缺失或格式错误，请检查路径定义是否正确",
		},
		{
			Name: "Path 错误",
			Content: []byte(`{ "openapi": "3.0.0", "info": { "title": "Test API", "version": "1.0.0" },
			"servers": [ { "url": "http://localhost:8080" } ], "paths": { "": { "get": { "summary": "Test GET",
			"operationId": "testGet", "responses": { "200": { "description": "OK" } } } } }, "components": {} }`),
			Code:        myErr.ErrExtOpenAPIInvalidPath.String(),
			Description: "API路径定义缺失或格式错误，请检查路径定义是否正确",
		},
		{
			Name: "Parameter Schema 类型错误",
			Content: []byte(`{ "openapi": "3.0.0", "info": { "title": "Test API", "version": "1.0.0" },
			"servers": [ { "url": "http://localhost:8080" } ], "paths": { "/test": { "get": { "summary": "Test GET", "operationId": "testGet",
			"parameters": [ { "name": "id", "in": "query", "required": true, "schema": { "type": "array" } } ], "responses": { "200": { "description": "OK" } } } } }, "components": {} }`),
			Code:        myErr.ErrExtOpenAPIInvalidSchemaType.String(),
			Description: "Schema类型“items”定义错误，请检查类型定义是否正确",
		},
		{
			Name: "Parameter 参数位置错误",
			Content: []byte(`{ "openapi": "3.0.0", "info": { "title": "Test API", "version": "1.0.0" },
			"servers": [ { "url": "http://localhost:8080" } ], "paths": { "/test": { "get": { "summary": "Test GET", "operationId":
			"testGet", "parameters": [ { "name": "id", "in": "sss", "required": true, "schema": { "type": "integer" } } ], "responses":
			{ "200": { "description": "OK" } } } } }, "components": {} }`),
			Code:        myErr.ErrExtOpenAPIInvalidParameterValue.String(),
			Description: "参数校验错误，请查看错误详情",
		},
		{
			Name: "Path 定义错误，缺少参数",
			Content: []byte(`{ "openapi": "3.0.0", "info": { "title": "Test API", "version": "1.0.0" }, "servers": [ { "url": "http://localhost:8080" } ],
			"paths": { "/test": { "get": { "summary": "Test GET", "operationId": "testGet", "parameters": [ { "name": "id", "in": "path", "required": true, "schema":
			{ "type": "integer" } } ], "responses": { "200": { "description": "OK" } } } } }, "components": {} }`),
			Code:        myErr.ErrExtOpenAPIInvalidParameterValue.String(),
			Description: "参数校验错误，请查看错误详情",
		},
		{
			Name: "requestBody 缺少引用",
			Content: []byte(`{ "openapi": "3.0.0", "info": { "title": "Test API", "version": "1.0.0" }, "servers": [ { "url": "http://localhost:8080" } ],
			"paths": { "/test": { "get": { "summary": "Test GET", "operationId": "testGet", "requestBody": { "content": { "application/json": { "schema":
			{ "$ref": "#/components/schemas/TestRequest" } } } }, "responses": { "200": { "description": "OK" } } } } }, "components": {} }`),
			Code:        myErr.ErrExtOpenAPISyntaxInvalid.String(),
			Description: "文件格式不正确，请检查是否符合OpenAPI 3.0规范",
		},
		{
			Name: "responses 缺少引用",
			Content: []byte(`{ "openapi": "3.0.0", "info": { "title": "Test API", "version": "1.0.0" },
			"servers": [ { "url": "http://localhost:8080" } ], "paths": { "/test": { "get": { "summary": "Test GET", "operationId": "testGet",
			"requestBody": { "content": { "application/json": { "schema": { "$ref": "#/components/schemas/TestRequest" } } } }, "responses": {} } } },
			"components": { "schemas": { "TestRequest": { "type": "object" } } } }`),
			Code:        myErr.ErrExtOpenAPIInvalidResponseSchema.String(),
			Description: "响应Schema定义错误，请查看错误详情",
		},
	}
	for _, testcase := range testcases {
		Convey(testcase.Name, t, func() {
			content := testcase.Content
			var err error
			if testcase.LocalPath != "" {
				content, err = os.ReadFile(testcase.LocalPath)
				So(err, ShouldBeNil)
			}
			_, err = parser.getAllContent(context.Background(), content)
			fmt.Println(err)
			So(err, ShouldNotBeNil)
			httpErr := &myErr.HTTPError{}
			So(errors.As(err, &httpErr), ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
			So(httpErr.Code, ShouldContainSubstring, testcase.Code)
			So(httpErr.Description, ShouldEqual, testcase.Description)
		})
	}
}

// func TestMUSTObjectErrorHandling(t *testing.T) {
// Convey("Test MUST be an object type error handling", t, func() {.
// 		ctx := context.Background()

// 		testCases := []struct {
// 			name           string
// 			errorString    string
// 			expectedCode   myErr.ErrorCode
// 			expectedParams []interface{}
// 		}{
// 			{
// name: "Parameter must be of object type",
// 				errorString:    `invalid parameter 'id': value MUST be an object`,
// 				expectedCode:   myErr.ErrExtOpenAPIInvalidParameter,
// 				expectedParams: []interface{}{"id"},
// 			},
// 			{
// name: "Response must be of type object",
// 				errorString:    `invalid response '200': value MUST be an object`,
// 				expectedCode:   myErr.ErrExtOpenAPIInvalidResponseDefinition,
// 				expectedParams: []interface{}{"200"},
// 			},
// 			{
// name: "Schema must be of object type",
// 				errorString:    `invalid schema 'User': value MUST be an object`,
// 				expectedCode:   myErr.ErrExtOpenAPIInvalidSchemaType,
// 				expectedParams: []interface{}{"User"},
// 			},
// 			{
// name: "Parameter name with spaces",
// 				errorString:    `invalid parameter 'user id': value MUST be an object`,
// 				expectedCode:   myErr.ErrExtOpenAPIInvalidParameter,
// 				expectedParams: []interface{}{"user id"},
// 			},
// 			{
// name: "Response name with special characters",
// 				errorString:    `invalid response '4xx': value MUST be an object`,
// 				expectedCode:   myErr.ErrExtOpenAPIInvalidResponseDefinition,
// 				expectedParams: []interface{}{"4xx"},
// 			},
// 		}

// 		for _, tc := range testCases {
// 			Convey(tc.name, func() {
// //Create simulated error.
// 				mockErr := &myErr.HTTPError{
// 					HTTPCode:    http.StatusBadRequest,
// 					Code:        tc.expectedCode.String(),
// 					Description: tc.errorString,
// 				}

// // Parse validation errors.
// 				httpErr := parseOpenAPIValidationError(ctx, mockErr)

// 				So(httpErr, ShouldNotBeNil)
// 				So(httpErr.Code, ShouldEqual, tc.expectedCode)
// 				// So(httpErr.ErrorParams, ShouldResemble, tc.expectedParams)
// 			})
// 		}

// Convey("Test mismatched MUST error", func() {.
// // Test for errors that do not contain MUST be an object.
// 			mockErr := fmt.Errorf(`invalid parameter 'id': value must be string`)
// 			httpErr := parseOpenAPIValidationError(ctx, mockErr)

// 			So(httpErr, ShouldNotBeNil)
// // Should fall back to default error handling.
// 			So(httpErr.Code, ShouldEqual, myErr.ErrExtOpenAPIInvalidParameter)
// 		})

// Convey("Testing regular expression edge cases", func() {.
// 			testCases := []struct {
// 				name        string
// 				errorString string
// 				shouldMatch bool
// 			}{
// 				{
// name: "standard format",
// 					errorString: `invalid parameter 'id': value MUST be an object`,
// 					shouldMatch: true,
// 				},
// 				{
// name: "With extra spaces",
// 					errorString: `invalid parameter  'id'  :  value  MUST  be  an  object`,
// 					shouldMatch: true,
// 				},
// 				{
// name: "single quote",
// 					errorString: `invalid parameter "id": value MUST be an object`,
// 					shouldMatch: true,
// 				},
// 				{
// name: "Does not contain MUST",
// 					errorString: `invalid parameter 'id': value should be an object`,
// 					shouldMatch: false,
// 				},
// 				{
// name: "does not contain object",
// 					errorString: `invalid parameter 'id': value MUST be a string`,
// 					shouldMatch: false,
// 				},
// 			}

// 			for _, tc := range testCases {
// 				Convey(tc.name, func() {
// 					matches := mustObjectRegex.FindStringSubmatch(tc.errorString)
// 					if tc.shouldMatch {
// 						So(matches, ShouldHaveLength, 3)
// 						So(matches[1], ShouldBeIn, "parameter", "response", "schema")
// 					} else {
// 						So(matches, ShouldBeEmpty)
// 					}
// 				})
// 			}
// 		})
// 	})
// }
