// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"bkn-backend-tests/integration_tests/bkn/helpers"
	"bkn-backend-tests/integration_tests/setup"
	"bkn-backend-tests/testutil"
)

// TestBKNImportExport runs BKN import/export integration tests.
// Test ID prefix: BKN1xx (Import/Export).
func TestBKNImportExport(t *testing.T) {

	Convey("BKN导入导出集成测试 - 初始化", t, func() {

		// Load test configuration.
		config, err := setup.LoadTestConfig()
		So(err, ShouldBeNil)
		So(config, ShouldNotBeNil)

		// Create the HTTP client.
		client := testutil.NewHTTPClient(config.BKNBackend.BaseURL)

		// Verify service availability.
		err = client.CheckHealth()
		So(err, ShouldBeNil)
		t.Logf("✓ 集成测试环境就绪，BKN Backend: %s", config.BKNBackend.BaseURL)

		// Clean up existing test knowledge networks.
		helpers.CleanupKNs(client, t)

		// ========== BKN import tests (BKN101-BKN103) ==========

		Convey("BKN101: 导入BKN - 使用 k8s-network 示例", func() {

			// Build a tar archive from the examples directory.
			tarData, err := helpers.BuildTarFromExamplesDir("k8s-network")
			So(err, ShouldBeNil)
			So(tarData, ShouldNotBeNil)
			So(len(tarData), ShouldBeGreaterThan, 0)

			// Upload the BKN file.
			resp := client.POSTMultipart(
				"/api/bkn-backend/v1/bkns",
				"file",
				tarData,
				"k8s-network.tar",
				nil,
			)

			So(resp.StatusCode, ShouldEqual, http.StatusOK)
			So(resp.Body, ShouldNotBeNil)
			So(resp.Body["kn_id"], ShouldEqual, "k8s-network")
		})

		Convey("BKN102: 导入BKN后验证对象类型、关系类型、行动类型已创建", func() {

			knID := "k8s-network"
			// Import the BKN; k8s-network contains object_types, relation_types, and action_types.
			tarData, _ := helpers.BuildTarFromExamplesDir("k8s-network")
			resp := client.POSTMultipart(
				"/api/bkn-backend/v1/bkns",
				"file",
				tarData,
				"k8s-network.tar",
				nil,
			)
			So(resp.StatusCode, ShouldEqual, http.StatusOK)

			// Verify object types were created.
			otEntries := helpers.VerifyObjectTypesExist(client, knID, t)
			So(len(otEntries), ShouldBeGreaterThan, 0)

			// Verify relation types were created.
			rtEntries := helpers.VerifyRelationTypesExist(client, knID, t)
			So(len(rtEntries), ShouldBeGreaterThan, 0)

			// Verify action types were created.
			atEntries := helpers.VerifyActionTypesExist(client, knID, t)
			So(len(atEntries), ShouldBeGreaterThan, 0)

			// Verify concept groups were created.
			cgEntries := helpers.VerifyConceptGroupsExist(client, knID, t)
			So(len(cgEntries), ShouldBeGreaterThan, 0)
		})

		Convey("BKN103: 导入含 metrics 的 k8s-network 并校验指标条数", func() {
			knID := "k8s-network"
			tarData, err := helpers.BuildTarFromExamplesDir("k8s-network")
			So(err, ShouldBeNil)
			resp := client.POSTMultipart(
				"/api/bkn-backend/v1/bkns",
				"file",
				tarData,
				"k8s-network.tar",
				nil,
			)
			So(resp.StatusCode, ShouldEqual, http.StatusOK)
			n := helpers.VerifyMetricsCountAtLeast(client, knID, t, 5)
			So(n, ShouldBeGreaterThanOrEqualTo, 5)
		})

		// ========== BKN export tests (BKN121-BKN122) ==========

		Convey("BKN121: 导出BKN - 基本场景", func() {
			knID := "k8s-network"

			// Import some data first.
			tarData, _ := helpers.BuildTarFromExamplesDir("k8s-network")
			client.POSTMultipart(
				"/api/bkn-backend/v1/bkns",
				"file",
				tarData,
				"k8s-network.tar",
				nil,
			)

			// Export the BKN.
			resp := client.GET("/api/bkn-backend/v1/bkns/" + knID)

			So(resp.StatusCode, ShouldEqual, http.StatusOK)
			So(resp.RawBody, ShouldNotBeNil)
			So(len(resp.RawBody), ShouldBeGreaterThan, 0)
			So(helpers.IsValidTar(resp.RawBody), ShouldBeTrue)
		})

		Convey("BKN122: 导出BKN - 验证Content-Disposition包含kn_id", func() {
			knID := "k8s-network"

			// Import data first.
			tarData, _ := helpers.BuildTarFromExamplesDir("k8s-network")
			client.POSTMultipart(
				"/api/bkn-backend/v1/bkns",
				"file",
				tarData,
				"k8s-network.tar",
				nil,
			)

			resp := client.GET("/api/bkn-backend/v1/bkns/" + knID)

			So(resp.StatusCode, ShouldEqual, http.StatusOK)

			// Verify the Content-Disposition header contains file download information.
			contentDisposition := resp.Headers.Get("Content-Disposition")
			So(contentDisposition, ShouldNotBeEmpty)
			So(strings.Contains(contentDisposition, knID), ShouldBeTrue)
		})

		Convey("BKN124: 导出 tar 含 metrics 条目（内容与示例一致）", func() {
			knID := "k8s-network"
			tarData, _ := helpers.BuildTarFromExamplesDir("k8s-network")
			client.POSTMultipart(
				"/api/bkn-backend/v1/bkns",
				"file",
				tarData,
				"k8s-network.tar",
				nil,
			)

			resp := client.GET("/api/bkn-backend/v1/bkns/" + knID)
			So(resp.StatusCode, ShouldEqual, http.StatusOK)
			So(bytes.Contains(resp.RawBody, []byte("metrics/")), ShouldBeTrue)
			So(bytes.Contains(resp.RawBody, []byte("pod_running_count")), ShouldBeTrue)
		})

		// ========== Negative tests (BKN201-BKN220) ==========

		Convey("BKN201: 导入无效文件格式", func() {

			// Upload a non-tar file.
			invalidData := []byte("this is not a tar file")
			resp := client.POSTMultipart(
				"/api/bkn-backend/v1/bkns",
				"file",
				invalidData,
				"invalid.txt",
				nil,
			)

			// An error should be returned.
			So(resp.StatusCode, ShouldBeGreaterThanOrEqualTo, 400)
		})

		Convey("BKN202: 导出不存在的知识网络", func() {
			// Try to export a non-existent KN.
			resp := client.GET("/api/bkn-backend/v1/bkns/non-existent-kn-id")

			// An error should be returned.
			So(resp.StatusCode, ShouldBeGreaterThanOrEqualTo, 400)
		})

		Convey("BKN203: 导入空文件", func() {

			// Upload an empty file.
			resp := client.POSTMultipart(
				"/api/bkn-backend/v1/bkns",
				"file",
				[]byte{},
				"empty.tar",
				nil,
			)

			// An error should be returned.
			So(resp.StatusCode, ShouldBeGreaterThanOrEqualTo, 400)
		})

		Convey("BKN204: 导入缺少network.bkn的tar包", func() {

			// Build a tar archive without network.bkn.
			tarData, err := helpers.BuildTarWithoutNetworkBKN()
			So(err, ShouldBeNil)

			resp := client.POSTMultipart(
				"/api/bkn-backend/v1/bkns",
				"file",
				tarData,
				"incomplete.tar",
				nil,
			)

			// An error should be returned.
			So(resp.StatusCode, ShouldBeGreaterThanOrEqualTo, 400)
		})

		Convey("BKN206: strict_mode=true 时 data_view 对象类上的指标严格校验失败", func() {
			tarData, err := helpers.BuildTarFromExamplesDir("k8s-network")
			So(err, ShouldBeNil)
			resp := client.POSTMultipart(
				"/api/bkn-backend/v1/bkns?strict_mode=true",
				"file",
				tarData,
				"k8s-network.tar",
				nil,
			)
			So(resp.StatusCode, ShouldBeGreaterThanOrEqualTo, 400)
		})

		// ========== Complex data test (BKN221) ==========

		Convey("BKN221: 导出包含复杂结构的BKN", func() {
			knID := "k8s-network"

			// Import a complex structure first; k8s-network contains objects, relations, actions, and other data.
			tarData, _ := helpers.BuildTarFromExamplesDir("k8s-network")
			client.POSTMultipart(
				"/api/bkn-backend/v1/bkns",
				"file",
				tarData,
				"k8s-network.tar",
				nil,
			)

			// Export.
			resp := client.GET("/api/bkn-backend/v1/bkns/" + knID)
			So(resp.StatusCode, ShouldEqual, http.StatusOK)

			// Verify the exported content contains all types.
			So(bytes.Contains(resp.RawBody, []byte("object_types")), ShouldBeTrue)
			So(bytes.Contains(resp.RawBody, []byte("relation_types")), ShouldBeTrue)
			So(bytes.Contains(resp.RawBody, []byte("action_types")), ShouldBeTrue)
		})
	})
}
