// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package capability

import (
	"context"
	"fmt"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
)

func skillRef(id string) interfaces.CapabilityRef {
	return interfaces.CapabilityRef{CapabilityType: interfaces.CapabilityTypeSkill, CapabilityID: id}
}

func functionRef(box, tool string) interfaces.CapabilityRef {
	return interfaces.CapabilityRef{
		CapabilityType: interfaces.CapabilityTypeFunction, OwnerID: box, CapabilityID: tool,
	}
}

func entryOf(ref interfaces.CapabilityRef, name string) map[string]any {
	return map[string]any{
		"capability_type": ref.CapabilityType,
		"owner_id":        ref.OwnerID,
		"capability_id":   ref.CapabilityID,
		"name":            name,
		"description":     name + " description",
	}
}

// embeddingOnce lets a search run its vector channel with a fixed query embedding.
func embeddingOnce(modelAPI *mocks.MockMFModelAPIClient) {
	modelAPI.EXPECT().Embeddings(gomock.Any(), gomock.Any()).Return(&interfaces.EmbeddingResp{
		Data: []interfaces.EmbeddingData{{Embedding: []float32{0.1, 0.2}}},
	}, nil).AnyTimes()
}

func newSearch(vega interfaces.VegaBackendClient, modelAPI interfaces.MFModelAPIClient) *capabilitySearchService {
	return &capabilitySearchService{
		logger:     logger.DefaultLogger(),
		vegaClient: vega,
		modelAPI:   modelAPI,
	}
}

// TestCapabilitySearchWhitelistIsFailClosed locks the rule that decides whether this endpoint is
// safe at all: no whitelist means no capability is in scope, never "do not filter".
func TestCapabilitySearchWhitelistIsFailClosed(t *testing.T) {
	Convey("白名单为空时不得回落到全平台", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		vega := mocks.NewMockVegaBackendClient(ctrl)
		modelAPI := mocks.NewMockMFModelAPIClient(ctrl)
		// No query is issued at all: the only safe answer is nothing.
		vega.EXPECT().QueryDatasetData(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

		service := newSearch(vega, modelAPI)

		Convey("refs 为 nil", func() {
			resp, err := service.SearchCapabilities(context.Background(), &interfaces.SearchCapabilitiesReq{Query: "汇率"})
			So(err, ShouldBeNil)
			So(resp.Entries, ShouldBeEmpty)
		})

		Convey("refs 全部非法", func() {
			resp, err := service.SearchCapabilities(context.Background(), &interfaces.SearchCapabilitiesReq{
				Query: "汇率",
				Refs: []interfaces.CapabilityRef{
					{CapabilityType: "unknown", CapabilityID: "x"},
					{CapabilityType: interfaces.CapabilityTypeFunction, CapabilityID: "no-owner"},
					{CapabilityType: interfaces.CapabilityTypeSkill},
				},
			})
			So(err, ShouldBeNil)
			So(resp.Entries, ShouldBeEmpty)
		})

		Convey("类型过滤把白名单筛空", func() {
			resp, err := service.SearchCapabilities(context.Background(), &interfaces.SearchCapabilitiesReq{
				Query: "汇率",
				Refs:  []interfaces.CapabilityRef{skillRef("s-1")},
				Types: []string{interfaces.CapabilityTypeMCPTool},
			})
			So(err, ShouldBeNil)
			So(resp.Entries, ShouldBeEmpty)
		})
	})
}

// TestCapabilitySearchWhitelistBound rejects a whitelist too large to read in a log rather than
// letting it reach the engine.
func TestCapabilitySearchWhitelistBound(t *testing.T) {
	Convey("白名单超过上限返回 400", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		vega := mocks.NewMockVegaBackendClient(ctrl)
		vega.EXPECT().QueryDatasetData(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

		refs := make([]interfaces.CapabilityRef, 0, maxSearchWhitelist+1)
		for i := 0; i <= maxSearchWhitelist; i++ {
			refs = append(refs, skillRef(fmt.Sprintf("s-%d", i)))
		}
		_, err := newSearch(vega, mocks.NewMockMFModelAPIClient(ctrl)).SearchCapabilities(
			context.Background(), &interfaces.SearchCapabilitiesReq{Query: "x", Refs: refs})
		So(err, ShouldNotBeNil)
	})
}

// TestCapabilitySearchKnnFilterIsPreFilter is the load-bearing test of the whole design.
//
// The whitelist must ride inside the knn condition's sub_conditions, which vega renders as the
// filter of the knn field object. As a sibling of "knn" OpenSearch rejects the query outright
// (#1296); applied after retrieval it silently empties the result for any network with few mounts.
func TestCapabilitySearchKnnFilterIsPreFilter(t *testing.T) {
	Convey("向量通道把白名单下推为 knn 的前置过滤", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		vega := mocks.NewMockVegaBackendClient(ctrl)
		modelAPI := mocks.NewMockMFModelAPIClient(ctrl)
		embeddingOnce(modelAPI)

		conditions := make(chan map[string]any, 2)
		vega.EXPECT().QueryDatasetData(gomock.Any(), capabilityDataset, gomock.Any()).DoAndReturn(
			func(_ context.Context, _ string, params *interfaces.VegaDataQueryParams) (*interfaces.VegaDataQueryResp, error) {
				conditions <- params.FilterCondition
				return &interfaces.VegaDataQueryResp{Entries: []map[string]any{}}, nil
			}).Times(2)

		_, err := newSearch(vega, modelAPI).SearchCapabilities(context.Background(), &interfaces.SearchCapabilitiesReq{
			Query: "汇率换算",
			Refs:  []interfaces.CapabilityRef{skillRef("s-1"), functionRef("box-1", "tool-1")},
			TopK:  7,
		})
		So(err, ShouldBeNil)
		close(conditions)

		var knn, match map[string]any
		for cond := range conditions {
			if cond["operation"] == "knn_vector" {
				knn = cond
				continue
			}
			match = cond
		}

		Convey("knn 通道", func() {
			So(knn, ShouldNotBeNil)
			So(knn["field"], ShouldEqual, "_vector")
			So(knn["limit_key"], ShouldEqual, "k")
			So(knn["limit_value"], ShouldEqual, 7)

			subs, ok := knn["sub_conditions"].([]map[string]any)
			So(ok, ShouldBeTrue)
			So(len(subs), ShouldEqual, 1)
			So(subs[0]["field"], ShouldEqual, "capability_key")
			So(subs[0]["operation"], ShouldEqual, "in")
			keys, ok := subs[0]["value"].([]string)
			So(ok, ShouldBeTrue)
			So(keys, ShouldResemble, []string{
				capabilityKey(skillRef("s-1")),
				capabilityKey(functionRef("box-1", "tool-1")),
			})
		})

		Convey("BM25 通道是独立请求，不与向量拼成一个 should", func() {
			So(match, ShouldNotBeNil)
			So(match["operation"], ShouldEqual, "and")
			subs, ok := match["sub_conditions"].([]map[string]any)
			So(ok, ShouldBeTrue)
			So(len(subs), ShouldEqual, 2)
			So(subs[0]["field"], ShouldEqual, "capability_key")
			So(subs[1]["operation"], ShouldEqual, "or")
		})
	})
}

// TestCapabilitySearchFusesByRank checks that a document both channels returned is reported as
// hybrid and outranks one only a single channel found.
func TestCapabilitySearchFusesByRank(t *testing.T) {
	Convey("双通道按 rank 融合", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		vega := mocks.NewMockVegaBackendClient(ctrl)
		modelAPI := mocks.NewMockMFModelAPIClient(ctrl)
		embeddingOnce(modelAPI)

		both := skillRef("s-both")
		knnOnly := skillRef("s-knn")
		matchOnly := functionRef("box-1", "t-match")

		vega.EXPECT().QueryDatasetData(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, _ string, params *interfaces.VegaDataQueryParams) (*interfaces.VegaDataQueryResp, error) {
				if params.FilterCondition["operation"] == "knn_vector" {
					return &interfaces.VegaDataQueryResp{Entries: []map[string]any{
						entryOf(knnOnly, "knn only"),
						entryOf(both, "both"),
					}}, nil
				}
				return &interfaces.VegaDataQueryResp{Entries: []map[string]any{
					entryOf(both, "both"),
					entryOf(matchOnly, "match only"),
				}}, nil
			}).Times(2)

		resp, err := newSearch(vega, modelAPI).SearchCapabilities(context.Background(), &interfaces.SearchCapabilitiesReq{
			Query: "汇率",
			Refs:  []interfaces.CapabilityRef{both, knnOnly, matchOnly},
		})
		So(err, ShouldBeNil)
		So(len(resp.Entries), ShouldEqual, 3)

		byID := map[string]*interfaces.CapabilityHit{}
		for _, hit := range resp.Entries {
			byID[hit.CapabilityID] = hit
		}
		So(byID["s-both"].MatchedBy, ShouldEqual, interfaces.CapabilityMatchedByHybrid)
		So(byID["s-knn"].MatchedBy, ShouldEqual, interfaces.CapabilityMatchedByKnn)
		So(byID["t-match"].MatchedBy, ShouldEqual, interfaces.CapabilityMatchedByMatch)

		// Second in one channel and first in the other beats first in one channel alone.
		So(resp.Entries[0].CapabilityID, ShouldEqual, "s-both")
	})
}

// TestCapabilitySearchSurvivesOneChannel keeps a working channel answering when the other fails,
// and refuses to invent an answer when neither can.
func TestCapabilitySearchSurvivesOneChannel(t *testing.T) {
	Convey("单通道失败不拖垮检索", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		Convey("向量通道建不出来时仍走全文", func() {
			vega := mocks.NewMockVegaBackendClient(ctrl)
			modelAPI := mocks.NewMockMFModelAPIClient(ctrl)
			// No embedding: the vector channel cannot be built at all.
			modelAPI.EXPECT().Embeddings(gomock.Any(), gomock.Any()).
				Return(nil, fmt.Errorf("embedding service down"))
			// Assertions cannot run on the channel goroutine, so the condition is captured here
			// and checked on the test goroutine below.
			issued := make(chan map[string]any, 1)
			vega.EXPECT().QueryDatasetData(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, _ string, params *interfaces.VegaDataQueryParams) (*interfaces.VegaDataQueryResp, error) {
					issued <- params.FilterCondition
					return &interfaces.VegaDataQueryResp{Entries: []map[string]any{
						entryOf(skillRef("s-1"), "汇率换算"),
					}}, nil
				}).Times(1)

			resp, err := newSearch(vega, modelAPI).SearchCapabilities(context.Background(),
				&interfaces.SearchCapabilitiesReq{Query: "汇率", Refs: []interfaces.CapabilityRef{skillRef("s-1")}})
			So(err, ShouldBeNil)
			So((<-issued)["operation"], ShouldEqual, "and")
			So(len(resp.Entries), ShouldEqual, 1)
			So(resp.Entries[0].MatchedBy, ShouldEqual, interfaces.CapabilityMatchedByMatch)
		})

		Convey("两个通道都失败时报错，不返回空成功", func() {
			vega := mocks.NewMockVegaBackendClient(ctrl)
			modelAPI := mocks.NewMockMFModelAPIClient(ctrl)
			embeddingOnce(modelAPI)
			vega.EXPECT().QueryDatasetData(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(nil, fmt.Errorf("index unreachable")).Times(2)

			_, err := newSearch(vega, modelAPI).SearchCapabilities(context.Background(),
				&interfaces.SearchCapabilitiesReq{Query: "汇率", Refs: []interfaces.CapabilityRef{skillRef("s-1")}})
			So(err, ShouldNotBeNil)
		})
	})
}

// TestCapabilitySearchEnumerates covers the no-query case: the whitelist is listed, and the result
// says so rather than claiming a ranking it did not do.
func TestCapabilitySearchEnumerates(t *testing.T) {
	Convey("无查询词时是枚举，matched_by 如实报 like", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		vega := mocks.NewMockVegaBackendClient(ctrl)
		modelAPI := mocks.NewMockMFModelAPIClient(ctrl)
		// No embedding call: there is no query to vectorise.
		modelAPI.EXPECT().Embeddings(gomock.Any(), gomock.Any()).Times(0)

		vega.EXPECT().QueryDatasetData(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, _ string, params *interfaces.VegaDataQueryParams) (*interfaces.VegaDataQueryResp, error) {
				So(params.FilterCondition["field"], ShouldEqual, "capability_key")
				return &interfaces.VegaDataQueryResp{Entries: []map[string]any{
					entryOf(skillRef("s-1"), "标准补货"),
				}}, nil
			}).Times(1)

		resp, err := newSearch(vega, modelAPI).SearchCapabilities(context.Background(),
			&interfaces.SearchCapabilitiesReq{Query: "  ", Refs: []interfaces.CapabilityRef{skillRef("s-1")}})
		So(err, ShouldBeNil)
		So(len(resp.Entries), ShouldEqual, 1)
		So(resp.Entries[0].MatchedBy, ShouldEqual, interfaces.CapabilityMatchedByLike)
	})
}

// TestCapabilityKeyIdentity locks the identity rules the whitelist and the document id rest on.
func TestCapabilityKeyIdentity(t *testing.T) {
	Convey("三段式身份", t, func() {
		Convey("不同类型的同名能力不互相碰撞", func() {
			function := functionRef("owner-1", "same-id")
			mcp := interfaces.CapabilityRef{
				CapabilityType: interfaces.CapabilityTypeMCPTool, OwnerID: "owner-1", CapabilityID: "same-id",
			}
			So(capabilityKey(function), ShouldNotEqual, capabilityKey(mcp))
			So(capabilityDocID(function), ShouldNotEqual, capabilityDocID(mcp))
		})

		Convey("分隔符不会被 owner 或 id 里的内容伪造", func() {
			// Without a separator that cannot appear in a part, ("a","b/c") and ("a/b","c")
			// would collapse onto one key and one document.
			left := functionRef("a", "b/c")
			right := functionRef("a/b", "c")
			So(capabilityKey(left), ShouldNotEqual, capabilityKey(right))
		})

		Convey("文档 id 只用 URL 安全字符", func() {
			// The id travels in a URL path segment; an MCP tool name is not ours to constrain.
			id := capabilityDocID(interfaces.CapabilityRef{
				CapabilityType: interfaces.CapabilityTypeMCPTool,
				OwnerID:        "mcp-1",
				CapabilityID:   "weird/name?with#stuff",
			})
			So(len(id), ShouldEqual, 64)
			for _, r := range id {
				So((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f'), ShouldBeTrue)
			}
		})

		Convey("同一身份稳定映射到同一文档", func() {
			So(capabilityDocID(skillRef("s-1")), ShouldEqual, capabilityDocID(skillRef("s-1")))
		})
	})
}

// TestSchemaAnalyzerIsReadBack covers the rule that keeps an index from being rebuilt over an
// analyzer that came and went.
func TestSchemaAnalyzerIsReadBack(t *testing.T) {
	Convey("分词器从存量 schema 读回", t, func() {
		schema := buildCapabilityIndexSchema(1024, "ik_max_word")
		So(schemaAnalyzer(schema), ShouldEqual, "ik_max_word")

		Convey("读不到时留给调用方兜默认值", func() {
			So(schemaAnalyzer(nil), ShouldEqual, "")
		})
	})
}
