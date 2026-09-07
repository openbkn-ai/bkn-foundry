// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package object_type

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"ontology-query/common"
	cond "ontology-query/common/condition"
	oerrors "ontology-query/errors"
	"ontology-query/interfaces"
	omock "ontology-query/interfaces/mock"
	"ontology-query/logics"
)

type objectTypeProxyResolverStub struct {
	bindings []interfaces.TrustedProxyBinding
	err      error
}

type fullPropertyAccessStub struct{}

func (fullPropertyAccessStub) ResolvePropertyLevels(_ context.Context,
	items []interfaces.PropertyLevelsRequestItem) ([]interfaces.PropertyLevelsDecisionEntry, error) {
	entries := make([]interfaces.PropertyLevelsDecisionEntry, 0, len(items))
	for _, item := range items {
		entry := interfaces.PropertyLevelsDecisionEntry{ObjectTypeRef: item.ObjectTypeRef}
		for _, name := range item.Properties {
			entry.Properties = append(entry.Properties, interfaces.PropertyAccessDecision{
				Name: name, Level: interfaces.PropertyAccessFull, Source: "test",
			})
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func (s *objectTypeProxyResolverStub) Resolve(ctx context.Context,
	binding interfaces.TrustedProxyBinding) (*interfaces.TrustedProxyContext, error) {
	s.bindings = append(s.bindings, binding)
	if s.err != nil {
		return nil, s.err
	}
	caller, _ := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	if caller.ID == "" {
		caller = interfaces.AccountInfo{ID: "test-caller", Type: "user"}
	}
	return &interfaces.TrustedProxyContext{
		Caller:                caller,
		Proxy:                 interfaces.AccountInfo{ID: "test-proxy", Type: interfaces.ProxyAccountTypeApp},
		ProxyVersion:          2,
		PublishedModelVersion: "model-v2",
		Binding:               binding,
	}, nil
}

func Test_NewObjectTypeService(t *testing.T) {
	Convey("Test NewObjectTypeService", t, func() {
		appSetting := &common.AppSetting{}

		Convey("成功 - 创建服务实例", func() {
			service := NewObjectTypeService(appSetting)
			So(service, ShouldNotBeNil)
		})

		Convey("成功 - 单例模式", func() {
			service1 := NewObjectTypeService(appSetting)
			service2 := NewObjectTypeService(appSetting)
			So(service1, ShouldEqual, service2)
		})
	})
}

func TestObjectTypeSchemaUsesPublishedViewDetailBinding(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	vega := omock.NewMockVegaBackendAccess(ctrl)
	proxy := &objectTypeProxyResolverStub{}
	service := &objectTypeService{omAccess: models, vba: vega, proxy: proxy, propertyAccess: fullPropertyAccessStub{}}

	models.EXPECT().GetObjectType(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, "ot-1").Return(
		interfaces.ObjectType{
			ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
				OTID: "ot-1", DataSource: &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "resource-1"},
				DataProperties: []cond.DataProperty{{Name: "id", Type: "string", MappedField: cond.Field{Name: "id"}}},
			},
			KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
		}, true, nil)
	vega.EXPECT().GetResourceSchema(gomock.Any(), "resource-1").DoAndReturn(
		func(ctx context.Context, _ string) (*interfaces.ResourceSchemaResponse, error) {
			trusted, ok := interfaces.TrustedProxyContextFromContext(ctx)
			if !ok || trusted.Binding.Operation != interfaces.PermissionOperationViewDetail {
				t.Fatalf("missing view_detail proxy context: %#v", trusted)
			}
			return &interfaces.ResourceSchemaResponse{SchemaDefinition: []map[string]any{{"name": "id", "type": "string"}}}, nil
		})

	got, err := service.GetObjectTypeSchema(context.Background(), "kn-1", interfaces.MAIN_BRANCH, "ot-1")
	if err != nil {
		t.Fatalf("GetObjectTypeSchema() error = %v", err)
	}
	if len(got.SchemaDefinition) != 1 || proxy.bindings[0].TargetID != "resource-1" ||
		proxy.bindings[0].Operation != interfaces.PermissionOperationViewDetail {
		t.Fatalf("unexpected schema/proxy binding: %#v %#v", got, proxy.bindings)
	}
}

func TestObjectTypeSampleDataUsesQueryDataProxyBinding(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	vega := &vegaStubForOTQuery{resp: &interfaces.DatasetQueryResponse{
		Entries: []map[string]any{{"field1": "sample"}}, TotalCount: 1,
	}}
	proxy := &objectTypeProxyResolverStub{}
	service := &objectTypeService{omAccess: models, vba: vega, proxy: proxy, propertyAccess: fullPropertyAccessStub{}}
	models.EXPECT().GetObjectType(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, "ot-1").Return(
		interfaces.ObjectType{
			ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
				OTID: "ot-1", OTName: "Orders",
				DataSource: &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "resource-1"},
				DataProperties: []cond.DataProperty{{
					Name: "order_id", DisplayName: "Order ID", MappedField: cond.Field{Name: "field1"},
				}},
				PrimaryKeys: []string{"order_id"}, DisplayKey: "order_id",
			},
			KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
		}, true, nil)

	got, err := service.GetObjectTypeSampleData(context.Background(), &interfaces.ObjectQueryBaseOnObjectType{
		KNID: "kn-1", Branch: interfaces.MAIN_BRANCH, ObjectTypeID: "ot-1",
	})
	if err != nil {
		t.Fatalf("GetObjectTypeSampleData() error = %v", err)
	}
	if got.Name != "Orders" || len(got.Columns) != 1 || got.Columns[0].DataIndex != "order_id" ||
		len(got.Entries) != 1 || got.Entries[0]["order_id"] != "sample" ||
		proxy.bindings[0].Operation != interfaces.PermissionOperationQueryData {
		t.Fatalf("unexpected sample response/binding: %#v %#v", got, proxy.bindings)
	}
}

func TestToolLogicPropertyUsesPublishedProxyBinding(t *testing.T) {
	const publishedBindingID = "d24404efa877194b29ee86381e672d23b44933439f4b0fcb11659f0fd4325406"
	if got := logicPropertyBindingID("kn-1", "ot-1", "risk_score"); got != publishedBindingID {
		t.Fatalf("logicPropertyBindingID() = %q, want BKN projection key %q", got, publishedBindingID)
	}
	ctrl := gomock.NewController(t)
	agentOperator := omock.NewMockAgentOperatorAccess(ctrl)
	proxy := &objectTypeProxyResolverStub{}
	service := &objectTypeService{aoAccess: agentOperator, proxy: proxy}
	logicProperty := &interfaces.LogicProperty{
		Name: "risk_score", Type: interfaces.LOGIC_PROPERTY_TYPE_TOOL,
		DataSource: &interfaces.ResourceInfo{BoxID: "box-1", ToolID: "tool-1"},
	}
	agentOperator.EXPECT().ExecuteToolAsProxy(gomock.Any(), "box-1", "tool-1", gomock.Any()).
		DoAndReturn(func(ctx context.Context, _, _ string, _ interfaces.ToolExecutionRequest) (any, error) {
			trusted, ok := interfaces.TrustedProxyContextFromContext(ctx)
			if !ok || trusted.Binding.ChildType != interfaces.PermissionResourceTypeLogicProperty ||
				trusted.Binding.ChildID != publishedBindingID ||
				trusted.Binding.TargetID != "box-1" ||
				trusted.Binding.Operation != interfaces.PermissionOperationExecute {
				t.Fatalf("unexpected logic-property proxy context: %#v", trusted)
			}
			return map[string]any{"score": 9}, nil
		})

	result, err := service.handleToolProperty(context.Background(), "kn-1", "ot-1", "risk_score",
		interfaces.ToolProperty{Parameters: map[string]any{}}, logicProperty, nil)
	if err != nil || result == nil || len(proxy.bindings) != 1 {
		t.Fatalf("handleToolProperty() = %#v, %v; bindings = %#v", result, err, proxy.bindings)
	}
}

func Test_objectTypeService_GetObjectsByObjectTypeID(t *testing.T) {
	Convey("Test objectTypeService GetObjectsByObjectTypeID", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		appSetting := &common.AppSetting{}
		omAccess := omock.NewMockOntologyManagerAccess(mockCtrl)
		osa := omock.NewMockOpenSearchAccess(mockCtrl)
		mfa := omock.NewMockModelFactoryAccess(mockCtrl)
		aoAccess := omock.NewMockAgentOperatorAccess(mockCtrl)

		logics.OMA = omAccess
		logics.OSA = osa
		logics.MFA = mfa
		logics.AOA = aoAccess

		service := &objectTypeService{
			appSetting:     appSetting,
			omAccess:       omAccess,
			osa:            osa,
			mfa:            mfa,
			aoAccess:       aoAccess,
			proxy:          &objectTypeProxyResolverStub{},
			propertyAccess: fullPropertyAccessStub{},
		}

		ctx := context.Background()
		knID := "kn1"
		branch := "main"
		objectTypeID := "ot1"

		Convey("失败 - 对象类不存在", func() {
			query := &interfaces.ObjectQueryBaseOnObjectType{
				KNID:         knID,
				Branch:       branch,
				ObjectTypeID: objectTypeID,
			}

			omAccess.EXPECT().GetObjectType(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(interfaces.ObjectType{}, false, nil)

			result, err := service.GetObjectsByObjectTypeID(ctx, query)
			So(err, ShouldNotBeNil)
			httpErr, ok := err.(*rest.HTTPError)
			So(ok, ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusNotFound)
			So(result.Datas, ShouldBeNil)
		})

		Convey("失败 - 获取对象类错误", func() {
			query := &interfaces.ObjectQueryBaseOnObjectType{
				KNID:         knID,
				Branch:       branch,
				ObjectTypeID: objectTypeID,
			}

			omAccess.EXPECT().GetObjectType(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(interfaces.ObjectType{}, false, rest.NewHTTPError(ctx, http.StatusInternalServerError, oerrors.OntologyQuery_InternalError))

			result, err := service.GetObjectsByObjectTypeID(ctx, query)
			So(err, ShouldNotBeNil)
			So(result.Datas, ShouldBeNil)
		})

		Convey("失败 - 无效的排序字段", func() {
			objectType := interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID: objectTypeID,
					DataProperties: []cond.DataProperty{
						{Name: "prop1"},
					},
				},
			}

			query := &interfaces.ObjectQueryBaseOnObjectType{
				KNID:         knID,
				Branch:       branch,
				ObjectTypeID: objectTypeID,
				PageQuery: interfaces.PageQuery{
					Sort: []*interfaces.SortParams{
						{Field: "invalid_field"},
					},
				},
			}

			omAccess.EXPECT().GetObjectType(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(objectType, true, nil)

			result, err := service.GetObjectsByObjectTypeID(ctx, query)
			So(err, ShouldNotBeNil)
			httpErr, ok := err.(*rest.HTTPError)
			So(ok, ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
			So(result.Datas, ShouldBeNil)
		})

		Convey("失败 - 无效的属性", func() {
			objectType := interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID: objectTypeID,
					DataProperties: []cond.DataProperty{
						{Name: "prop1"},
					},
				},
			}

			query := &interfaces.ObjectQueryBaseOnObjectType{
				KNID:         knID,
				Branch:       branch,
				ObjectTypeID: objectTypeID,
				Properties:   []string{"invalid_prop"},
			}

			omAccess.EXPECT().GetObjectType(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(objectType, true, nil)

			result, err := service.GetObjectsByObjectTypeID(ctx, query)
			So(err, ShouldNotBeNil)
			httpErr, ok := err.(*rest.HTTPError)
			So(ok, ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
			So(result.Datas, ShouldBeNil)
		})

		Convey("成功 - resource 数据源且 IndexAvailable 时仍走 vega 而非索引", func() {
			objectType := interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID: objectTypeID,
					DataProperties: []cond.DataProperty{
						{
							Name: "prop1",
							MappedField: cond.Field{
								Name: "field1",
							},
						},
					},
					PrimaryKeys: []string{"id"},
					DisplayKey:  "prop1",
					DataSource: &interfaces.ResourceInfo{
						Type: interfaces.DATA_SOURCE_TYPE_RESOURCE,
						ID:   "res1",
					},
				},
				Status: &interfaces.ObjectTypeStatus{
					IndexAvailable: true,
					Index:          "should_not_query",
				},
			}

			query := &interfaces.ObjectQueryBaseOnObjectType{
				KNID:         knID,
				Branch:       branch,
				ObjectTypeID: objectTypeID,
				PageQuery: interfaces.PageQuery{
					Limit:     10,
					NeedTotal: false,
				},
			}

			service.vba = &vegaStubForOTQuery{
				resp: &interfaces.DatasetQueryResponse{
					TotalCount: 1,
					Entries: []map[string]any{
						{"field1": "v1"},
					},
				},
			}

			omAccess.EXPECT().GetObjectType(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(objectType, true, nil)

			result, err := service.GetObjectsByObjectTypeID(ctx, query)
			So(err, ShouldBeNil)
			So(result.SearchFromIndex, ShouldBeFalse)
			So(len(result.Datas), ShouldEqual, 1)
		})

		Convey("成功 - resource 数据源 Sort 从属性名映射为资源列字段", func() {
			objectType := interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID: objectTypeID,
					DataProperties: []cond.DataProperty{
						{
							Name: "prop1",
							MappedField: cond.Field{
								Name: "field1",
							},
						},
					},
					PrimaryKeys: []string{"id"},
					DisplayKey:  "prop1",
					DataSource: &interfaces.ResourceInfo{
						Type: interfaces.DATA_SOURCE_TYPE_RESOURCE,
						ID:   "res1",
					},
				},
				Status: &interfaces.ObjectTypeStatus{
					IndexAvailable: true,
					Index:          "should_not_query",
				},
			}

			query := &interfaces.ObjectQueryBaseOnObjectType{
				KNID:         knID,
				Branch:       branch,
				ObjectTypeID: objectTypeID,
				PageQuery: interfaces.PageQuery{
					Limit:     10,
					NeedTotal: false,
					Sort: []*interfaces.SortParams{
						{Field: "prop1", Direction: interfaces.ASC_DIRECTION},
					},
				},
			}

			stub := &vegaStubForOTQuery{
				resp: &interfaces.DatasetQueryResponse{
					TotalCount: 1,
					Entries: []map[string]any{
						{"field1": "v1"},
					},
				},
			}
			service.vba = stub

			omAccess.EXPECT().GetObjectType(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(objectType, true, nil)

			result, err := service.GetObjectsByObjectTypeID(ctx, query)
			So(err, ShouldBeNil)
			So(stub.lastParams, ShouldNotBeNil)
			So(len(stub.lastParams.Sort), ShouldEqual, 1)
			So(stub.lastParams.Sort[0].Field, ShouldEqual, "field1")
			So(stub.lastParams.Sort[0].Direction, ShouldEqual, interfaces.ASC_DIRECTION)
			So(len(result.Datas), ShouldEqual, 1)
		})

		Convey("成功 - resource 数据源查询（原视图路径）", func() {
			objectType := interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID: objectTypeID,
					DataProperties: []cond.DataProperty{
						{
							Name: "prop1",
							MappedField: cond.Field{
								Name: "field1",
							},
						},
					},
					PrimaryKeys: []string{"id"},
					DataSource: &interfaces.ResourceInfo{
						Type: interfaces.DATA_SOURCE_TYPE_RESOURCE,
						ID:   "res1",
					},
				},
			}

			query := &interfaces.ObjectQueryBaseOnObjectType{
				KNID:         knID,
				Branch:       branch,
				ObjectTypeID: objectTypeID,
				PageQuery: interfaces.PageQuery{
					Limit: 10,
				},
			}

			service.vba = &vegaStubForOTQuery{
				resp: &interfaces.DatasetQueryResponse{
					TotalCount: 1,
					Entries: []map[string]any{
						{"field1": "value1", "id": "123", "prop1": "value1"},
					},
				},
			}

			omAccess.EXPECT().GetObjectType(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(objectType, true, nil)

			result, err := service.GetObjectsByObjectTypeID(ctx, query)
			So(err, ShouldBeNil)
			So(result.SearchFromIndex, ShouldBeFalse)
			So(len(result.Datas), ShouldEqual, 1)
		})

		Convey("失败 - 重写条件失败", func() {
			objectType := interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID: objectTypeID,
					DataProperties: []cond.DataProperty{
						{
							Name: "prop1",
							MappedField: cond.Field{
								Name: "field1",
							},
						},
					},
					PrimaryKeys: []string{"id"},
					DataSource: &interfaces.ResourceInfo{
						Type: interfaces.DATA_SOURCE_TYPE_RESOURCE,
						ID:   "res1",
					},
				},
			}

			query := &interfaces.ObjectQueryBaseOnObjectType{
				KNID:         knID,
				Branch:       branch,
				ObjectTypeID: objectTypeID,
				ActualCondition: &cond.CondCfg{
					Name:      "invalid_field",
					Operation: "==",
					ValueOptCfg: cond.ValueOptCfg{
						Value: "value1",
					},
				},
				PageQuery: interfaces.PageQuery{
					Limit: 10,
				},
			}

			service.vba = &vegaStubForOTQuery{
				resp: &interfaces.DatasetQueryResponse{
					TotalCount: 1,
					Entries: []map[string]any{
						{"field1": "value1", "id": "123", "prop1": "value1"},
					},
				},
			}

			omAccess.EXPECT().GetObjectType(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(objectType, true, nil)

			result, err := service.GetObjectsByObjectTypeID(ctx, query)
			So(err, ShouldNotBeNil)
			httpErr, ok := err.(*rest.HTTPError)
			So(ok, ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
			So(result.Datas, ShouldBeNil)
		})

		Convey("成功 - 包含逻辑属性", func() {
			objectType := interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID: objectTypeID,
					DataProperties: []cond.DataProperty{
						{
							Name: "prop1",
							MappedField: cond.Field{
								Name: "field1",
							},
						},
					},
					DataSource: &interfaces.ResourceInfo{
						Type: interfaces.DATA_SOURCE_TYPE_RESOURCE,
						ID:   "res1",
					},
					PrimaryKeys: []string{"id"},
					LogicProperties: []*interfaces.LogicProperty{
						{
							Name: "logic_prop1",
							Type: interfaces.LOGIC_PROPERTY_TYPE_METRIC,
							DataSource: &interfaces.ResourceInfo{
								Type: interfaces.LOGIC_PROPERTY_TYPE_METRIC,
								ID:   "metric1",
							},
							Parameters: []interfaces.Parameter{
								{
									Name:      "param1",
									ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_PROP,
									Value:     "prop1",
								},
							},
						},
					},
				},
			}

			query := &interfaces.ObjectQueryBaseOnObjectType{
				KNID:         knID,
				Branch:       branch,
				ObjectTypeID: objectTypeID,
				CommonQueryParameters: interfaces.CommonQueryParameters{
					IncludeLogicParams: true,
				},
				PageQuery: interfaces.PageQuery{
					Limit: 10,
				},
			}

			service.vba = &vegaStubForOTQuery{
				resp: &interfaces.DatasetQueryResponse{
					TotalCount: 1,
					Entries: []map[string]any{
						{"field1": "value1", "id": "123", "prop1": "value1"},
					},
				},
			}

			omAccess.EXPECT().GetObjectType(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(objectType, true, nil)

			result, err := service.GetObjectsByObjectTypeID(ctx, query)
			So(err, ShouldBeNil)
			So(len(result.Datas), ShouldEqual, 1)
			So(result.Datas[0]["logic_prop1"], ShouldNotBeNil)
		})

		Convey("失败 - 唯一标识缺少主键字段", func() {
			objectType := interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID: objectTypeID,
					DataProperties: []cond.DataProperty{
						{Name: "prop1"},
					},
					PrimaryKeys: []string{"id", "name"},
				},
			}

			query := &interfaces.ObjectQueryBaseOnObjectType{
				KNID:         knID,
				Branch:       branch,
				ObjectTypeID: objectTypeID,
				ObjectQueryInfo: &interfaces.ObjectQueryInfo{
					InstanceIdentity: []map[string]any{
						{"id": "123"}, // Missing name.
					},
				},
			}

			omAccess.EXPECT().GetObjectType(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(objectType, true, nil)

			result, err := service.GetObjectsByObjectTypeID(ctx, query)
			So(err, ShouldNotBeNil)
			httpErr, ok := err.(*rest.HTTPError)
			So(ok, ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
			So(result.Datas, ShouldBeNil)
		})

		Convey("失败 - 属性查询的属性不存在", func() {
			objectType := interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID: objectTypeID,
					DataProperties: []cond.DataProperty{
						{Name: "prop1"},
					},
					PrimaryKeys: []string{"id"},
				},
			}

			query := &interfaces.ObjectQueryBaseOnObjectType{
				KNID:         knID,
				Branch:       branch,
				ObjectTypeID: objectTypeID,
				ObjectQueryInfo: &interfaces.ObjectQueryInfo{
					InstanceIdentity: []map[string]any{
						{"id": "123"},
					},
					Properties: []string{"invalid_prop"},
				},
			}

			service.vba = &vegaStubForOTQuery{
				resp: &interfaces.DatasetQueryResponse{
					TotalCount: 1,
					Entries: []map[string]any{
						{"field1": "value1", "id": "123", "prop1": "value1"},
					},
				},
			}

			omAccess.EXPECT().GetObjectType(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(objectType, true, nil)

			result, err := service.GetObjectsByObjectTypeID(ctx, query)
			So(err, ShouldNotBeNil)
			httpErr, ok := err.(*rest.HTTPError)
			So(ok, ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
			So(result.Datas, ShouldBeNil)
		})

		Convey("成功 - IgnoringStore=true 时走 resource 数据源", func() {
			objectType := interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID: objectTypeID,
					DataProperties: []cond.DataProperty{
						{
							Name: "prop1",
							MappedField: cond.Field{
								Name: "field1",
							},
						},
					},
					PrimaryKeys: []string{"id"},
					DataSource: &interfaces.ResourceInfo{
						Type: interfaces.DATA_SOURCE_TYPE_RESOURCE,
						ID:   "res1",
					},
				},
				Status: &interfaces.ObjectTypeStatus{
					IndexAvailable: true,
					Index:          "index1",
				},
			}

			query := &interfaces.ObjectQueryBaseOnObjectType{
				KNID:         knID,
				Branch:       branch,
				ObjectTypeID: objectTypeID,
				CommonQueryParameters: interfaces.CommonQueryParameters{
					IgnoringStore: true,
				},
				PageQuery: interfaces.PageQuery{
					Limit: 10,
				},
			}

			service.vba = &vegaStubForOTQuery{
				resp: &interfaces.DatasetQueryResponse{
					TotalCount: 1,
					Entries: []map[string]any{
						{"field1": "value1", "id": "123", "prop1": "value1"},
					},
				},
			}

			omAccess.EXPECT().GetObjectType(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(objectType, true, nil)

			result, err := service.GetObjectsByObjectTypeID(ctx, query)
			So(err, ShouldBeNil)
			So(result.SearchFromIndex, ShouldBeFalse)
			So(len(result.Datas), ShouldEqual, 1)
		})

		Convey("失败 - 视图数据源为空", func() {
			objectType := interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID: objectTypeID,
					DataProperties: []cond.DataProperty{
						{
							Name: "prop1",
							MappedField: cond.Field{
								Name: "field1",
							},
						},
					},
					PrimaryKeys: []string{"id"},
					DataSource:  nil, // Data source is empty.
				},
			}

			query := &interfaces.ObjectQueryBaseOnObjectType{
				KNID:         knID,
				Branch:       branch,
				ObjectTypeID: objectTypeID,
				PageQuery: interfaces.PageQuery{
					Limit: 10,
				},
			}

			omAccess.EXPECT().GetObjectType(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(objectType, true, nil)

			result, err := service.GetObjectsByObjectTypeID(ctx, query)
			So(err, ShouldNotBeNil)
			httpErr, ok := err.(*rest.HTTPError)
			So(ok, ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
			So(result.Datas, ShouldBeNil)
		})

		Convey("失败 - 视图数据源ID为空", func() {
			objectType := interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID: objectTypeID,
					DataProperties: []cond.DataProperty{
						{
							Name: "prop1",
							MappedField: cond.Field{
								Name: "field1",
							},
						},
					},
					PrimaryKeys: []string{"id"},
					DataSource: &interfaces.ResourceInfo{
						ID: "", // IDempty.
					},
				},
			}

			query := &interfaces.ObjectQueryBaseOnObjectType{
				KNID:         knID,
				Branch:       branch,
				ObjectTypeID: objectTypeID,
				PageQuery: interfaces.PageQuery{
					Limit: 10,
				},
			}

			service.vba = &vegaStubForOTQuery{
				resp: &interfaces.DatasetQueryResponse{
					TotalCount: 1,
					Entries: []map[string]any{
						{"field1": "value1", "id": "123", "prop1": "value1"},
					},
				},
			}

			omAccess.EXPECT().GetObjectType(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(objectType, true, nil)

			result, err := service.GetObjectsByObjectTypeID(ctx, query)
			So(err, ShouldNotBeNil)
			httpErr, ok := err.(*rest.HTTPError)
			So(ok, ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
			So(result.Datas, ShouldBeNil)
		})

		Convey("成功 - IncludeTypeInfo=true", func() {
			objectType := interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID: objectTypeID,
					DataProperties: []cond.DataProperty{
						{
							Name: "prop1",
							MappedField: cond.Field{
								Name: "field1",
							},
						},
					},
					PrimaryKeys: []string{"id"},
					DataSource: &interfaces.ResourceInfo{
						Type: interfaces.DATA_SOURCE_TYPE_RESOURCE,
						ID:   "res1",
					},
				},
			}

			query := &interfaces.ObjectQueryBaseOnObjectType{
				KNID:         knID,
				Branch:       branch,
				ObjectTypeID: objectTypeID,
				CommonQueryParameters: interfaces.CommonQueryParameters{
					IncludeTypeInfo: true,
				},
				PageQuery: interfaces.PageQuery{
					Limit: 10,
				},
			}

			service.vba = &vegaStubForOTQuery{
				resp: &interfaces.DatasetQueryResponse{
					TotalCount: 1,
					Entries: []map[string]any{
						{"field1": "value1", "id": "123", "prop1": "value1"},
					},
				},
			}

			omAccess.EXPECT().GetObjectType(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(objectType, true, nil)

			result, err := service.GetObjectsByObjectTypeID(ctx, query)
			So(err, ShouldBeNil)
			So(result.ObjectType, ShouldNotBeNil)
			So(result.ObjectType.OTID, ShouldEqual, objectTypeID)
		})

		Convey("成功 - 包含LOGIC_PARAMS_VALUE_FROM_CONST的逻辑属性", func() {
			objectType := interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID: objectTypeID,
					DataProperties: []cond.DataProperty{
						{
							Name: "prop1",
							MappedField: cond.Field{
								Name: "field1",
							},
						},
					},
					DataSource: &interfaces.ResourceInfo{
						Type: interfaces.DATA_SOURCE_TYPE_RESOURCE,
						ID:   "res1",
					},
					PrimaryKeys: []string{"id"},
					LogicProperties: []*interfaces.LogicProperty{
						{
							Name: "logic_prop1",
							Type: interfaces.LOGIC_PROPERTY_TYPE_METRIC,
							DataSource: &interfaces.ResourceInfo{
								ID: "metric1",
							},
							Parameters: []interfaces.Parameter{
								{
									Name:      "param1",
									ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_CONST,
									Value:     "const_value",
								},
							},
						},
					},
				},
			}

			query := &interfaces.ObjectQueryBaseOnObjectType{
				KNID:         knID,
				Branch:       branch,
				ObjectTypeID: objectTypeID,
				CommonQueryParameters: interfaces.CommonQueryParameters{
					IncludeLogicParams: true,
				},
				PageQuery: interfaces.PageQuery{
					Limit: 10,
				},
			}

			service.vba = &vegaStubForOTQuery{
				resp: &interfaces.DatasetQueryResponse{
					TotalCount: 1,
					Entries: []map[string]any{
						{"field1": "value1", "id": "123", "prop1": "value1"},
					},
				},
			}

			omAccess.EXPECT().GetObjectType(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(objectType, true, nil)

			result, err := service.GetObjectsByObjectTypeID(ctx, query)
			So(err, ShouldBeNil)
			So(len(result.Datas), ShouldEqual, 1)
			So(result.Datas[0]["logic_prop1"], ShouldNotBeNil)
		})

		Convey("成功 - 包含LOGIC_PARAMS_VALUE_FROM_INPUT的逻辑属性", func() {
			objectType := interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID: objectTypeID,
					DataProperties: []cond.DataProperty{
						{
							Name: "prop1",
							MappedField: cond.Field{
								Name: "field1",
							},
						},
					},
					DataSource: &interfaces.ResourceInfo{
						Type: interfaces.DATA_SOURCE_TYPE_RESOURCE,
						ID:   "res1",
					},
					PrimaryKeys: []string{"id"},
					LogicProperties: []*interfaces.LogicProperty{
						{
							Name: "logic_prop1",
							Type: interfaces.LOGIC_PROPERTY_TYPE_METRIC,
							DataSource: &interfaces.ResourceInfo{
								ID: "metric1",
							},
							Parameters: []interfaces.Parameter{
								{
									Name:      "param1",
									ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_INPUT,
								},
							},
						},
					},
				},
			}

			query := &interfaces.ObjectQueryBaseOnObjectType{
				KNID:         knID,
				Branch:       branch,
				ObjectTypeID: objectTypeID,
				CommonQueryParameters: interfaces.CommonQueryParameters{
					IncludeLogicParams: true,
				},
				PageQuery: interfaces.PageQuery{
					Limit: 10,
				},
			}

			service.vba = &vegaStubForOTQuery{
				resp: &interfaces.DatasetQueryResponse{
					TotalCount: 1,
					Entries: []map[string]any{
						{"field1": "value1", "id": "123", "prop1": "value1"},
					},
				},
			}

			omAccess.EXPECT().GetObjectType(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(objectType, true, nil)

			result, err := service.GetObjectsByObjectTypeID(ctx, query)
			So(err, ShouldBeNil)
			So(len(result.Datas), ShouldEqual, 1)
			So(result.Datas[0]["logic_prop1"], ShouldNotBeNil)
		})

		Convey("成功 - 包含工具逻辑属性", func() {
			objectType := interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID: objectTypeID,
					DataProperties: []cond.DataProperty{
						{
							Name: "prop1",
							MappedField: cond.Field{
								Name: "field1",
							},
						},
					},
					DataSource: &interfaces.ResourceInfo{
						Type: interfaces.DATA_SOURCE_TYPE_RESOURCE,
						ID:   "res1",
					},
					PrimaryKeys: []string{"id"},
					LogicProperties: []*interfaces.LogicProperty{
						{
							Name: "logic_prop1",
							Type: interfaces.LOGIC_PROPERTY_TYPE_TOOL,
							DataSource: &interfaces.ResourceInfo{
								BoxID:  "box1",
								ToolID: "tool1",
							},
							Parameters: []interfaces.Parameter{
								{
									Name:      "param1",
									ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_PROP,
									Value:     "prop1",
								},
							},
						},
					},
				},
			}

			query := &interfaces.ObjectQueryBaseOnObjectType{
				KNID:         knID,
				Branch:       branch,
				ObjectTypeID: objectTypeID,
				CommonQueryParameters: interfaces.CommonQueryParameters{
					IncludeLogicParams: true,
				},
				PageQuery: interfaces.PageQuery{
					Limit: 10,
				},
			}

			service.vba = &vegaStubForOTQuery{
				resp: &interfaces.DatasetQueryResponse{
					TotalCount: 1,
					Entries: []map[string]any{
						{"field1": "value1", "id": "123", "prop1": "value1"},
					},
				},
			}

			omAccess.EXPECT().GetObjectType(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(objectType, true, nil)

			result, err := service.GetObjectsByObjectTypeID(ctx, query)
			So(err, ShouldBeNil)
			So(len(result.Datas), ShouldEqual, 1)
			So(result.Datas[0]["logic_prop1"], ShouldNotBeNil)
		})

		Convey("成功 - 不支持的逻辑属性类型", func() {
			objectType := interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID: objectTypeID,
					DataProperties: []cond.DataProperty{
						{
							Name: "prop1",
							MappedField: cond.Field{
								Name: "field1",
							},
						},
					},
					DataSource: &interfaces.ResourceInfo{
						Type: interfaces.DATA_SOURCE_TYPE_RESOURCE,
						ID:   "res1",
					},
					PrimaryKeys: []string{"id"},
					LogicProperties: []*interfaces.LogicProperty{
						{
							Name: "logic_prop1",
							Type: "unsupported_type",
							DataSource: &interfaces.ResourceInfo{
								ID: "resource1",
							},
						},
					},
				},
			}

			query := &interfaces.ObjectQueryBaseOnObjectType{
				KNID:         knID,
				Branch:       branch,
				ObjectTypeID: objectTypeID,
				CommonQueryParameters: interfaces.CommonQueryParameters{
					IncludeLogicParams: true,
				},
				PageQuery: interfaces.PageQuery{
					Limit: 10,
				},
			}

			service.vba = &vegaStubForOTQuery{
				resp: &interfaces.DatasetQueryResponse{
					TotalCount: 1,
					Entries: []map[string]any{
						{"field1": "value1", "id": "123", "prop1": "value1"},
					},
				},
			}

			omAccess.EXPECT().GetObjectType(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(objectType, true, nil)

			result, err := service.GetObjectsByObjectTypeID(ctx, query)
			So(err, ShouldBeNil)
			So(len(result.Datas), ShouldEqual, 1)
			// Unsupported logical property types are not added to the result.
		})

		Convey("vega 返回 4xx 时保留状态码但不泄漏底层详情", func() {
			objectType := interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:        objectTypeID,
					PrimaryKeys: []string{"id"},
					DataProperties: []cond.DataProperty{
						{Name: "prop1", MappedField: cond.Field{Name: "field1"}},
					},
					DataSource: &interfaces.ResourceInfo{
						Type: interfaces.DATA_SOURCE_TYPE_RESOURCE,
						ID:   "res1",
					},
				},
			}

			query := &interfaces.ObjectQueryBaseOnObjectType{
				KNID:         knID,
				Branch:       branch,
				ObjectTypeID: objectTypeID,
				PageQuery:    interfaces.PageQuery{Limit: 10},
			}

			details := "operation match is not supported by the sql query channel; " +
				"full-text operations need a local index on the resource"
			service.vba = &vegaStubForOTQuery{
				err: interfaces.NewVegaDownstreamError(http.StatusBadRequest,
					`{"error_code":"VegaBackend.Query.InvalidParameter","description":"查询参数错误",`+
						`"error_details":"`+details+`"}`),
			}

			omAccess.EXPECT().GetObjectType(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(objectType, true, nil)

			_, err := service.GetObjectsByObjectTypeID(ctx, query)
			So(err, ShouldNotBeNil)

			httpErr, ok := err.(*rest.HTTPError)
			So(ok, ShouldBeTrue)
			// Reporting parameter issues as 500 makes callers check service health, while the real fix is changing the query or building the index.
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
			So(httpErr.BaseError.ErrorDetails, ShouldBeEmpty)
		})

		Convey("vega 返回 5xx 时仍认定为依赖故障", func() {
			objectType := interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID:        objectTypeID,
					PrimaryKeys: []string{"id"},
					DataProperties: []cond.DataProperty{
						{Name: "prop1", MappedField: cond.Field{Name: "field1"}},
					},
					DataSource: &interfaces.ResourceInfo{
						Type: interfaces.DATA_SOURCE_TYPE_RESOURCE,
						ID:   "res1",
					},
				},
			}

			query := &interfaces.ObjectQueryBaseOnObjectType{
				KNID:         knID,
				Branch:       branch,
				ObjectTypeID: objectTypeID,
				PageQuery:    interfaces.PageQuery{Limit: 10},
			}

			service.vba = &vegaStubForOTQuery{
				err: interfaces.NewVegaDownstreamError(http.StatusInternalServerError,
					`{"error_code":"VegaBackend.Resource.InternalError","description":"数据资源内部错误"}`),
			}

			omAccess.EXPECT().GetObjectType(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(objectType, true, nil)

			_, err := service.GetObjectsByObjectTypeID(ctx, query)
			So(err, ShouldNotBeNil)

			httpErr, ok := err.(*rest.HTTPError)
			So(ok, ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusInternalServerError)
		})
	})
}

func Test_objectTypeService_GetTotal(t *testing.T) {
	Convey("Test objectTypeService GetTotal", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		service := &objectTypeService{
			osa: omock.NewMockOpenSearchAccess(mockCtrl),
		}

		ctx := context.Background()
		index := "index1"
		dsl := map[string]any{
			"query": map[string]any{
				"match_all": map[string]any{},
			},
			"from": 0,
			"size": 10,
			"sort": []any{},
		}

		Convey("成功 - 获取总数", func() {
			mockOSA := service.osa.(*omock.MockOpenSearchAccess)
			mockOSA.EXPECT().Count(gomock.Any(), gomock.Any(), gomock.Any()).Return([]byte(`{"count":100}`), nil)

			result, err := service.GetTotal(ctx, index, dsl)
			So(err, ShouldBeNil)
			So(result, ShouldEqual, 100)
		})

		Convey("失败 - Count错误", func() {
			mockOSA := service.osa.(*omock.MockOpenSearchAccess)
			mockOSA.EXPECT().Count(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, oerrors.OntologyQuery_InternalError))

			result, err := service.GetTotal(ctx, index, dsl)
			So(err, ShouldNotBeNil)
			So(result, ShouldEqual, 0)
		})

		Convey("失败 - 无效JSON", func() {
			mockOSA := service.osa.(*omock.MockOpenSearchAccess)
			mockOSA.EXPECT().Count(gomock.Any(), gomock.Any(), gomock.Any()).Return([]byte(`invalid json`), nil)

			result, err := service.GetTotal(ctx, index, dsl)
			So(err, ShouldNotBeNil)
			So(result, ShouldEqual, 0)
		})

		Convey("失败 - 获取count字段失败", func() {
			mockOSA := service.osa.(*omock.MockOpenSearchAccess)
			mockOSA.EXPECT().Count(gomock.Any(), gomock.Any(), gomock.Any()).Return([]byte(`{"total":100}`), nil)

			result, err := service.GetTotal(ctx, index, dsl)
			So(err, ShouldNotBeNil)
			So(result, ShouldEqual, 0)
		})

		Convey("失败 - 转换为int64失败", func() {
			mockOSA := service.osa.(*omock.MockOpenSearchAccess)
			mockOSA.EXPECT().Count(gomock.Any(), gomock.Any(), gomock.Any()).Return([]byte(`{"count":"not_a_number"}`), nil)

			result, err := service.GetTotal(ctx, index, dsl)
			So(err, ShouldNotBeNil)
			So(result, ShouldEqual, 0)
		})
	})
}

func Test_objectTypeService_GetObjectPropertyValue(t *testing.T) {
	Convey("Test objectTypeService GetObjectPropertyValue", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		appSetting := &common.AppSetting{}
		omAccess := omock.NewMockOntologyManagerAccess(mockCtrl)
		osa := omock.NewMockOpenSearchAccess(mockCtrl)
		vba := omock.NewMockVegaBackendAccess(mockCtrl)
		mqs := omock.NewMockMetricQueryService(mockCtrl)
		mfa := omock.NewMockModelFactoryAccess(mockCtrl)
		aoAccess := omock.NewMockAgentOperatorAccess(mockCtrl)

		logics.OMA = omAccess
		logics.OSA = osa
		logics.VBA = vba
		logics.MFA = mfa
		logics.AOA = aoAccess

		service := &objectTypeService{
			appSetting:     appSetting,
			omAccess:       omAccess,
			osa:            osa,
			vba:            vba,
			mqs:            mqs,
			mfa:            mfa,
			aoAccess:       aoAccess,
			proxy:          &objectTypeProxyResolverStub{},
			propertyAccess: fullPropertyAccessStub{},
		}

		ctx := context.Background()

		Convey("成功 - 执行工具箱逻辑属性", func() {
			logicProp := &interfaces.LogicProperty{
				Name: "logic_prop1",
				Type: interfaces.LOGIC_PROPERTY_TYPE_TOOL,
				DataSource: &interfaces.ResourceInfo{
					Type:   interfaces.LOGIC_PROPERTY_TYPE_TOOL,
					BoxID:  "box1",
					ToolID: "tool1",
				},
				Parameters: []interfaces.Parameter{
					{
						Name:      "payload.id",
						ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_INPUT,
						Source:    interfaces.PARAMETER_BODY,
					},
				},
			}
			toolValue := interfaces.ToolProperty{
				Parameters:    map[string]any{},
				DynamicParams: map[string]any{"payload": map[string]any{"id": "123"}},
			}
			aoAccess.EXPECT().ExecuteToolAsProxy(gomock.Any(), "box1", "tool1", gomock.Any()).DoAndReturn(
				func(_ context.Context, _, _ string, request interfaces.ToolExecutionRequest) (any, error) {
					So(request.Timeout, ShouldEqual, int64(300))
					So(request.Body, ShouldResemble, map[string]any{"payload": map[string]any{"id": "123"}})
					return map[string]any{"result": "success"}, nil
				})

			result, err := service.handleToolProperty(ctx, "kn1", "ot1", "logic_prop1", toolValue, logicProp,
				map[string]map[string]any{"logic_prop1": {"payload": map[string]any{"id": "123"}}})
			So(err, ShouldBeNil)
			So(result, ShouldResemble, map[string]any{"result": "success"})
		})

		Convey("失败 - 工具执行错误保留具体原因", func() {
			localizedCtx := rest.WithLanguage(ctx, rest.AmericanEnglish)
			logicProp := &interfaces.LogicProperty{
				Name: "logic_prop1",
				Type: interfaces.LOGIC_PROPERTY_TYPE_TOOL,
				DataSource: &interfaces.ResourceInfo{
					Type:   interfaces.LOGIC_PROPERTY_TYPE_TOOL,
					BoxID:  "box1",
					ToolID: "tool1",
				},
			}
			toolValue := interfaces.ToolProperty{Parameters: map[string]any{}}
			aoAccess.EXPECT().ExecuteToolAsProxy(gomock.Any(), "box1", "tool1", gomock.Any()).
				Return(nil, fmt.Errorf("tool failed"))

			result, err := service.handleToolProperty(localizedCtx, "kn1", "ot1", "logic_prop1", toolValue, logicProp, nil)
			So(result, ShouldBeNil)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			So(httpErr.BaseError.ErrorDetails, ShouldEqual,
				"Toolbox box1 tool tool1 failed while evaluating logic property logic_prop1: tool failed")
		})

		Convey("失败 - 指标动态参数错误保留具体原因", func() {
			localizedCtx := rest.WithLanguage(ctx, rest.SimplifiedChinese)
			dynamicParams := map[string]map[string]any{
				"metric_prop": {"invalid": make(chan int)},
			}

			_, err := service.handleMetricProperty(localizedCtx, "kn1", "main", "ot1", "metric_prop",
				interfaces.MetricProperty{}, &interfaces.LogicProperty{}, dynamicParams)
			So(err, ShouldNotBeNil)
			httpErr := err.(*rest.HTTPError)
			details := httpErr.BaseError.ErrorDetails.(string)
			So(details, ShouldStartWith, "解析属性 metric_prop 的动态参数失败：")
			So(details, ShouldContainSubstring, "chan")
		})

		Convey("成功 - 提取工具箱逻辑属性嵌套结果", func() {
			logicProp := &interfaces.LogicProperty{
				Name: "logic_prop1",
				Type: interfaces.LOGIC_PROPERTY_TYPE_TOOL,
				DataSource: &interfaces.ResourceInfo{
					Type:       interfaces.LOGIC_PROPERTY_TYPE_TOOL,
					BoxID:      "box1",
					ToolID:     "tool1",
					ResultPath: "$.data.result",
				},
			}
			toolValue := interfaces.ToolProperty{Parameters: map[string]any{}}
			aoAccess.EXPECT().ExecuteToolAsProxy(gomock.Any(), "box1", "tool1", gomock.Any()).
				Return(map[string]any{"data": map[string]any{"result": "success"}}, nil)

			result, err := service.handleToolProperty(ctx, "kn1", "ot1", "logic_prop1", toolValue, logicProp, nil)
			So(err, ShouldBeNil)
			So(result, ShouldEqual, "success")
		})

		Convey("成功 - 工具箱逻辑属性结果路径未命中时返回空值", func() {
			logicProp := &interfaces.LogicProperty{
				Name: "logic_prop1",
				Type: interfaces.LOGIC_PROPERTY_TYPE_TOOL,
				DataSource: &interfaces.ResourceInfo{
					Type:       interfaces.LOGIC_PROPERTY_TYPE_TOOL,
					BoxID:      "box1",
					ToolID:     "tool1",
					ResultPath: "$.data.result",
				},
			}
			toolValue := interfaces.ToolProperty{Parameters: map[string]any{}}
			aoAccess.EXPECT().ExecuteToolAsProxy(gomock.Any(), "box1", "tool1", gomock.Any()).
				Return(map[string]any{"data": map[string]any{}}, nil)

			result, err := service.handleToolProperty(ctx, "kn1", "ot1", "logic_prop1", toolValue, logicProp, nil)
			So(result, ShouldBeNil)
			So(err, ShouldBeNil)
		})

		Convey("成功 - 工具箱逻辑属性结果不是 JSON 时返回空值", func() {
			logicProp := &interfaces.LogicProperty{
				Name: "logic_prop1",
				Type: interfaces.LOGIC_PROPERTY_TYPE_TOOL,
				DataSource: &interfaces.ResourceInfo{
					Type:       interfaces.LOGIC_PROPERTY_TYPE_TOOL,
					BoxID:      "box1",
					ToolID:     "tool1",
					ResultPath: "$.data.result",
				},
			}
			toolValue := interfaces.ToolProperty{Parameters: map[string]any{}}
			aoAccess.EXPECT().ExecuteToolAsProxy(gomock.Any(), "box1", "tool1", gomock.Any()).
				Return("not-json", nil)

			result, err := service.handleToolProperty(ctx, "kn1", "ot1", "logic_prop1", toolValue, logicProp, nil)
			So(result, ShouldBeNil)
			So(err, ShouldBeNil)
		})

		Convey("成功 - 获取对象属性值", func() {
			query := &interfaces.ObjectPropertyValueQuery{
				KNID:         "kn1",
				Branch:       "main",
				ObjectTypeID: "ot1",
				InstanceIdentities: []map[string]any{
					{"id": "123"},
				},
				Properties: []string{"prop1"},
			}

			objectType := interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID: "ot1",
					DataProperties: []cond.DataProperty{
						{Name: "id", MappedField: cond.Field{Name: "id"}},
						{Name: "prop1", MappedField: cond.Field{Name: "prop1"}},
					},
					PrimaryKeys: []string{"id"},
					DataSource: &interfaces.ResourceInfo{
						Type: interfaces.DATA_SOURCE_TYPE_RESOURCE,
						ID:   "res1",
					},
				},
			}

			// GetObjectPropertyValue internally calls GetObjectsByObjectTypeID.
			// GetObjectsByObjectTypeID needs these dependencies.
			omAccess.EXPECT().GetObjectType(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(objectType, true, nil)
			vba.EXPECT().QueryResourceData(gomock.Any(), "res1", gomock.Any()).Return(&interfaces.DatasetQueryResponse{
				Entries: []map[string]any{{"id": "123", "prop1": "value1"}},
			}, nil)

			result, err := service.GetObjectPropertyValue(ctx, query)
			So(err, ShouldBeNil)
			So(len(result.Datas), ShouldEqual, 1)
			So(result.Datas[0]["prop1"], ShouldEqual, "value1")
			So(result.Datas[0]["id"], ShouldEqual, "123")
		})

		Convey("成功 - 包含逻辑属性", func() {
			query := &interfaces.ObjectPropertyValueQuery{
				KNID:         "kn1",
				Branch:       "main",
				ObjectTypeID: "ot1",
				InstanceIdentities: []map[string]any{
					{"id": "123"},
				},
				Properties: []string{"prop1", "logic_prop1"},
				DynamicParams: map[string]map[string]any{
					"logic_prop1": {
						"start":   1234567890,
						"end":     1234567890,
						"instant": true,
					},
				},
			}

			objectType := interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID: "ot1",
					DataProperties: []cond.DataProperty{
						{Name: "id", MappedField: cond.Field{Name: "id"}},
						{Name: "prop1", MappedField: cond.Field{Name: "prop1"}},
					},
					PrimaryKeys: []string{"id"},
					LogicProperties: []*interfaces.LogicProperty{
						{
							Name: "logic_prop1",
							Type: interfaces.LOGIC_PROPERTY_TYPE_METRIC,
							Parameters: []interfaces.Parameter{
								{
									Name:      "param1",
									ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_PROP,
									Value:     "prop1",
								},
							},
							DataSource: &interfaces.ResourceInfo{
								ID: "metric1",
							},
						},
					},
					DataSource: &interfaces.ResourceInfo{
						Type: interfaces.DATA_SOURCE_TYPE_RESOURCE,
						ID:   "res1",
					},
				},
			}

			omAccess.EXPECT().GetObjectType(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(objectType, true, nil).AnyTimes()
			vba.EXPECT().QueryResourceData(gomock.Any(), "res1", gomock.Any()).Return(&interfaces.DatasetQueryResponse{
				Entries: []map[string]any{
					{
						"id":    "123",
						"prop1": "value1",
						"logic_prop1": interfaces.MetricProperty{
							PropertyType:    interfaces.LOGIC_PROPERTY_TYPE_METRIC,
							MappingSourceId: "metric1",
							Parameters: interfaces.MetricFilters{
								Filters: []interfaces.Filter{
									{
										Name:      "param1",
										Operation: "==",
										Value:     "value1",
									},
								},
							},
							DynamicParams: map[string]any{},
						},
					},
				},
				TotalCount: 1,
			}, nil)
			omAccess.EXPECT().GetMetricDefinition(gomock.Any(), "kn1", "main", "metric1").Return(&interfaces.MetricDefinition{
				ID:       "metric1",
				ScopeRef: "ot1",
			}, true, nil)
			mqs.EXPECT().QueryMetricData(gomock.Any(), "kn1", "main", "metric1", gomock.Any()).Return(interfaces.MetricData{
				Datas: []interfaces.Data{
					{
						Labels: map[string]string{"param1": "value1"},
						Values: []interface{}{100},
						Times:  []interface{}{1234567890},
					},
				},
			}, nil)

			result, err := service.GetObjectPropertyValue(ctx, query)
			So(err, ShouldBeNil)
			So(len(result.Datas), ShouldEqual, 1)
			So(result.Datas[0]["prop1"], ShouldEqual, "value1")
		})

		Convey("失败 - 获取对象错误", func() {
			query := &interfaces.ObjectPropertyValueQuery{
				KNID:         "kn1",
				Branch:       "main",
				ObjectTypeID: "ot1",
				InstanceIdentities: []map[string]any{
					{"id": "123"},
				},
				Properties: []string{"prop1"},
			}

			omAccess.EXPECT().GetObjectType(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(interfaces.ObjectType{}, false, nil)

			result, err := service.GetObjectPropertyValue(ctx, query)
			So(err, ShouldNotBeNil)
			So(len(result.Datas), ShouldEqual, 0)
		})

		Convey("成功 - 包含逻辑属性且处理成功", func() {
			query := &interfaces.ObjectPropertyValueQuery{
				KNID:         "kn1",
				Branch:       "main",
				ObjectTypeID: "ot1",
				InstanceIdentities: []map[string]any{
					{"id": "123"},
				},
				Properties: []string{"logic_prop1"},
				DynamicParams: map[string]map[string]any{
					"logic_prop1": {
						"start":   int64(1234567890),
						"end":     int64(1234567890),
						"instant": true,
					},
				},
			}

			objectType := interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID: "ot1",
					DataProperties: []cond.DataProperty{
						{Name: "id", MappedField: cond.Field{Name: "id"}},
					},
					PrimaryKeys: []string{"id"},
					LogicProperties: []*interfaces.LogicProperty{
						{
							Name: "logic_prop1",
							Type: interfaces.LOGIC_PROPERTY_TYPE_METRIC,
							Parameters: []interfaces.Parameter{
								{
									Name:      "param1",
									ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_PROP,
									Value:     "id",
								},
							},
							DataSource: &interfaces.ResourceInfo{
								ID: "metric1",
							},
						},
					},
					DataSource: &interfaces.ResourceInfo{
						Type: interfaces.DATA_SOURCE_TYPE_RESOURCE,
						ID:   "res1",
					},
				},
			}

			omAccess.EXPECT().GetObjectType(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(objectType, true, nil).AnyTimes()
			vba.EXPECT().QueryResourceData(gomock.Any(), "res1", gomock.Any()).Return(&interfaces.DatasetQueryResponse{
				Entries: []map[string]any{
					{"id": "123"},
				},
				TotalCount: 1,
			}, nil)
			omAccess.EXPECT().GetMetricDefinition(gomock.Any(), "kn1", "main", "metric1").Return(&interfaces.MetricDefinition{
				ID:       "metric1",
				ScopeRef: "ot1",
			}, true, nil)
			mqs.EXPECT().QueryMetricData(gomock.Any(), "kn1", "main", "metric1", gomock.Any()).Return(interfaces.MetricData{
				Datas: []interfaces.Data{
					{
						Labels: map[string]string{"param1": "123"},
						Values: []interface{}{100},
						Times:  []interface{}{1234567890},
					},
				},
			}, nil)

			result, err := service.GetObjectPropertyValue(ctx, query)
			So(err, ShouldBeNil)
			So(len(result.Datas), ShouldEqual, 1)
		})

		Convey("成功 - 包含工具类型逻辑属性", func() {
			query := &interfaces.ObjectPropertyValueQuery{
				KNID:         "kn1",
				Branch:       "main",
				ObjectTypeID: "ot1",
				InstanceIdentities: []map[string]any{
					{"id": "123"},
				},
				Properties: []string{"logic_prop1"},
				DynamicParams: map[string]map[string]any{
					"logic_prop1": {
						"param1": "value1",
					},
				},
			}

			objectType := interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID: "ot1",
					DataProperties: []cond.DataProperty{
						{Name: "id", MappedField: cond.Field{Name: "id"}},
					},
					PrimaryKeys: []string{"id"},
					LogicProperties: []*interfaces.LogicProperty{
						{
							Name: "logic_prop1",
							Type: interfaces.LOGIC_PROPERTY_TYPE_TOOL,
							Parameters: []interfaces.Parameter{
								{
									Name:      "param1",
									ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_INPUT,
									Source:    interfaces.PARAMETER_BODY,
								},
							},
							DataSource: &interfaces.ResourceInfo{
								BoxID:  "box1",
								ToolID: "tool1",
							},
						},
					},
					DataSource: &interfaces.ResourceInfo{
						Type: interfaces.DATA_SOURCE_TYPE_RESOURCE,
						ID:   "res1",
					},
				},
			}

			omAccess.EXPECT().GetObjectType(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(objectType, true, nil)
			vba.EXPECT().QueryResourceData(gomock.Any(), "res1", gomock.Any()).Return(&interfaces.DatasetQueryResponse{
				Entries: []map[string]any{{"id": "123"}},
			}, nil)
			aoAccess.EXPECT().ExecuteToolAsProxy(gomock.Any(), "box1", "tool1", gomock.Any()).Return(map[string]any{"result": "success"}, nil)

			result, err := service.GetObjectPropertyValue(ctx, query)
			So(err, ShouldBeNil)
			So(len(result.Datas), ShouldEqual, 1)
		})

		Convey("失败 - 工具执行失败", func() {
			query := &interfaces.ObjectPropertyValueQuery{
				KNID:         "kn1",
				Branch:       "main",
				ObjectTypeID: "ot1",
				InstanceIdentities: []map[string]any{
					{"id": "123"},
				},
				Properties: []string{"logic_prop1"},
				DynamicParams: map[string]map[string]any{
					"logic_prop1": {
						"param1": "value1",
					},
				},
			}

			objectType := interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID: "ot1",
					DataProperties: []cond.DataProperty{
						{Name: "id", MappedField: cond.Field{Name: "id"}},
					},
					PrimaryKeys: []string{"id"},
					LogicProperties: []*interfaces.LogicProperty{
						{
							Name: "logic_prop1",
							Type: interfaces.LOGIC_PROPERTY_TYPE_TOOL,
							Parameters: []interfaces.Parameter{
								{
									Name:      "param1",
									ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_INPUT,
									Source:    interfaces.PARAMETER_BODY,
								},
							},
							DataSource: &interfaces.ResourceInfo{
								BoxID:  "box1",
								ToolID: "tool1",
							},
						},
					},
					DataSource: &interfaces.ResourceInfo{
						Type: interfaces.DATA_SOURCE_TYPE_RESOURCE,
						ID:   "res1",
					},
				},
			}

			omAccess.EXPECT().GetObjectType(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(objectType, true, nil)
			vba.EXPECT().QueryResourceData(gomock.Any(), "res1", gomock.Any()).Return(&interfaces.DatasetQueryResponse{
				Entries: []map[string]any{{"id": "123"}},
			}, nil)
			aoAccess.EXPECT().ExecuteToolAsProxy(gomock.Any(), "box1", "tool1", gomock.Any()).Return(nil, fmt.Errorf("tool failed"))

			result, err := service.GetObjectPropertyValue(ctx, query)
			So(err, ShouldNotBeNil)
			So(len(result.Datas), ShouldEqual, 0)
		})

		Convey("失败 - 工具缺少动态参数", func() {
			query := &interfaces.ObjectPropertyValueQuery{
				KNID:         "kn1",
				Branch:       "main",
				ObjectTypeID: "ot1",
				InstanceIdentities: []map[string]any{
					{"id": "123"},
				},
				Properties:    []string{"logic_prop1"},
				DynamicParams: map[string]map[string]any{}, // Missing dynamic parameters.
			}

			objectType := interfaces.ObjectType{
				ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
					OTID: "ot1",
					DataProperties: []cond.DataProperty{
						{Name: "id", MappedField: cond.Field{Name: "id"}},
					},
					PrimaryKeys: []string{"id"},
					LogicProperties: []*interfaces.LogicProperty{
						{
							Name: "logic_prop1",
							Type: interfaces.LOGIC_PROPERTY_TYPE_TOOL,
							Parameters: []interfaces.Parameter{
								{
									Name:      "param1",
									ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_INPUT,
									Source:    interfaces.PARAMETER_BODY,
								},
							},
							DataSource: &interfaces.ResourceInfo{
								BoxID:  "box1",
								ToolID: "tool1",
							},
						},
					},
					DataSource: &interfaces.ResourceInfo{
						Type: interfaces.DATA_SOURCE_TYPE_RESOURCE,
						ID:   "res1",
					},
				},
			}

			omAccess.EXPECT().GetObjectType(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(objectType, true, nil)
			vba.EXPECT().QueryResourceData(gomock.Any(), "res1", gomock.Any()).Return(&interfaces.DatasetQueryResponse{
				Entries: []map[string]any{{"id": "123"}},
			}, nil)

			result, err := service.GetObjectPropertyValue(ctx, query)
			So(err, ShouldNotBeNil)
			So(len(result.Datas), ShouldEqual, 0)
		})
	})
}

func Test_getNestedValue(t *testing.T) {
	Convey("Test getNestedValue", t, func() {
		Convey("成功 - 简单字段", func() {
			data := map[string]any{
				"key1": "value1",
			}
			result := getNestedValue(data, "key1")
			So(result, ShouldEqual, "value1")
		})

		Convey("成功 - 嵌套字段", func() {
			data := map[string]any{
				"level1": map[string]any{
					"level2": "value",
				},
			}
			result := getNestedValue(data, "level1.level2")
			So(result, ShouldEqual, "value")
		})

		Convey("成功 - data为nil", func() {
			result := getNestedValue(nil, "key1")
			So(result, ShouldBeNil)
		})

		Convey("成功 - 字段不存在", func() {
			data := map[string]any{}
			result := getNestedValue(data, "nonexistent")
			So(result, ShouldBeNil)
		})

		Convey("成功 - 嵌套路径不存在", func() {
			data := map[string]any{
				"level1": "not_a_map",
			}
			result := getNestedValue(data, "level1.level2")
			So(result, ShouldBeNil)
		})
	})
}

func Test_setNestedValue(t *testing.T) {
	Convey("Test setNestedValue", t, func() {
		Convey("成功 - 简单字段", func() {
			target := make(map[string]any)
			setNestedValue(target, "key1", "value1")
			So(target["key1"], ShouldEqual, "value1")
		})

		Convey("成功 - 嵌套字段", func() {
			target := make(map[string]any)
			setNestedValue(target, "level1.level2", "value")
			So(target["level1"], ShouldNotBeNil)
			level1, ok := target["level1"].(map[string]any)
			So(ok, ShouldBeTrue)
			So(level1["level2"], ShouldEqual, "value")
		})

		Convey("成功 - value为nil", func() {
			target := make(map[string]any)
			setNestedValue(target, "key1", nil)
			_, exists := target["key1"]
			So(exists, ShouldBeFalse)
		})

		Convey("成功 - 深层嵌套", func() {
			target := make(map[string]any)
			setNestedValue(target, "a.b.c", "value")
			a, _ := target["a"].(map[string]any)
			b, _ := a["b"].(map[string]any)
			So(b["c"], ShouldEqual, "value")
		})
	})
}

func Test_generateExecRequest(t *testing.T) {
	Convey("Test generateExecRequest", t, func() {
		Convey("成功 - Header参数", func() {
			configParams := []interfaces.Parameter{
				{
					Name:      "header_param",
					Source:    interfaces.PARAMETER_HEADER,
					ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_CONST,
				},
			}
			parameters := map[string]any{
				"header_param": "header_value",
			}
			dynamicParams := map[string]any{}

			result := generateToolExecutionRequest(configParams, parameters, dynamicParams)
			So(result.Header["header_param"], ShouldEqual, "header_value")
		})

		Convey("成功 - Query参数", func() {
			configParams := []interfaces.Parameter{
				{
					Name:      "query_param",
					Source:    interfaces.PARAMETER_QUERY,
					ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_CONST,
				},
			}
			parameters := map[string]any{
				"query_param": "query_value",
			}
			dynamicParams := map[string]any{}

			result := generateToolExecutionRequest(configParams, parameters, dynamicParams)
			So(result.Query["query_param"], ShouldEqual, "query_value")
		})

		Convey("成功 - Body参数", func() {
			configParams := []interfaces.Parameter{
				{
					Name:      "body_param",
					Source:    interfaces.PARAMETER_BODY,
					ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_CONST,
				},
			}
			parameters := map[string]any{
				"body_param": "body_value",
			}
			dynamicParams := map[string]any{}

			result := generateToolExecutionRequest(configParams, parameters, dynamicParams)
			So(result.Body["body_param"], ShouldEqual, "body_value")
		})

		Convey("成功 - Path参数", func() {
			configParams := []interfaces.Parameter{
				{
					Name:      "path_param",
					Source:    interfaces.PARAMETER_PATH,
					ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_CONST,
				},
			}
			parameters := map[string]any{
				"path_param": "path_value",
			}
			dynamicParams := map[string]any{}

			result := generateToolExecutionRequest(configParams, parameters, dynamicParams)
			So(result.Path["path_param"], ShouldEqual, "path_value")
		})

		Convey("成功 - 动态参数", func() {
			configParams := []interfaces.Parameter{
				{
					Name:      "dynamic_param",
					Source:    interfaces.PARAMETER_BODY,
					ValueFrom: interfaces.VALUE_FROM_INPUT,
				},
			}
			parameters := map[string]any{}
			dynamicParams := map[string]any{
				"dynamic_param": "dynamic_value",
			}

			result := generateToolExecutionRequest(configParams, parameters, dynamicParams)
			So(result.Body["dynamic_param"], ShouldEqual, "dynamic_value")
		})

		Convey("成功 - 嵌套参数", func() {
			configParams := []interfaces.Parameter{
				{
					Name:      "nested.param",
					Source:    interfaces.PARAMETER_BODY,
					ValueFrom: interfaces.LOGIC_PARAMS_VALUE_FROM_CONST,
				},
			}
			parameters := map[string]any{
				"nested": map[string]any{
					"param": "nested_value",
				},
			}
			dynamicParams := map[string]any{}

			result := generateToolExecutionRequest(configParams, parameters, dynamicParams)
			nested, ok := result.Body["nested"].(map[string]any)
			So(ok, ShouldBeTrue)
			So(nested["param"], ShouldEqual, "nested_value")
		})
	})
}

func TestObjectTypeProxyFailureStopsVegaRead(t *testing.T) {
	ctrl := gomock.NewController(t)
	models := omock.NewMockOntologyManagerAccess(ctrl)
	vega := omock.NewMockVegaBackendAccess(ctrl)
	proxyErr := errors.New("proxy unavailable")
	service := &objectTypeService{
		omAccess:       models,
		vba:            vega,
		proxy:          &objectTypeProxyResolverStub{err: proxyErr},
		propertyAccess: fullPropertyAccessStub{},
	}
	models.EXPECT().GetObjectType(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, "ot-1").Return(
		interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
			OTID:           "ot-1",
			DataProperties: []cond.DataProperty{{Name: "id", MappedField: cond.Field{Name: "id"}}},
			DataSource: &interfaces.ResourceInfo{
				Type: interfaces.DATA_SOURCE_TYPE_RESOURCE,
				ID:   "resource-1",
			},
		}}, true, nil)

	_, err := service.GetObjectsByObjectTypeID(context.Background(), &interfaces.ObjectQueryBaseOnObjectType{
		KNID: "kn-1", Branch: interfaces.MAIN_BRANCH, ObjectTypeID: "ot-1",
	})
	if !errors.Is(err, proxyErr) {
		t.Fatalf("GetObjectsByObjectTypeID() error = %v, want proxy failure", err)
	}
}

// vegaStubForOTQuery implements interfaces.VegaBackendAccess for tests.
type vegaStubForOTQuery struct {
	resp       *interfaces.DatasetQueryResponse
	err        error
	lastParams *interfaces.ResourceDataQueryParams
}

func (v *vegaStubForOTQuery) QueryResourceData(ctx context.Context, resourceID string, params *interfaces.ResourceDataQueryParams) (*interfaces.DatasetQueryResponse, error) {
	v.lastParams = params
	if v.err != nil {
		return nil, v.err
	}
	return v.resp, nil
}

func (v *vegaStubForOTQuery) GetResourceSchema(context.Context, string) (*interfaces.ResourceSchemaResponse, error) {
	return &interfaces.ResourceSchemaResponse{SchemaDefinition: []map[string]any{}}, nil
}
