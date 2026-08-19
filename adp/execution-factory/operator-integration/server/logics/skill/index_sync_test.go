package skill

import (
	"context"
	"errors"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

func TestSkillIndexSync(t *testing.T) {
	Convey("SkillIndexSync", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		Convey("EnsureDataset creates catalog and resource when absent", func() {
			var createdCatalog *interfaces.VegaCatalogRequest
			var createdResource *interfaces.VegaResourceRequest
			mockModelManager := mocks.NewMockMFModelManager(ctrl)
			mockModelAPI := mocks.NewMockMFModelAPIClient(ctrl)
			mockVegaClient := mocks.NewMockVegaBackendClient(ctrl)
			syncer := &skillIndexSync{
				modelManager: mockModelManager,
				modelAPI:     mockModelAPI,
				vegaClient:   mockVegaClient,
				logger:       logger.DefaultLogger(),
			}
			mockVegaClient.EXPECT().GetCatalogByID(gomock.Any(), executionFactoryCatalogID).Return(nil, nil)
			mockVegaClient.EXPECT().CreateCatalog(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, req *interfaces.VegaCatalogRequest) (*interfaces.VegaCatalog, error) {
				createdCatalog = req
				return &interfaces.VegaCatalog{ID: req.ID}, nil
			})
			mockVegaClient.EXPECT().GetResourceByID(gomock.Any(), executionFactorySkillDataset).Return(nil, nil)
			// The system is not configured by default -> fallback by name "embedding".
			mockModelManager.EXPECT().GetDefaultEmbeddingModel(gomock.Any(), interfaces.SmallModelTypeEmbedding).
				Return(nil, nil)
			mockModelManager.EXPECT().GetEmbeddingModel(gomock.Any(), interfaces.SmallModelTypeEmbedding, interfaces.SmallModelTypeEmbedding).
				Return(&interfaces.EmbeddingModel{ModelName: interfaces.SmallModelTypeEmbedding, EmbeddingDim: 768}, nil)
			mockVegaClient.EXPECT().CreateResource(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, req *interfaces.VegaResourceRequest) (*interfaces.VegaResource, error) {
				createdResource = req
				return &interfaces.VegaResource{ID: req.ID}, nil
			})

			err := syncer.Init(context.Background())
			So(err, ShouldBeNil)
			So(createdCatalog, ShouldNotBeNil)
			So(createdCatalog.ID, ShouldEqual, executionFactoryCatalogID)
			// The logical directory must be enabled, otherwise the dataset read and write under it will be rejected by vega 409.
			So(createdCatalog.Enabled, ShouldBeTrue)
			So(createdCatalog.Internal, ShouldBeTrue)
			// internal tag: Studio relies on it to recognize the built-in directory (the front end does not read the internal field)
			So(createdCatalog.Tags, ShouldContain, internalCatalogTag)
			So(createdResource, ShouldNotBeNil)
			So(createdResource.ID, ShouldEqual, executionFactorySkillDataset)
			So(createdResource.Status, ShouldEqual, executionFactoryDatasetStatus)
			// The model snapshot enters the resource level index_config: tag will be returned by vega's ':' check.
			// The feature config of the vector attribute will be copied into knn_vector mapping, causing index creation to fail.
			for _, tag := range createdResource.Tags {
				So(tag, ShouldNotContainSubstring, ":")
			}
			So(createdResource.IndexConfig, ShouldNotBeNil)
			So(createdResource.IndexConfig.DefaultEmbeddingModel, ShouldEqual, interfaces.SmallModelTypeEmbedding)
			So(extractEmbeddingModelFromSchema(createdResource.SchemaDefinition), ShouldBeEmpty)
			for _, property := range createdResource.SchemaDefinition {
				for _, feature := range property.Features {
					_, ok := feature.Config[embeddingModelConfigKey]
					So(ok, ShouldBeFalse)
					So(feature.RefProperty, ShouldBeEmpty)
				}
			}
			So(len(createdResource.SchemaDefinition), ShouldEqual, 10)
			var nameProperty interfaces.VegaProperty
			var descriptionProperty interfaces.VegaProperty
			for _, property := range createdResource.SchemaDefinition {
				switch property.Name {
				case "name":
					nameProperty = property
				case "description":
					descriptionProperty = property
				}
			}
			So(nameProperty.Name, ShouldEqual, "name")
			So(descriptionProperty.Name, ShouldEqual, "description")
			So(len(nameProperty.Features), ShouldEqual, 2)
			So(len(descriptionProperty.Features), ShouldEqual, 2)
			So(nameProperty.Features[0].Name, ShouldEqual, "keyword_name")
			So(nameProperty.Features[0].FeatureType, ShouldEqual, "keyword")
			So(nameProperty.Features[0].Config["ignore_above"], ShouldEqual, 1024)
			So(nameProperty.Features[1].Name, ShouldEqual, "fulltext_name")
			So(nameProperty.Features[1].FeatureType, ShouldEqual, "fulltext")
			So(descriptionProperty.Features[0].Name, ShouldEqual, "keyword_description")
			So(descriptionProperty.Features[0].FeatureType, ShouldEqual, "keyword")
			So(descriptionProperty.Features[0].Config["ignore_above"], ShouldEqual, 1024)
			So(descriptionProperty.Features[1].Name, ShouldEqual, "fulltext_description")
			So(descriptionProperty.Features[1].FeatureType, ShouldEqual, "fulltext")
		})

		Convey("EnsureDataset succeeds without embedding lookup when resource already exists", func() {
			mockModelManager := mocks.NewMockMFModelManager(ctrl)
			mockModelAPI := mocks.NewMockMFModelAPIClient(ctrl)
			mockVegaClient := mocks.NewMockVegaBackendClient(ctrl)
			syncer := &skillIndexSync{
				modelManager: mockModelManager,
				modelAPI:     mockModelAPI,
				vegaClient:   mockVegaClient,
				logger:       logger.DefaultLogger(),
			}
			mockVegaClient.EXPECT().GetCatalogByID(gomock.Any(), executionFactoryCatalogID).
				Return(&interfaces.VegaCatalog{
					ID:      executionFactoryCatalogID,
					Name:    executionFactoryCatalogID,
					Tags:    []string{internalCatalogTag},
					Enabled: true,
				}, nil)
			mockVegaClient.EXPECT().GetResourceByID(gomock.Any(), executionFactorySkillDataset).
				Return(&interfaces.VegaResource{ID: executionFactorySkillDataset, Name: executionFactorySkillDataset}, nil)

			err := syncer.Init(context.Background())
			So(err, ShouldBeNil)
			So(syncer.isInitialized(), ShouldBeTrue)
			So(syncer.getDatasetID(), ShouldEqual, executionFactorySkillDataset)
		})

		Convey("Init reads the model snapshot from index_config first, then schema, then tag", func() {
			mockVegaClient := mocks.NewMockVegaBackendClient(ctrl)
			syncer := &skillIndexSync{vegaClient: mockVegaClient, logger: logger.DefaultLogger()}
			mockVegaClient.EXPECT().GetCatalogByID(gomock.Any(), executionFactoryCatalogID).
				Return(&interfaces.VegaCatalog{
					ID:      executionFactoryCatalogID,
					Name:    executionFactoryCatalogID,
					Tags:    []string{internalCatalogTag},
					Enabled: true,
				}, nil)
			mockVegaClient.EXPECT().GetResourceByID(gomock.Any(), executionFactorySkillDataset).
				Return(&interfaces.VegaResource{
					ID:          executionFactorySkillDataset,
					Name:        executionFactorySkillDataset,
					IndexConfig: &interfaces.VegaResourceIndexConfig{DefaultEmbeddingModel: "text-embedding-v4"},
					// The old snapshots in schema and tag are all there, index_config takes priority.
					SchemaDefinition: []interfaces.VegaProperty{{
						Name: "_vector",
						Type: "vector",
						Features: []interfaces.VegaPropertyFeature{{
							FeatureType: "vector",
							Config:      map[string]any{embeddingModelConfigKey: "stale-from-schema"},
						}},
					}},
					Tags: []string{embeddingModelTagPrefix + "stale-from-tag"},
				}, nil)

			So(syncer.Init(context.Background()), ShouldBeNil)
			So(syncer.getEmbeddingModelName(), ShouldEqual, "text-embedding-v4")
		})

		Convey("Init fails when the catalog cannot be enabled, so the retry loop takes over", func() {
			mockVegaClient := mocks.NewMockVegaBackendClient(ctrl)
			syncer := &skillIndexSync{vegaClient: mockVegaClient, logger: logger.DefaultLogger()}
			mockVegaClient.EXPECT().GetCatalogByID(gomock.Any(), executionFactoryCatalogID).
				Return(&interfaces.VegaCatalog{
					ID:      executionFactoryCatalogID,
					Name:    executionFactoryCatalogID,
					Tags:    []string{internalCatalogTag},
					Enabled: false,
				}, nil)
			mockVegaClient.EXPECT().EnableCatalog(gomock.Any(), executionFactoryCatalogID).Return(errors.New("vega 500"))

			// Data reading and writing in the disabled directory will be rejected by vega 409, and cannot be marked as initialized with this status.
			So(syncer.Init(context.Background()), ShouldNotBeNil)
			So(syncer.isInitialized(), ShouldBeFalse)
		})

		// When the data set is hung in another directory, writing is governed by its own parent directory, and disabled must be enabled.
		Convey("Init enables the dataset's own catalog when it lives elsewhere", func() {
			mockVegaClient := mocks.NewMockVegaBackendClient(ctrl)
			syncer := &skillIndexSync{vegaClient: mockVegaClient, logger: logger.DefaultLogger()}
			mockVegaClient.EXPECT().GetCatalogByID(gomock.Any(), executionFactoryCatalogID).
				Return(&interfaces.VegaCatalog{
					ID:      executionFactoryCatalogID,
					Name:    executionFactoryCatalogID,
					Tags:    []string{internalCatalogTag},
					Enabled: true,
				}, nil)
			mockVegaClient.EXPECT().GetResourceByID(gomock.Any(), executionFactorySkillDataset).
				Return(&interfaces.VegaResource{
					ID:        executionFactorySkillDataset,
					Name:      executionFactorySkillDataset,
					CatalogID: "other_catalog",
				}, nil)
			mockVegaClient.EXPECT().GetCatalogByID(gomock.Any(), "other_catalog").
				Return(&interfaces.VegaCatalog{ID: "other_catalog", Name: "other_catalog", Enabled: false}, nil)
			mockVegaClient.EXPECT().EnableCatalog(gomock.Any(), "other_catalog").Return(nil)

			So(syncer.Init(context.Background()), ShouldBeNil)
			So(syncer.getDatasetID(), ShouldEqual, executionFactorySkillDataset)
		})

		Convey("Init fails when the dataset points to a missing catalog", func() {
			mockVegaClient := mocks.NewMockVegaBackendClient(ctrl)
			syncer := &skillIndexSync{vegaClient: mockVegaClient, logger: logger.DefaultLogger()}
			mockVegaClient.EXPECT().GetCatalogByID(gomock.Any(), executionFactoryCatalogID).
				Return(&interfaces.VegaCatalog{
					ID:      executionFactoryCatalogID,
					Name:    executionFactoryCatalogID,
					Tags:    []string{internalCatalogTag},
					Enabled: true,
				}, nil)
			mockVegaClient.EXPECT().GetResourceByID(gomock.Any(), executionFactorySkillDataset).
				Return(&interfaces.VegaResource{ID: executionFactorySkillDataset, CatalogID: "ghost_catalog"}, nil)
			mockVegaClient.EXPECT().GetCatalogByID(gomock.Any(), "ghost_catalog").Return(nil, nil)

			err := syncer.Init(context.Background())
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "missing catalog")
			So(syncer.isInitialized(), ShouldBeFalse)
		})

		Convey("Init skips the internal tag backfill when the catalog already has 5 tags", func() {
			var reconciled *interfaces.VegaCatalogRequest
			mockVegaClient := mocks.NewMockVegaBackendClient(ctrl)
			syncer := &skillIndexSync{vegaClient: mockVegaClient, logger: logger.DefaultLogger()}
			fullTags := []string{"a", "b", "c", "d", "e"}
			mockVegaClient.EXPECT().GetCatalogByID(gomock.Any(), executionFactoryCatalogID).
				Return(&interfaces.VegaCatalog{
					ID:      executionFactoryCatalogID,
					Name:    "stale_display_name",
					Tags:    fullTags,
					Enabled: true,
				}, nil)
			mockVegaClient.EXPECT().UpdateCatalog(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, req *interfaces.VegaCatalogRequest) error {
				reconciled = req
				return nil
			})
			mockVegaClient.EXPECT().GetResourceByID(gomock.Any(), executionFactorySkillDataset).
				Return(&interfaces.VegaResource{ID: executionFactorySkillDataset}, nil)

			So(syncer.Init(context.Background()), ShouldBeNil)
			// If the tag exceeds the limit, don’t block it. The main goal of changing the name cannot be taken away with 400.
			So(reconciled, ShouldNotBeNil)
			So(reconciled.Name, ShouldEqual, executionFactoryCatalogID)
			So(reconciled.Tags, ShouldResemble, fullTags)
		})

		Convey("Init backfills the internal tag when only the tag is missing", func() {
			var reconciled *interfaces.VegaCatalogRequest
			mockVegaClient := mocks.NewMockVegaBackendClient(ctrl)
			syncer := &skillIndexSync{
				vegaClient: mockVegaClient,
				logger:     logger.DefaultLogger(),
			}
			mockVegaClient.EXPECT().GetCatalogByID(gomock.Any(), executionFactoryCatalogID).
				Return(&interfaces.VegaCatalog{
					ID:      executionFactoryCatalogID,
					Name:    executionFactoryCatalogID,
					Tags:    []string{"execution-factory", "索引"},
					Enabled: true,
				}, nil)
			mockVegaClient.EXPECT().UpdateCatalog(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, req *interfaces.VegaCatalogRequest) error {
				reconciled = req
				return nil
			})
			mockVegaClient.EXPECT().GetResourceByID(gomock.Any(), executionFactorySkillDataset).
				Return(&interfaces.VegaResource{ID: executionFactorySkillDataset, Name: executionFactorySkillDataset}, nil)

			err := syncer.Init(context.Background())
			So(err, ShouldBeNil)
			So(reconciled, ShouldNotBeNil)
			So(reconciled.Tags, ShouldResemble, []string{"execution-factory", "索引", internalCatalogTag})
		})

		Convey("Init survives a failed catalog rename, which is cosmetic", func() {
			mockVegaClient := mocks.NewMockVegaBackendClient(ctrl)
			syncer := &skillIndexSync{
				vegaClient: mockVegaClient,
				logger:     logger.DefaultLogger(),
			}
			resource := &interfaces.VegaResource{ID: executionFactorySkillDataset, Name: executionFactorySkillDataset}
			mockVegaClient.EXPECT().GetCatalogByID(gomock.Any(), executionFactoryCatalogID).
				Return(&interfaces.VegaCatalog{ID: executionFactoryCatalogID, Name: "stale_display_name", Enabled: true}, nil)
			mockVegaClient.EXPECT().UpdateCatalog(gomock.Any(), gomock.Any()).Return(errors.New("vega 500"))
			mockVegaClient.EXPECT().GetResourceByID(gomock.Any(), executionFactorySkillDataset).Return(resource, nil)

			err := syncer.Init(context.Background())
			So(err, ShouldBeNil)
			So(syncer.isInitialized(), ShouldBeTrue)
			So(syncer.getDatasetID(), ShouldEqual, executionFactorySkillDataset)
		})

		// The model snapshot of the old dataset only falls on the resource tag, and the read path still needs to be covered.
		Convey("Init reads the model snapshot from a tag when nothing else carries it", func() {
			mockVegaClient := mocks.NewMockVegaBackendClient(ctrl)
			syncer := &skillIndexSync{vegaClient: mockVegaClient, logger: logger.DefaultLogger()}
			mockVegaClient.EXPECT().GetCatalogByID(gomock.Any(), executionFactoryCatalogID).
				Return(&interfaces.VegaCatalog{
					ID:      executionFactoryCatalogID,
					Name:    executionFactoryCatalogID,
					Tags:    []string{internalCatalogTag},
					Enabled: true,
				}, nil)
			mockVegaClient.EXPECT().GetResourceByID(gomock.Any(), executionFactorySkillDataset).
				Return(&interfaces.VegaResource{
					ID:   executionFactorySkillDataset,
					Name: executionFactorySkillDataset,
					Tags: []string{embeddingModelTagPrefix + "text-embedding-v4"},
				}, nil)

			So(syncer.Init(context.Background()), ShouldBeNil)
			So(syncer.getEmbeddingModelName(), ShouldEqual, "text-embedding-v4")
		})

		Convey("UpsertSkill writes complete document with _id and vector", func() {
			var writtenDocs []map[string]any
			mockModelAPI := mocks.NewMockMFModelAPIClient(ctrl)
			mockVegaClient := mocks.NewMockVegaBackendClient(ctrl)
			syncer := &skillIndexSync{
				modelAPI:    mockModelAPI,
				vegaClient:  mockVegaClient,
				logger:      logger.DefaultLogger(),
				initialized: true,
			}
			mockModelAPI.EXPECT().Embeddings(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, req *interfaces.EmbeddingReq) (*interfaces.EmbeddingResp, error) {
				So(req.Model, ShouldEqual, interfaces.SmallModelTypeEmbedding)
				So(req.Input, ShouldResemble, []string{"demo\ndesc"})
				return &interfaces.EmbeddingResp{
					Data: []interfaces.EmbeddingData{{Embedding: []float32{0.1, 0.2}}},
				}, nil
			})
			mockVegaClient.EXPECT().WriteDatasetDocuments(gomock.Any(), executionFactorySkillDataset, gomock.Any()).
				DoAndReturn(func(ctx context.Context, datasetID string, documents []map[string]any) error {
					So(datasetID, ShouldEqual, executionFactorySkillDataset)
					writtenDocs = documents
					return nil
				})

			err := syncer.UpsertSkill(context.Background(), &model.SkillRepositoryDB{
				SkillID:     "skill-1",
				Name:        "demo",
				Description: "desc",
				Version:     "1.0.0",
				Category:    "general",
				CreateUser:  "u1",
				CreateTime:  100,
				UpdateUser:  "u2",
				UpdateTime:  200,
			})
			So(err, ShouldBeNil)
			So(len(writtenDocs), ShouldEqual, 1)
			So(writtenDocs[0]["_id"], ShouldEqual, "skill-1")
			So(writtenDocs[0]["id"], ShouldEqual, "skill-1")
			So(writtenDocs[0]["skill_id"], ShouldEqual, "skill-1")
			So(writtenDocs[0]["name"], ShouldEqual, "demo")
			So(writtenDocs[0]["description"], ShouldEqual, "desc")
			So(writtenDocs[0]["version"], ShouldEqual, "1.0.0")
			So(writtenDocs[0]["category"], ShouldEqual, "general")
			So(writtenDocs[0]["_vector"], ShouldResemble, []float32{0.1, 0.2})
		})

		Convey("DeleteSkill deletes dataset document by skill id", func() {
			mockVegaClient := mocks.NewMockVegaBackendClient(ctrl)
			syncer := &skillIndexSync{
				vegaClient:  mockVegaClient,
				logger:      logger.DefaultLogger(),
				initialized: true,
			}
			mockVegaClient.EXPECT().DeleteDatasetDocumentByID(gomock.Any(), executionFactorySkillDataset, "skill-1").Return(nil)

			err := syncer.DeleteSkill(context.Background(), "skill-1")
			So(err, ShouldBeNil)
		})

		Convey("UpsertSkill fails when embedding result is empty", func() {
			mockModelAPI := mocks.NewMockMFModelAPIClient(ctrl)
			mockVegaClient := mocks.NewMockVegaBackendClient(ctrl)
			syncer := &skillIndexSync{
				modelAPI:    mockModelAPI,
				vegaClient:  mockVegaClient,
				logger:      logger.DefaultLogger(),
				initialized: true,
			}
			mockModelAPI.EXPECT().Embeddings(gomock.Any(), gomock.Any()).Return(&interfaces.EmbeddingResp{}, nil)

			err := syncer.UpsertSkill(context.Background(), &model.SkillRepositoryDB{
				SkillID: "skill-1",
				Name:    "demo",
			})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "embedding result is empty")
		})

		Convey("UpdateSkill updates complete document with _id and vector", func() {
			var updatedDocs []map[string]any
			mockModelAPI := mocks.NewMockMFModelAPIClient(ctrl)
			mockVegaClient := mocks.NewMockVegaBackendClient(ctrl)
			syncer := &skillIndexSync{
				modelAPI:    mockModelAPI,
				vegaClient:  mockVegaClient,
				logger:      logger.DefaultLogger(),
				initialized: true,
			}
			mockModelAPI.EXPECT().Embeddings(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, req *interfaces.EmbeddingReq) (*interfaces.EmbeddingResp, error) {
				So(req.Model, ShouldEqual, interfaces.SmallModelTypeEmbedding)
				So(req.Input, ShouldResemble, []string{"demo\ndesc"})
				return &interfaces.EmbeddingResp{
					Data: []interfaces.EmbeddingData{{Embedding: []float32{0.3, 0.4}}},
				}, nil
			})
			mockVegaClient.EXPECT().UpdateDatasetDocuments(gomock.Any(), executionFactorySkillDataset, gomock.Any()).
				DoAndReturn(func(ctx context.Context, datasetID string, documents []map[string]any) error {
					So(datasetID, ShouldEqual, executionFactorySkillDataset)
					updatedDocs = documents
					return nil
				})

			err := syncer.UpdateSkill(context.Background(), &model.SkillRepositoryDB{
				SkillID:     "skill-2",
				Name:        "demo",
				Description: "desc",
				Version:     "1.0.1",
				Category:    "general",
				CreateUser:  "u1",
				CreateTime:  101,
				UpdateUser:  "u2",
				UpdateTime:  201,
			})
			So(err, ShouldBeNil)
			So(len(updatedDocs), ShouldEqual, 1)
			So(updatedDocs[0]["_id"], ShouldEqual, "skill-2")
			So(updatedDocs[0]["id"], ShouldEqual, "skill-2")
			So(updatedDocs[0]["skill_id"], ShouldEqual, "skill-2")
			So(updatedDocs[0]["version"], ShouldEqual, "1.0.1")
			So(updatedDocs[0]["_vector"], ShouldResemble, []float32{0.3, 0.4})
		})
	})
}
