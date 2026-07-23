package drivenadapters

import (
	"context"
	"net/http"
	"testing"

	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/mocks"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

func TestVegaBackendClient(t *testing.T) {
	Convey("VegaBackendClient", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		httpClient := mocks.NewMockHTTPClient(ctrl)
		client := &vegaBackendClient{
			baseURL:    "http://vega-backend:9898/api/vega-backend",
			logger:     logger.DefaultLogger(),
			httpClient: httpClient,
		}
		ctx := common.SetAccountAuthContextToCtx(context.Background(), &interfaces.AccountAuthContext{
			AccountID:   "acc-1",
			AccountType: interfaces.AccessorTypeUser,
		})
		headers := map[string]string{
			"Content-Type":  "application/json",
			"x-account-id":   "acc-1",
			"x-account-type": "user",
		}

		Convey("creates catalog with fixed fields", func() {
			req := &interfaces.VegaCatalogRequest{
				ID:          "bkn_execution_factory_catalog",
				Name:        "bkn_execution_factory_catalog",
				Tags:        []string{"execution-factory", "索引"},
				Description: "执行工厂的逻辑命名空间",
			}
			httpClient.EXPECT().PostNoUnmarshal(gomock.Any(), "http://vega-backend:9898/api/vega-backend/v1/catalogs", headers, req).
				Return(http.StatusCreated, []byte(`{"id":"bkn_execution_factory_catalog","name":"bkn_execution_factory_catalog"}`), nil)

			resp, err := client.CreateCatalog(ctx, req)
			So(err, ShouldBeNil)
			So(resp, ShouldNotBeNil)
			So(resp.ID, ShouldEqual, "bkn_execution_factory_catalog")
		})

		Convey("gets catalog from entries response", func() {
			httpClient.EXPECT().GetNoUnmarshal(gomock.Any(), "http://vega-backend:9898/api/vega-backend/v1/catalogs/kweaver_execution_factory_catalog", gomock.Nil(), headers).
				Return(http.StatusOK, []byte(`{"entries":[{"id":"kweaver_execution_factory_catalog","name":"kweaver_execution_factory_catalog","enabled":false}]}`), nil)

			resp, err := client.GetCatalogByID(ctx, "kweaver_execution_factory_catalog")
			So(err, ShouldBeNil)
			So(resp, ShouldNotBeNil)
			So(resp.ID, ShouldEqual, "kweaver_execution_factory_catalog")
			So(resp.Name, ShouldEqual, "kweaver_execution_factory_catalog")
			So(resp.Enabled, ShouldBeFalse)
		})

		Convey("ignores entries that do not match the requested catalog id", func() {
			httpClient.EXPECT().GetNoUnmarshal(gomock.Any(), "http://vega-backend:9898/api/vega-backend/v1/catalogs/bkn_execution_factory_catalog", gomock.Nil(), headers).
				Return(http.StatusOK, []byte(`{"entries":[{"id":"some_other_catalog","name":"other"}]}`), nil)

			resp, err := client.GetCatalogByID(ctx, "bkn_execution_factory_catalog")
			So(err, ShouldBeNil)
			So(resp, ShouldBeNil)
		})

		Convey("treats an empty catalog payload as not found", func() {
			httpClient.EXPECT().GetNoUnmarshal(gomock.Any(), "http://vega-backend:9898/api/vega-backend/v1/catalogs/bkn_execution_factory_catalog", gomock.Nil(), headers).
				Return(http.StatusOK, []byte(`{"entries":[]}`), nil)

			resp, err := client.GetCatalogByID(ctx, "bkn_execution_factory_catalog")
			So(err, ShouldBeNil)
			So(resp, ShouldBeNil)
		})

		Convey("renames catalog in place via PUT", func() {
			req := &interfaces.VegaCatalogRequest{
				ID:       "kweaver_execution_factory_catalog",
				Name:     "bkn_execution_factory_catalog",
				Tags:     []string{"execution-factory", "索引"},
				Internal: true,
				Enabled:  true,
			}
			httpClient.EXPECT().PutNoUnmarshal(gomock.Any(), "http://vega-backend:9898/api/vega-backend/v1/catalogs/kweaver_execution_factory_catalog", headers, req).
				Return(http.StatusNoContent, []byte{}, nil)

			So(client.UpdateCatalog(ctx, req), ShouldBeNil)
		})

		Convey("enables catalog", func() {
			httpClient.EXPECT().PostNoUnmarshal(gomock.Any(), "http://vega-backend:9898/api/vega-backend/v1/catalogs/kweaver_execution_factory_catalog/enable", headers, nil).
				Return(http.StatusNoContent, []byte{}, nil)

			So(client.EnableCatalog(ctx, "kweaver_execution_factory_catalog"), ShouldBeNil)
		})

		Convey("renames resource without touching schema", func() {
			resource := &interfaces.VegaResource{
				ID:        "kweaver_execution_factory_skill_dataset",
				CatalogID: "kweaver_execution_factory_catalog",
				Name:      "kweaver_execution_factory_skill_dataset",
				Tags:      []string{"execution-factory", "skill"},
				// schema 不参与请求体：回填有损 schema 会被 vega 判成 schema 变更并清空 LocalIndexName
				SchemaDefinition: []interfaces.VegaProperty{{Name: "skill_id", Type: "string"}},
			}
			expectedPayload := map[string]any{
				"id":          "kweaver_execution_factory_skill_dataset",
				"catalog_id":  "kweaver_execution_factory_catalog",
				"name":        "bkn_execution_factory_skill_dataset",
				"tags":        []string{"execution-factory", "skill"},
				"description": "",
			}
			httpClient.EXPECT().PutNoUnmarshal(gomock.Any(), "http://vega-backend:9898/api/vega-backend/v1/resources/kweaver_execution_factory_skill_dataset", headers, expectedPayload).
				Return(http.StatusNoContent, []byte{}, nil)

			So(client.RenameResource(ctx, resource, "bkn_execution_factory_skill_dataset"), ShouldBeNil)
		})

		Convey("gets resource from entries response", func() {
			httpClient.EXPECT().GetNoUnmarshal(gomock.Any(), "http://vega-backend:9898/api/vega-backend/v1/resources/bkn_execution_factory_skill_dataset", gomock.Nil(), headers).
				Return(http.StatusOK, []byte(`{"entries":[{"id":"bkn_execution_factory_skill_dataset","catalog_id":"bkn_execution_factory_catalog"}]}`), nil)

			resp, err := client.GetResourceByID(ctx, "bkn_execution_factory_skill_dataset")
			So(err, ShouldBeNil)
			So(resp, ShouldNotBeNil)
			So(resp.ID, ShouldEqual, "bkn_execution_factory_skill_dataset")
		})

		Convey("writes dataset documents", func() {
			docs := []map[string]any{
				{"_id": "skill-1", "skill_id": "skill-1", "name": "demo"},
			}
			writeHeaders := map[string]string{
				"Content-Type":            "application/json",
				"x-account-id":           "acc-1",
				"x-account-type":         "user",
				"X-HTTP-Method-Override": "POST",
			}
			httpClient.EXPECT().PostNoUnmarshal(gomock.Any(), "http://vega-backend:9898/api/vega-backend/v1/resources/bkn_execution_factory_skill_dataset/data", writeHeaders, docs).
				Return(http.StatusCreated, []byte(`{}`), nil)

			err := client.WriteDatasetDocuments(ctx, "bkn_execution_factory_skill_dataset", docs)
			So(err, ShouldBeNil)
		})

		Convey("updates dataset documents", func() {
			docs := []map[string]any{
				{"_id": "skill-1", "skill_id": "skill-1", "name": "demo-updated"},
			}
			httpClient.EXPECT().PutNoUnmarshal(gomock.Any(), "http://vega-backend:9898/api/vega-backend/v1/resources/bkn_execution_factory_skill_dataset/data", headers, docs).
				Return(http.StatusOK, []byte(`{}`), nil)

			err := client.UpdateDatasetDocuments(ctx, "bkn_execution_factory_skill_dataset", docs)
			So(err, ShouldBeNil)
		})

		Convey("deletes dataset document by id", func() {
			httpClient.EXPECT().DeleteNoUnmarshal(gomock.Any(), "http://vega-backend:9898/api/vega-backend/v1/resources/bkn_execution_factory_skill_dataset/data/skill-1", headers).
				Return(http.StatusNoContent, []byte{}, nil)

			err := client.DeleteDatasetDocumentByID(ctx, "bkn_execution_factory_skill_dataset", "skill-1")
			So(err, ShouldBeNil)
		})
	})
}
