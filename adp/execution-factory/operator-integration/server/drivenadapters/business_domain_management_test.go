package drivenadapters

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	infraErr "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

// createTestClient creates a business domain management client for testing.
func createTestClient(ctrl *gomock.Controller) (*businessDomainManagementClient, *mocks.MockLogger, *mocks.MockHTTPClient) {
	logger := mocks.NewMockLogger(ctrl)
	httpClient := mocks.NewMockHTTPClient(ctrl)

	client := &businessDomainManagementClient{
		baseURL:    "http://localhost:8080/internal/api/business-system/v1",
		logger:     logger,
		httpClient: httpClient,
	}

	return client, logger, httpClient
}

// TestAssociateResource test resource is associated with the business domain.
func TestAssociateResource(t *testing.T) {
	Convey("TestAssociateResource", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		client, logger, httpClient := createTestClient(ctrl)

		req := &interfaces.BusinessDomainResourceAssociateRequest{
			ID:   "test-resource-id",
			BDID: "test-domain-id",
			Type: "operator",
		}

		Convey("正常关联成功", func() {
			httpClient.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(http.StatusOK, nil, nil)
			logger.EXPECT().Infof(gomock.Any(), gomock.Any(), gomock.Any()).Return()

			err := client.AssociateResource(context.Background(), req)
			So(err, ShouldBeNil)
		})

		Convey("403 权限不足错误", func() {
			httpClient.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(http.StatusForbidden, nil, &rest.ExHTTPError{
					HTTPCode: http.StatusForbidden,
					Body:     []byte(`{"message":"forbidden"}`),
				})
			logger.EXPECT().Errorf(gomock.Any(), gomock.Any(), gomock.Any()).Return()

			err := client.AssociateResource(context.Background(), req)

			So(err, ShouldNotBeNil)
			httpErr, ok := err.(*infraErr.HTTPError)
			So(ok, ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusForbidden)
		})

		Convey("409 资源已关联冲突错误", func() {
			httpClient.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(http.StatusConflict, nil, &rest.ExHTTPError{
					HTTPCode: http.StatusConflict,
					Body:     []byte(`{"code":3,"message":"resource already connected to business domain","cause":"Conflict"}`),
				})
			logger.EXPECT().Warnf(gomock.Any(), gomock.Any(), gomock.Any()).Return()

			err := client.AssociateResource(context.Background(), req)

			So(err, ShouldNotBeNil)
			httpErr, ok := err.(*infraErr.HTTPError)
			So(ok, ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusConflict)
		})

		Convey("其他错误返回500", func() {
			httpClient.EXPECT().Post(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(http.StatusInternalServerError, nil, fmt.Errorf("internal error"))
			logger.EXPECT().Errorf(gomock.Any(), gomock.Any(), gomock.Any()).Return()

			err := client.AssociateResource(context.Background(), req)

			So(err, ShouldNotBeNil)
			httpErr, ok := err.(*infraErr.HTTPError)
			So(ok, ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusInternalServerError)
		})
	})
}

// TestDisassociateResource tests to disassociate resources from business domains.
func TestDisassociateResource(t *testing.T) {
	Convey("TestDisassociateResource", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		client, logger, httpClient := createTestClient(ctrl)

		req := &interfaces.BusinessDomainResourceDisassociateRequest{
			ID:   "test-resource-id",
			BDID: "test-domain-id",
			Type: "operator",
		}

		Convey("正常取消关联成功", func() {
			httpClient.EXPECT().Delete(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(http.StatusOK, nil, nil)
			logger.EXPECT().Infof(gomock.Any(), gomock.Any(), gomock.Any()).Return()

			err := client.DisassociateResource(context.Background(), req)
			So(err, ShouldBeNil)
		})

		Convey("验证请求URL包含正确的查询参数", func() {
			expectedURL := "http://localhost:8080/internal/api/business-system/v1/resource?"
			httpClient.EXPECT().Delete(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(ctx context.Context, urlStr string, headers map[string]string) (int, interface{}, error) {
					// Parse URL and validate query parameters.
					parsedURL, err := url.Parse(urlStr)
					So(err, ShouldBeNil)
					So(parsedURL.Query().Get("id"), ShouldEqual, "test-resource-id")
					So(parsedURL.Query().Get("bd_id"), ShouldEqual, "test-domain-id")
					So(parsedURL.Query().Get("type"), ShouldEqual, "operator")
					return http.StatusOK, nil, nil
				})
			logger.EXPECT().Infof(gomock.Any(), gomock.Any(), gomock.Any()).Return()

			err := client.DisassociateResource(context.Background(), req)
			So(err, ShouldBeNil)
			_ = expectedURL // Avoid unused warnings.
		})

		Convey("403 权限不足错误", func() {
			httpClient.EXPECT().Delete(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(http.StatusForbidden, nil, &rest.ExHTTPError{
					HTTPCode: http.StatusForbidden,
					Body:     []byte(`{"message":"forbidden"}`),
				})
			logger.EXPECT().Errorf(gomock.Any(), gomock.Any(), gomock.Any()).Return()

			err := client.DisassociateResource(context.Background(), req)

			So(err, ShouldNotBeNil)
			httpErr, ok := err.(*infraErr.HTTPError)
			So(ok, ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusForbidden)
		})

		Convey("其他错误返回500", func() {
			httpClient.EXPECT().Delete(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(http.StatusInternalServerError, nil, fmt.Errorf("internal error"))
			logger.EXPECT().Errorf(gomock.Any(), gomock.Any(), gomock.Any()).Return()

			err := client.DisassociateResource(context.Background(), req)

			So(err, ShouldNotBeNil)
			httpErr, ok := err.(*infraErr.HTTPError)
			So(ok, ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusInternalServerError)
		})
	})
}

// TestResourceList tests the resource list under the query business domain.
func TestResourceList(t *testing.T) {
	Convey("TestResourceList", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		client, logger, httpClient := createTestClient(ctrl)

		Convey("正常查询成功", func() {
			req := &interfaces.BusinessDomainResourceListRequest{
				BDID:   "test-domain-id",
				ID:     "",
				Type:   "operator",
				Limit:  10,
				Offset: 0,
			}

			// Simulate the resource list data returned.
			mockResponse := map[string]interface{}{
				"total": float64(2),
				"items": []interface{}{
					map[string]interface{}{
						"id":    "resource-1",
						"type":  "operator",
						"bd_id": "test-domain-id",
					},
					map[string]interface{}{
						"id":    "resource-2",
						"type":  "operator",
						"bd_id": "test-domain-id",
					},
				},
			}

			httpClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(http.StatusOK, mockResponse, nil)
			logger.EXPECT().Infof(gomock.Any(), gomock.Any(), gomock.Any()).Return()

			result, err := client.ResourceList(context.Background(), req)

			So(err, ShouldBeNil)
			So(result, ShouldNotBeNil)
			So(result.Total, ShouldEqual, 2)
		})

		Convey("验证请求包含正确的查询参数", func() {
			req := &interfaces.BusinessDomainResourceListRequest{
				BDID:   "test-domain-id",
				ID:     "specific-id",
				Type:   "tool",
				Limit:  20,
				Offset: 10,
			}

			httpClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(ctx context.Context, urlStr string, queryParams url.Values, headers map[string]string) (int, interface{}, error) {
					// Validate query parameters.
					So(queryParams.Get("bd_id"), ShouldEqual, "test-domain-id")
					So(queryParams.Get("id"), ShouldEqual, "specific-id")
					So(queryParams.Get("type"), ShouldEqual, "tool")
					So(queryParams.Get("limit"), ShouldEqual, "20")
					So(queryParams.Get("offset"), ShouldEqual, "10")
					return http.StatusOK, map[string]interface{}{"total": float64(0), "items": []interface{}{}}, nil
				})
			logger.EXPECT().Infof(gomock.Any(), gomock.Any(), gomock.Any()).Return()

			_, err := client.ResourceList(context.Background(), req)
			So(err, ShouldBeNil)
		})

		Convey("offset为0时不传offset参数", func() {
			req := &interfaces.BusinessDomainResourceListRequest{
				BDID:   "test-domain-id",
				Limit:  10,
				Offset: 0,
			}

			httpClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(ctx context.Context, urlStr string, queryParams url.Values, headers map[string]string) (int, interface{}, error) {
					// The offset parameter should not be included when offset is 0.
					So(queryParams.Get("offset"), ShouldEqual, "")
					return http.StatusOK, map[string]interface{}{"total": float64(0), "items": []interface{}{}}, nil
				})
			logger.EXPECT().Infof(gomock.Any(), gomock.Any(), gomock.Any()).Return()

			_, err := client.ResourceList(context.Background(), req)
			So(err, ShouldBeNil)
		})

		Convey("403 权限不足错误", func() {
			req := &interfaces.BusinessDomainResourceListRequest{
				BDID:  "test-domain-id",
				Limit: 10,
			}

			httpClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(http.StatusForbidden, nil, &rest.ExHTTPError{
					HTTPCode: http.StatusForbidden,
					Body:     []byte(`{"message":"forbidden"}`),
				})
			logger.EXPECT().Errorf(gomock.Any(), gomock.Any(), gomock.Any()).Return()

			result, err := client.ResourceList(context.Background(), req)

			So(result, ShouldBeNil)
			So(err, ShouldNotBeNil)
			httpErr, ok := err.(*infraErr.HTTPError)
			So(ok, ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusForbidden)
		})

		Convey("其他HTTP错误返回500", func() {
			req := &interfaces.BusinessDomainResourceListRequest{
				BDID:  "test-domain-id",
				Limit: 10,
			}

			httpClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(http.StatusInternalServerError, nil, fmt.Errorf("internal error"))
			logger.EXPECT().Errorf(gomock.Any(), gomock.Any(), gomock.Any()).Return()

			result, err := client.ResourceList(context.Background(), req)

			So(result, ShouldBeNil)
			So(err, ShouldNotBeNil)
			httpErr, ok := err.(*infraErr.HTTPError)
			So(ok, ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusInternalServerError)
		})

		Convey("响应数据反序列化错误", func() {
			req := &interfaces.BusinessDomainResourceListRequest{
				BDID:  "test-domain-id",
				Limit: 10,
			}

			// Returns a data structure that cannot be deserialized correctly.
			invalidResponse := "invalid json structure"

			httpClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(http.StatusOK, invalidResponse, nil)
			logger.EXPECT().Errorf(gomock.Any(), gomock.Any()).Return()

			result, err := client.ResourceList(context.Background(), req)

			So(result, ShouldBeNil)
			So(err, ShouldNotBeNil)
		})
	})
}
