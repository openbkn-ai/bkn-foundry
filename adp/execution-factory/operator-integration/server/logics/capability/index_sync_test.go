// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package capability

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
)

// readySync builds a sync service that already believes its dataset exists.
func readySync(vega interfaces.VegaBackendClient, modelAPI interfaces.MFModelAPIClient) *capabilityIndexSync {
	return &capabilityIndexSync{
		vegaClient:  vega,
		modelAPI:    modelAPI,
		logger:      logger.DefaultLogger(),
		initialized: true,
		datasetID:   capabilityDataset,
	}
}

func indexEntry(ref interfaces.CapabilityRef, name string) map[string]any {
	return map[string]any{
		"capability_type": ref.CapabilityType,
		"owner_id":        ref.OwnerID,
		"capability_id":   ref.CapabilityID,
		"name":            name,
		"description":     name + " desc",
	}
}

// TestUpsertRejectsUnaddressableIdentity keeps rows the index cannot address out of it.
func TestUpsertRejectsUnaddressableIdentity(t *testing.T) {
	Convey("身份不合法的能力不写入", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		vega := mocks.NewMockVegaBackendClient(ctrl)
		vega.EXPECT().WriteDatasetDocument(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
		sync := readySync(vega, mocks.NewMockMFModelAPIClient(ctrl))

		Convey("未知类型", func() {
			err := sync.UpsertCapability(context.Background(), &interfaces.CapabilityDocument{
				CapabilityRef: interfaces.CapabilityRef{CapabilityType: "api", CapabilityID: "x"},
			})
			So(err, ShouldNotBeNil)
		})

		Convey("函数工具缺 owner", func() {
			err := sync.UpsertCapability(context.Background(), &interfaces.CapabilityDocument{
				CapabilityRef: interfaces.CapabilityRef{
					CapabilityType: interfaces.CapabilityTypeFunction, CapabilityID: "tool-1",
				},
			})
			So(err, ShouldNotBeNil)
		})

		Convey("Skill 允许 owner 为空", func() {
			modelAPI := mocks.NewMockMFModelAPIClient(ctrl)
			modelAPI.EXPECT().Embeddings(gomock.Any(), gomock.Any()).Return(&interfaces.EmbeddingResp{
				Data: []interfaces.EmbeddingData{{Embedding: []float32{0.1}}},
			}, nil)
			writeVega := mocks.NewMockVegaBackendClient(ctrl)
			writeVega.EXPECT().WriteDatasetDocument(gomock.Any(), capabilityDataset,
				capabilityDocID(skillRef("s-1")), gomock.Any()).Return(nil)

			err := readySync(writeVega, modelAPI).UpsertCapability(context.Background(),
				&interfaces.CapabilityDocument{CapabilityRef: skillRef("s-1"), Name: "标准补货"})
			So(err, ShouldBeNil)
		})
	})
}

// TestUpsertIsDroppedBeforeTheDatasetExists covers the case where a write arrives before Init has
// managed to create the dataset. Dropping it with a warning is deliberate: failing the tool or
// Skill write over an index that is not ready yet would be worse, and the reconciler re-drives it.
func TestUpsertIsDroppedBeforeTheDatasetExists(t *testing.T) {
	Convey("数据集未就绪时跳过写入而不是报错", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		vega := mocks.NewMockVegaBackendClient(ctrl)
		vega.EXPECT().WriteDatasetDocument(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
		sync := readySync(vega, mocks.NewMockMFModelAPIClient(ctrl))
		sync.initialized = false

		err := sync.UpsertCapability(context.Background(),
			&interfaces.CapabilityDocument{CapabilityRef: skillRef("s-1"), Name: "标准补货"})
		So(err, ShouldBeNil)
	})
}

// TestListIndexedPagesByKey covers the scan that both the reconciler and DeleteOwner stand on.
//
// The cursor is capability_key rather than capability_id because only the key is unique: a tool id
// is unique inside its box, so paging on the id would skip the rest of a run of equal ids that
// straddled a page boundary.
func TestListIndexedPagesByKey(t *testing.T) {
	Convey("索引扫描按 capability_key 翻页", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		vega := mocks.NewMockVegaBackendClient(ctrl)

		// Two boxes hold a tool of the same id: paging on capability_id would lose one of them.
		first := make([]map[string]any, 0, ownerScanBatch)
		for i := 0; i < ownerScanBatch; i++ {
			first = append(first, indexEntry(functionRef(fmt.Sprintf("box-%03d", i), "same-tool"), "tool"))
		}
		last := functionRef(fmt.Sprintf("box-%03d", ownerScanBatch-1), "same-tool")

		call := 0
		vega.EXPECT().QueryDatasetData(gomock.Any(), capabilityDataset, gomock.Any()).DoAndReturn(
			func(_ context.Context, _ string, params *interfaces.VegaDataQueryParams) (*interfaces.VegaDataQueryResp, error) {
				call++
				So(params.Sort[0].Field, ShouldEqual, "capability_key")
				subs, _ := params.FilterCondition["sub_conditions"].([]map[string]any)
				if call == 1 {
					So(len(subs), ShouldEqual, 1)
					return &interfaces.VegaDataQueryResp{Entries: first}, nil
				}
				// The second page asks for keys strictly after the last one seen.
				So(len(subs), ShouldEqual, 2)
				So(subs[1]["field"], ShouldEqual, "capability_key")
				So(subs[1]["operation"], ShouldEqual, "gt")
				So(subs[1]["value"], ShouldEqual, capabilityKey(last))
				return &interfaces.VegaDataQueryResp{Entries: []map[string]any{
					indexEntry(functionRef("box-999", "another"), "another"),
				}}, nil
			}).Times(2)

		indexed, err := readySync(vega, nil).ListIndexed(context.Background(), interfaces.CapabilityTypeFunction)
		So(err, ShouldBeNil)
		So(len(indexed), ShouldEqual, ownerScanBatch+1)
	})
}

// TestListIndexedStopsWhenNothingAdvances stops a full page of unreadable rows from looping on
// itself forever.
func TestListIndexedStopsWhenNothingAdvances(t *testing.T) {
	Convey("整页都读不出身份时停止而不是空转", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		junk := make([]map[string]any, 0, ownerScanBatch)
		for i := 0; i < ownerScanBatch; i++ {
			junk = append(junk, map[string]any{"capability_type": "", "capability_id": ""})
		}
		vega := mocks.NewMockVegaBackendClient(ctrl)
		vega.EXPECT().QueryDatasetData(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(&interfaces.VegaDataQueryResp{Entries: junk}, nil).Times(1)

		indexed, err := readySync(vega, nil).ListIndexed(context.Background(), interfaces.CapabilityTypeFunction)
		So(err, ShouldBeNil)
		So(indexed, ShouldBeEmpty)
	})
}

// TestDeleteOwnerPagesPastOnePage covers a purge of an owner holding more rows than one page.
func TestDeleteOwnerPagesPastOnePage(t *testing.T) {
	Convey("按 owner 清除会翻完所有页", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		full := make([]map[string]any, 0, ownerScanBatch)
		for i := 0; i < ownerScanBatch; i++ {
			full = append(full, indexEntry(functionRef("box-1", fmt.Sprintf("tool-%04d", i)), "tool"))
		}
		tail := []map[string]any{indexEntry(functionRef("box-1", "tool-9999"), "tool")}

		vega := mocks.NewMockVegaBackendClient(ctrl)
		call := 0
		vega.EXPECT().QueryDatasetData(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, _ string, params *interfaces.VegaDataQueryParams) (*interfaces.VegaDataQueryResp, error) {
				call++
				if call == 1 {
					return &interfaces.VegaDataQueryResp{Entries: full}, nil
				}
				return &interfaces.VegaDataQueryResp{Entries: tail}, nil
			}).Times(2)
		vega.EXPECT().DeleteDatasetDocumentByID(gomock.Any(), capabilityDataset, gomock.Any()).
			Return(nil).Times(ownerScanBatch + 1)

		err := readySync(vega, nil).DeleteOwner(context.Background(), interfaces.CapabilityTypeFunction, "box-1")
		So(err, ShouldBeNil)
	})
}

// TestListIndexedByOwnerScopesTheRead keeps the reconciler from scanning a whole capability type
// once per owner.
func TestListIndexedByOwnerScopesTheRead(t *testing.T) {
	Convey("按 owner 扫描把 owner 下推到查询里", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		vega := mocks.NewMockVegaBackendClient(ctrl)
		vega.EXPECT().QueryDatasetData(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, _ string, params *interfaces.VegaDataQueryParams) (*interfaces.VegaDataQueryResp, error) {
				subs, _ := params.FilterCondition["sub_conditions"].([]map[string]any)
				So(len(subs), ShouldEqual, 2)
				So(subs[1]["field"], ShouldEqual, "owner_id")
				So(subs[1]["value"], ShouldEqual, "mcp-1")
				return &interfaces.VegaDataQueryResp{Entries: nil}, nil
			}).Times(1)

		_, err := readySync(vega, nil).ListIndexedByOwner(context.Background(),
			interfaces.CapabilityTypeMCPTool, "mcp-1")
		So(err, ShouldBeNil)
	})
}

// TestRebuildReasonCatchesAnUnwritableDataset covers the state a dataset row can be left in after
// an upgrade: the row exists, so it is adopted, but nothing behind it can accept a write.
//
// This is not hypothetical. On a deployment whose vega predated managed index names, the dataset
// row carried no index name at all, and every document write came back 400 "dataset resource has
// no available local index" — forever, because adoption never questioned the row.
func TestRebuildReasonCatchesAnUnwritableDataset(t *testing.T) {
	Convey("接手存量数据集时要看它能不能写", t, func() {
		model := &interfaces.EmbeddingModel{ModelID: "m-1", ModelName: "embedding", EmbeddingDim: 8}
		healthy := &interfaces.VegaResource{
			ID:               capabilityDataset,
			LocalIndexName:   "vega-dataset-01",
			LocalIndexStatus: interfaces.VegaLocalIndexAvailable,
			SchemaDefinition: buildCapabilityIndexSchema(8, defaultFulltextAnalyzer),
			IndexConfig:      &interfaces.VegaResourceIndexConfig{DefaultEmbeddingModel: "m-1"},
		}

		Convey("健康的数据集不重建", func() {
			So(rebuildReason(healthy, model, defaultFulltextAnalyzer), ShouldEqual, "")
		})

		Convey("没有托管索引名要重建", func() {
			broken := *healthy
			broken.LocalIndexName = ""
			So(rebuildReason(&broken, model, defaultFulltextAnalyzer), ShouldNotEqual, "")
		})

		Convey("托管索引不可用要重建", func() {
			broken := *healthy
			broken.LocalIndexStatus = interfaces.VegaLocalIndexUnavailable
			So(rebuildReason(&broken, model, defaultFulltextAnalyzer), ShouldNotEqual, "")
		})

		Convey("embedding 模型变了要重建", func() {
			changed := *healthy
			changed.IndexConfig = &interfaces.VegaResourceIndexConfig{DefaultEmbeddingModel: "m-2"}
			So(rebuildReason(&changed, model, defaultFulltextAnalyzer), ShouldNotEqual, "")
		})

		Convey("分词器不同不算重建理由——它只在建库时定一次", func() {
			adopted := *healthy
			adopted.SchemaDefinition = buildCapabilityIndexSchema(8, "ik_max_word")
			// The caller reads the analyzer back off the resource, so the comparison is made
			// against the analyzer the dataset already has, not against a freshly resolved one.
			So(rebuildReason(&adopted, model, "ik_max_word"), ShouldEqual, "")
		})
	})
}

// TestInitIsSerialised covers what three reconcilers calling EnsureInitialized at once can do to a
// rebuild.
//
// The rebuild branch deletes and then creates. Run twice concurrently, the second delete removes
// the dataset the first just created, and initialized flips back to false in between — the window
// in which every write is dropped with a warning instead of landing.
//
// What is pinned is the ordering, not a call count. Init deliberately re-runs on every pass: it is
// how a dataset whose managed index was later lost heals without a restart, and asserting "created
// exactly once" would lock in the one-shot behaviour that removes.
func TestInitIsSerialised(t *testing.T) {
	Convey("Init 并发进入不会删掉别人刚建好的数据集", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		vega := mocks.NewMockVegaBackendClient(ctrl)
		modelManager := mocks.NewMockMFModelManager(ctrl)

		vega.EXPECT().GetCatalogByID(gomock.Any(), gomock.Any()).
			Return(&interfaces.VegaCatalog{ID: executionFactoryCatalogID, Enabled: true}, nil).AnyTimes()
		modelManager.EXPECT().GetDefaultEmbeddingModel(gomock.Any(), gomock.Any()).Return(
			&interfaces.EmbeddingModel{ModelID: "m-1", ModelName: "embedding", EmbeddingDim: 8}, nil).AnyTimes()

		// The dataset exists after the first create, which is what a later caller must observe —
		// an unserialised second caller would see nil, create again, and the two would race.
		var mu sync.Mutex
		exists := false
		inFlight := 0
		var overlapped bool

		vega.EXPECT().GetResourceByID(gomock.Any(), capabilityDataset).DoAndReturn(
			func(context.Context, string) (*interfaces.VegaResource, error) {
				mu.Lock()
				defer mu.Unlock()
				if !exists {
					return nil, nil
				}
				return &interfaces.VegaResource{
					ID:               capabilityDataset,
					LocalIndexName:   "vega-dataset-01",
					LocalIndexStatus: interfaces.VegaLocalIndexAvailable,
					SchemaDefinition: buildCapabilityIndexSchema(8, defaultFulltextAnalyzer),
					IndexConfig:      &interfaces.VegaResourceIndexConfig{DefaultEmbeddingModel: "m-1"},
				}, nil
			}).AnyTimes()

		vega.EXPECT().CreateResource(gomock.Any(), gomock.Any()).DoAndReturn(
			func(context.Context, *interfaces.VegaResourceRequest) (*interfaces.VegaResource, error) {
				mu.Lock()
				inFlight++
				if inFlight > 1 {
					overlapped = true
				}
				mu.Unlock()
				time.Sleep(5 * time.Millisecond)
				mu.Lock()
				inFlight--
				exists = true
				mu.Unlock()
				return nil, nil
			}).AnyTimes()

		s := &capabilityIndexSync{
			vegaClient:   vega,
			modelManager: modelManager,
			logger:       logger.DefaultLogger(),
		}

		var wg sync.WaitGroup
		for i := 0; i < 3; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = s.Init(context.Background())
			}()
		}
		wg.Wait()

		So(overlapped, ShouldBeFalse)
		So(s.isInitialized(), ShouldBeTrue)
	})
}

// TestInitStaysRepeatable keeps Init from becoming a one-shot. The reconcilers call it on every
// pass, and that is how a dataset whose managed index was later lost heals without a restart.
func TestInitStaysRepeatable(t *testing.T) {
	Convey("Init 每轮都要真的重新判断，否则索引失效后只能等重启", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		vega := mocks.NewMockVegaBackendClient(ctrl)
		modelManager := mocks.NewMockMFModelManager(ctrl)
		vega.EXPECT().GetCatalogByID(gomock.Any(), gomock.Any()).
			Return(&interfaces.VegaCatalog{ID: executionFactoryCatalogID, Enabled: true}, nil).AnyTimes()
		modelManager.EXPECT().GetDefaultEmbeddingModel(gomock.Any(), gomock.Any()).Return(
			&interfaces.EmbeddingModel{ModelID: "m-1", ModelName: "embedding", EmbeddingDim: 8}, nil).AnyTimes()

		healthy := &interfaces.VegaResource{
			ID:               capabilityDataset,
			LocalIndexName:   "vega-dataset-01",
			LocalIndexStatus: interfaces.VegaLocalIndexAvailable,
			SchemaDefinition: buildCapabilityIndexSchema(8, defaultFulltextAnalyzer),
			IndexConfig:      &interfaces.VegaResourceIndexConfig{DefaultEmbeddingModel: "m-1"},
		}
		// First pass adopts a healthy dataset; by the second its index has been invalidated.
		broken := *healthy
		broken.LocalIndexName = ""
		broken.LocalIndexStatus = interfaces.VegaLocalIndexUnavailable

		gomock.InOrder(
			vega.EXPECT().GetResourceByID(gomock.Any(), capabilityDataset).Return(healthy, nil),
			vega.EXPECT().GetResourceByID(gomock.Any(), capabilityDataset).Return(&broken, nil),
		)
		// The second pass must notice and rebuild.
		vega.EXPECT().DeleteResource(gomock.Any(), capabilityDataset).Return(nil).Times(1)
		vega.EXPECT().CreateResource(gomock.Any(), gomock.Any()).Return(nil, nil).Times(1)

		s := &capabilityIndexSync{
			vegaClient:   vega,
			modelManager: modelManager,
			logger:       logger.DefaultLogger(),
		}
		So(s.Init(context.Background()), ShouldBeNil)
		So(s.Init(context.Background()), ShouldBeNil)
	})
}
