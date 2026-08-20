package skill

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/drivenadapters"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
)

const (
	executionFactoryCatalogID     = "bkn_execution_factory_catalog"
	executionFactoryCatalogDesc   = "执行工厂的逻辑命名空间"
	executionFactorySkillDataset  = "bkn_execution_factory_skill_dataset"
	executionFactoryDatasetDesc   = "执行工厂的Skill索引数据集"
	executionFactoryDatasetStatus = "active"
	// internalCatalogTag allows built-in catalogs to have "built-in" semantic tags. Studio currently does not read the backend.
	// internal field, rely on metadata/tag/name prefix heuristic to determine the built-in directory, the tag hits its.
	// Built-in tag collection - "built-in" can be displayed correctly and management operations can be closed with zero modification on the front end.
	internalCatalogTag = "internal"
	// vegaMaxTags is aligned with vega's TAGS_MAX_NUMBER (exceeding 400)
	vegaMaxTags = 5
	// embeddingModelConfigKey is the model key in the vector feature config. For read only: once put the model.
	// The snapshot is written here, but vega will copy the feature config of the vector attribute into OpenSearch as it is.
	// knn_vector mapping, OpenSearch rejected it with unknown parameter, and the index could not be built.
	// The write path uses resource-level index_config.default_embedding_model instead.
	embeddingModelConfigKey = "embedding_model"
	// embeddingModelTagPrefix is the old vector (resource tag) of this snapshot. Vega's tag verification is disabled.
	// ':', a dataset creation request with this tag will result in 400, so only the read path is retained to be compatible with the old dataset.
	embeddingModelTagPrefix = "embedding_model:"
)

type skillIndexSync struct {
	modelManager interfaces.MFModelManager
	modelAPI     interfaces.MFModelAPIClient
	vegaClient   interfaces.VegaBackendClient
	logger       interfaces.Logger
	mu           sync.RWMutex
	initialized  bool
	// datasetID is the dataset ID actually used by this process; an empty value means it has not been parsed yet, and the default value is used.
	datasetID string
	// embeddingModelName The embedding model name locked when the system skill dataset is built (system default snapshot),
	// Protected by mu; upsert reads back the vector it generates, rather than re-fetching the current default each time.
	embeddingModelName string
	retryOnce          sync.Once
}

var (
	ssOnce     = sync.Once{}
	ssInstance *skillIndexSync
)

func NewSkillIndexSyncService() interfaces.SkillIndexSyncService {
	ssOnce.Do(func() {
		conf := config.NewConfigLoader()
		ssInstance = &skillIndexSync{
			modelManager: drivenadapters.NewMFModelManager(),
			modelAPI:     drivenadapters.NewMFModelAPIClient(),
			vegaClient:   drivenadapters.NewVegaBackendClient(),
			logger:       conf.GetLogger(),
		}
	})
	return ssInstance
}

func (s *skillIndexSync) EnsureInitialized(ctx context.Context) error {
	if err := s.Init(ctx); err != nil {
		s.retryOnce.Do(func() {
			go s.retryInit()
		})
		return err
	}
	return nil
}

// EnsureDataset ensures that the Skill index data set exists.
// If it does not exist, create it.
// If it exists, check if it is the latest version.
// If it is not the latest version, update it.
// If it is the latest version, it returns success.
func (s *skillIndexSync) Init(ctx context.Context) (err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)

	initialized := false
	defer func() {
		s.setInitialized(initialized)
	}()
	s.logger.WithContext(ctx).Infof("init skill index dataset, catalog_id=%s, resource_id=%s", executionFactoryCatalogID, executionFactorySkillDataset)
	catalogID, err := s.ensureCatalog(ctx)
	if err != nil {
		return err
	}

	datasetID, resource, err := s.resolveDataset(ctx)
	if err != nil {
		return err
	}
	s.setDatasetID(datasetID)
	if resource != nil {
		// The adopted dataset may be hung in another directory (hybrid form), and writing is governed by its own parent directory.
		if err := s.ensureDatasetCatalogEnabled(ctx, resource, catalogID); err != nil {
			return err
		}
		// The dataset already exists: read back the model name locked during creation (model building == model query).
		// The old dataset is trapped in both forms, and when neither can be retrieved, it falls back to the name "embedding", which is consistent with the behavior before the transformation.
		modelName := extractEmbeddingModelFromIndexConfig(resource.IndexConfig)
		if modelName == "" {
			modelName = extractEmbeddingModelFromSchema(resource.SchemaDefinition)
		}
		if modelName == "" {
			modelName = extractEmbeddingModelFromTags(resource.Tags)
		}
		if modelName == "" {
			modelName = interfaces.SmallModelTypeEmbedding
		}
		s.setEmbeddingModelName(modelName)
		initialized = true
		s.logger.WithContext(ctx).Infof("resource already exists, resource_id=%s, embedding_model=%s", datasetID, modelName)
		return nil
	}
	// When creating a dataset for the first time: use the system default embedding model (interface configurable); if the default is not configured, it will fall back to the name "embedding".
	embeddingModel, err := s.resolveBuildEmbeddingModel(ctx)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("resolve embedding model failed, resource_id=%s, err=%v", datasetID, err)
		return err
	}
	s.logger.WithContext(ctx).Infof("creating skill dataset resource, resource_id=%s, catalog_id=%s, embedding_model=%s, dimension=%d",
		datasetID, catalogID, embeddingModel.ModelName, embeddingModel.EmbeddingDim)
	_, err = s.vegaClient.CreateResource(ctx, &interfaces.VegaResourceRequest{
		ID:               datasetID,
		CatalogID:        catalogID,
		Name:             datasetID,
		Tags:             []string{"execution-factory", "skill", "索引"},
		Description:      executionFactoryDatasetDesc,
		Category:         "dataset",
		Status:           executionFactoryDatasetStatus,
		SourceIdentifier: datasetID,
		SchemaDefinition: buildSkillIndexSchema(embeddingModel.EmbeddingDim),
		// The model name locked at build time is snapshotted into the resource level index_config: vega. Use it when parsing the vector model.
		// And it does not enter OpenSearch mapping. You can't put tags (vega tag verification prohibits ':', which will result in 400), and you can't.
		// Feature config with vector attributes (will be copied into knn_vector mapping, rejected by OpenSearch).
		IndexConfig: &interfaces.VegaResourceIndexConfig{DefaultEmbeddingModel: embeddingModel.ModelName},
	})
	if err != nil {
		s.logger.WithContext(ctx).Errorf("create skill dataset resource failed, resource_id=%s, err=%v", datasetID, err)
		return err
	}
	s.setEmbeddingModelName(embeddingModel.ModelName)
	initialized = true
	return nil
}

// ensureCatalog parses and ensures the existence of the built-in catalog and returns the catalog ID actually used by this process.
func (s *skillIndexSync) ensureCatalog(ctx context.Context) (string, error) {
	catalog, err := s.vegaClient.GetCatalogByID(ctx, executionFactoryCatalogID)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("get catalog failed during ensure dataset, catalog_id=%s, err=%v", executionFactoryCatalogID, err)
		return "", err
	}
	if catalog == nil {
		s.logger.WithContext(ctx).Infof("catalog not found, creating catalog, catalog_id=%s", executionFactoryCatalogID)
		_, err = s.vegaClient.CreateCatalog(ctx, &interfaces.VegaCatalogRequest{
			ID:          executionFactoryCatalogID,
			Name:        executionFactoryCatalogID,
			Tags:        []string{"execution-factory", "索引", internalCatalogTag},
			Description: executionFactoryCatalogDesc,
			// System internal catalog: only visible to super administrators, the catalog:* authorization of business roles (data administrators, etc.) cannot match.
			Internal: true,
			// If the logical directory is disabled, the reading and writing of the dataset under it will be blocked by vega with 409.
			// Catalog.IsDisabled is rejected (the built-in catalog of bkn-backend is also explicitly set to true)
			Enabled: true,
		})
		if err != nil {
			s.logger.WithContext(ctx).Errorf("create catalog failed, catalog_id=%s, err=%v", executionFactoryCatalogID, err)
			return "", err
		}
		return executionFactoryCatalogID, nil
	}
	if err := s.reconcileCatalog(ctx, catalog); err != nil {
		return "", err
	}
	return catalog.ID, nil
}

// reconcileCatalog aligns the inventory catalog to the current expectations: moves the display name to the new brand name, adds internal tags,
// Directory is enabled.
//
// Renaming and adding labels are display items, and only alerts if they fail; "Enable" is a functional item - when the directory is disabled, the dataset below it.
// The read and write will be rejected by vega with 409, so the activation failure must bubble up into Init failure and hand it over to the retry loop.
// Otherwise, it will be marked as initialized with a status of inevitable write failure.
func (s *skillIndexSync) reconcileCatalog(ctx context.Context, catalog *interfaces.VegaCatalog) error {
	tags := appendInternalTag(catalog.Tags)
	// The upper limit of vega's tag number is 5; when the limit is exceeded, give up adding tags and keep the main goal of "renaming".
	// Otherwise, the entire PUT will be 400, and the name change and label supplementation will fail permanently.
	if len(tags) > vegaMaxTags {
		s.logger.WithContext(ctx).Warnf("skip internal tag backfill, tag limit reached, catalog_id=%s, tags=%d", catalog.ID, len(tags))
		tags = catalog.Tags
	}
	if catalog.Name != executionFactoryCatalogID || len(tags) != len(catalog.Tags) {
		req := &interfaces.VegaCatalogRequest{
			ID:                 catalog.ID,
			Name:               executionFactoryCatalogID,
			Tags:               tags,
			Description:        catalog.Description,
			ExpectedUpdateTime: catalog.UpdateTime,
			Internal:           true,
			Enabled:            catalog.Enabled,
			ConnectorType:      catalog.ConnectorType,
		}
		if err := s.vegaClient.UpdateCatalog(ctx, req); err != nil {
			s.logger.WithContext(ctx).Warnf("reconcile catalog failed, catalog_id=%s, name=%s, err=%v", catalog.ID, executionFactoryCatalogID, err)
		} else {
			s.logger.WithContext(ctx).Infof("catalog reconciled, catalog_id=%s, name=%s, tags=%v", catalog.ID, executionFactoryCatalogID, tags)
		}
	}
	return s.ensureCatalogEnabled(ctx, catalog)
}

// ensureCatalogEnabled ensures that the catalog is enabled. Reading and writing of the dataset under the directory when it is disabled.
// Will be rejected by vega with 409 Catalog.IsDisabled, so failure will be returned as an error.
func (s *skillIndexSync) ensureCatalogEnabled(ctx context.Context, catalog *interfaces.VegaCatalog) error {
	if catalog.Enabled {
		return nil
	}
	if err := s.vegaClient.EnableCatalog(ctx, catalog.ID); err != nil {
		s.logger.WithContext(ctx).Errorf("enable catalog failed, catalog_id=%s, err=%v", catalog.ID, err)
		return err
	}
	s.logger.WithContext(ctx).Infof("catalog enabled, catalog_id=%s", catalog.ID)
	return nil
}

// ensureDatasetCatalogEnabled ensures that "the directory where the adopted dataset is hung" is enabled.
//
// In mixed form, the two may not be the same directory: ensureCatalog resolves to the directory of the new ID, while dataset.
// Still hanging on the old directory. Writing is governed by the dataset's parent directory, and simply enabling the former is not enough.
func (s *skillIndexSync) ensureDatasetCatalogEnabled(ctx context.Context, resource *interfaces.VegaResource, resolvedCatalogID string) error {
	if resource == nil || resource.CatalogID == "" || resource.CatalogID == resolvedCatalogID {
		return nil
	}
	parent, err := s.vegaClient.GetCatalogByID(ctx, resource.CatalogID)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("get dataset parent catalog failed, resource_id=%s, catalog_id=%s, err=%v", resource.ID, resource.CatalogID, err)
		return err
	}
	if parent == nil {
		return fmt.Errorf("skill dataset %s points to missing catalog %s", resource.ID, resource.CatalogID)
	}
	s.logger.WithContext(ctx).Infof("skill dataset lives in another catalog, resource_id=%s, catalog_id=%s", resource.ID, parent.ID)
	return s.ensureCatalogEnabled(ctx, parent)
}

// appendInternalTag appends the internal tag; if it already exists (ignoring case and leading and trailing spaces), it will be returned as is.
func appendInternalTag(tags []string) []string {
	for _, tag := range tags {
		if strings.EqualFold(strings.TrimSpace(tag), internalCatalogTag) {
			return tags
		}
	}
	return append(append([]string{}, tags...), internalCatalogTag)
}

// resolveDataset resolves the skill dataset used by this process. The returned resource is nil.
// It does not exist yet and needs to be created.
func (s *skillIndexSync) resolveDataset(ctx context.Context) (string, *interfaces.VegaResource, error) {
	resource, err := s.vegaClient.GetResourceByID(ctx, executionFactorySkillDataset)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("get resource failed during ensure dataset, resource_id=%s, err=%v", executionFactorySkillDataset, err)
		return "", nil, err
	}
	if resource != nil {
		return resource.ID, resource, nil
	}
	return executionFactorySkillDataset, nil, nil
}

// resolveBuildEmbeddingModel determines the embedding model when building a dataset: the system default (interface type) is given priority, and if not configured, it falls back to the name "embedding" (behavior before transformation).
func (s *skillIndexSync) resolveBuildEmbeddingModel(ctx context.Context) (*interfaces.EmbeddingModel, error) {
	model, err := s.modelManager.GetDefaultEmbeddingModel(ctx, interfaces.SmallModelTypeEmbedding)
	if err != nil {
		s.logger.WithContext(ctx).Warnf("get default embedding model failed, fallback to named '%s': %v", interfaces.SmallModelTypeEmbedding, err)
	} else if model != nil && model.EmbeddingDim > 0 {
		return model, nil
	}
	return s.modelManager.GetEmbeddingModel(ctx, interfaces.SmallModelTypeEmbedding, interfaces.SmallModelTypeEmbedding)
}

// extractEmbeddingModelFromIndexConfig reads back the model name locked at build time from resource-level index_config (current writing method)
func extractEmbeddingModelFromIndexConfig(indexConfig *interfaces.VegaResourceIndexConfig) string {
	if indexConfig == nil {
		return ""
	}
	return indexConfig.DefaultEmbeddingModel
}

// extractEmbeddingModelFromSchema reads back the model name from the vector feature's config.embedding_model.
// It only serves the dataset that has been written to this location briefly. When creating a new dataset, the model name will no longer be written into the schema.
func extractEmbeddingModelFromSchema(schema []interfaces.VegaProperty) string {
	for _, property := range schema {
		for _, feature := range property.Features {
			if feature.FeatureType != "vector" || feature.Config == nil {
				continue
			}
			if name, ok := feature.Config[embeddingModelConfigKey].(string); ok && name != "" {
				return name
			}
		}
	}
	return ""
}

// extractEmbeddingModelFromTags parses the build-time locked embedding model names from resource tags.
// It only serves the old dataset created during the tag snapshot period, and the new dataset will no longer write this tag.
func extractEmbeddingModelFromTags(tags []string) string {
	for _, t := range tags {
		if strings.HasPrefix(t, embeddingModelTagPrefix) {
			return strings.TrimPrefix(t, embeddingModelTagPrefix)
		}
	}
	return ""
}

func (s *skillIndexSync) getEmbeddingModelName() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.embeddingModelName == "" {
		return interfaces.SmallModelTypeEmbedding
	}
	return s.embeddingModelName
}

func (s *skillIndexSync) setEmbeddingModelName(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.embeddingModelName = name
}

// getDatasetID returns the dataset ID actually used by this process; if it is not parsed, it takes the new default value.
func (s *skillIndexSync) getDatasetID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.datasetID == "" {
		return executionFactorySkillDataset
	}
	return s.datasetID
}

func (s *skillIndexSync) setDatasetID(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.datasetID = id
}

func (s *skillIndexSync) UpsertSkill(ctx context.Context, skill *model.SkillRepositoryDB) error {
	log := s.logger
	if !s.isInitialized() {
		log.WithContext(ctx).Warnf("skip skill index upsert because dataset is not initialized, skill_id=%s", skill.SkillID)
		return nil
	}
	document, err := s.buildSkillDocument(ctx, skill)
	if err != nil {
		log.Errorf("build skill index document failed, skill_id=%s, err=%v", skill.SkillID, err)
		return err
	}
	datasetID := s.getDatasetID()
	log.Infof("upsert skill index document, skill_id=%s, resource_id=%s", skill.SkillID, datasetID)
	if err = s.vegaClient.WriteDatasetDocuments(ctx, datasetID, []map[string]any{document}); err != nil {
		log.Errorf("write skill index document failed, skill_id=%s, err=%v", skill.SkillID, err)
		return err
	}
	return nil
}

func (s *skillIndexSync) UpdateSkill(ctx context.Context, skill *model.SkillRepositoryDB) error {
	log := s.logger
	if !s.isInitialized() {
		log.WithContext(ctx).Warnf("skip skill index update because dataset is not initialized, skill_id=%s", skill.SkillID)
		return nil
	}
	document, err := s.buildSkillDocument(ctx, skill)
	if err != nil {
		log.Errorf("build skill index document failed, skill_id=%s, err=%v", skill.SkillID, err)
		return err
	}
	datasetID := s.getDatasetID()
	log.Infof("update skill index document, skill_id=%s, resource_id=%s", skill.SkillID, datasetID)
	if err = s.vegaClient.UpdateDatasetDocuments(ctx, datasetID, []map[string]any{document}); err != nil {
		log.Errorf("update skill index document failed, skill_id=%s, err=%v", skill.SkillID, err)
		return err
	}
	return nil
}

func (s *skillIndexSync) DeleteSkill(ctx context.Context, skillID string) error {
	if !s.isInitialized() {
		s.logger.WithContext(ctx).Warnf("skip skill index delete because dataset is not initialized, skill_id=%s", skillID)
		return nil
	}
	datasetID := s.getDatasetID()
	s.logger.Infof("delete skill index document, skill_id=%s, resource_id=%s", skillID, datasetID)
	if err := s.vegaClient.DeleteDatasetDocumentByID(ctx, datasetID, skillID); err != nil {
		s.logger.WithContext(ctx).Errorf("delete skill index document failed, skill_id=%s, err=%v", skillID, err)
		return err
	}
	return nil
}

func (s *skillIndexSync) isInitialized() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.initialized
}

func (s *skillIndexSync) setInitialized(initialized bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initialized = initialized
}

func (s *skillIndexSync) retryInit() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	s.logger.Warn("skill index sync service init retry loop started")
	for range ticker.C {
		if err := s.Init(context.Background()); err != nil {
			s.logger.Warnf("retry init skill index sync service failed, error: %v", err)
			continue
		}
		s.logger.Info("skill index sync service init retry succeeded")
		return
	}
}

func (s *skillIndexSync) buildSkillDocument(ctx context.Context, skill *model.SkillRepositoryDB) (map[string]any, error) {
	log := s.logger
	log.Infof("build skill index document, skill_id=%s", skill.SkillID)
	// Read back the model locked when creating the dataset (build model == check model), instead of retrieving the current system default each time.
	embeddingResp, err := s.modelAPI.Embeddings(ctx, &interfaces.EmbeddingReq{
		Model: s.getEmbeddingModelName(),
		Input: []string{buildEmbeddingInput(skill.Name, skill.Description)},
	})
	if err != nil {
		log.Errorf("get skill embedding failed, skill_id=%s, err=%v", skill.SkillID, err)
		return nil, err
	}
	if embeddingResp == nil || len(embeddingResp.Data) == 0 || len(embeddingResp.Data[0].Embedding) == 0 {
		log.Errorf("empty skill embedding result, skill_id=%s", skill.SkillID)
		return nil, fmt.Errorf("embedding result is empty")
	}

	return map[string]any{
		"_id":         skill.SkillID,
		"id":          skill.SkillID,
		"skill_id":    skill.SkillID,
		"name":        skill.Name,
		"description": skill.Description,
		"version":     skill.Version,
		"category":    skill.Category,
		"create_user": skill.CreateUser,
		"create_time": skill.CreateTime,
		"update_user": skill.UpdateUser,
		"update_time": skill.UpdateTime,
		"_vector":     embeddingResp.Data[0].Embedding,
	}, nil
}

func buildEmbeddingInput(name string, description string) string {
	parts := []string{name, description}
	return strings.Join(parts, "\n")
}

// buildSkillIndexSchema Builds the skill index schema. The model name does not enter here - it is a vector attribute.
// The feature config will be copied into OpenSearch mapping by vega as it is, and extra keys will cause index building to fail.
func buildSkillIndexSchema(dimension int) []interfaces.VegaProperty {
	return []interfaces.VegaProperty{
		{
			Name:         "skill_id",
			Type:         "string",
			DisplayName:  "skill_id",
			OriginalName: "skill_id",
			Description:  "Skill 业务主键",
			Features: []interfaces.VegaPropertyFeature{{
				Name:        "keyword_skill_id",
				DisplayName: "keyword_skill_id",
				FeatureType: "keyword",
				Description: "Skill ID 精确过滤",
				IsDefault:   true,
				IsNative:    false,
				Config:      map[string]any{"ignore_above": 1024},
			}},
		},
		{
			Name:         "name",
			Type:         "text",
			DisplayName:  "name",
			OriginalName: "name",
			Description:  "Skill 名称",
			Features: []interfaces.VegaPropertyFeature{
				{
					Name:        "keyword_name",
					DisplayName: "keyword_name",
					FeatureType: "keyword",
					Description: "Skill 名称的关键词特征",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{"ignore_above": 1024},
				},
				{
					Name:        "fulltext_name",
					DisplayName: "fulltext_name",
					FeatureType: "fulltext",
					Description: "Skill 名称全文检索",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{"analyzer": "standard"},
				}},
		},
		{
			Name:         "description",
			Type:         "text",
			DisplayName:  "description",
			OriginalName: "description",
			Description:  "Skill 描述",
			Features: []interfaces.VegaPropertyFeature{
				{
					Name:        "keyword_description",
					DisplayName: "keyword_description",
					FeatureType: "keyword",
					Description: "Skill 描述的关键词特征",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{"ignore_above": 1024},
				},
				{
					Name:        "fulltext_description",
					DisplayName: "fulltext_description",
					FeatureType: "fulltext",
					Description: "Skill 描述全文检索",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{"analyzer": "standard"},
				}},
		},
		{
			Name:         "version",
			Type:         "string",
			DisplayName:  "version",
			OriginalName: "version",
			Description:  "Skill 版本",
			Features: []interfaces.VegaPropertyFeature{{
				Name:        "keyword_version",
				DisplayName: "keyword_version",
				FeatureType: "keyword",
				Description: "Skill 版本精确过滤",
				IsDefault:   true,
				IsNative:    false,
				Config:      map[string]any{"ignore_above": 1024},
			}},
		},
		{
			Name:         "category",
			Type:         "string",
			DisplayName:  "category",
			OriginalName: "category",
			Description:  "Skill 分类",
			Features: []interfaces.VegaPropertyFeature{{
				Name:        "keyword_category",
				DisplayName: "keyword_category",
				FeatureType: "keyword",
				Description: "Skill 分类精确过滤",
				IsDefault:   true,
				IsNative:    false,
				Config:      map[string]any{"ignore_above": 1024},
			}},
		},
		{
			Name:         "create_user",
			Type:         "string",
			DisplayName:  "create_user",
			OriginalName: "create_user",
			Description:  "创建人",
			Features: []interfaces.VegaPropertyFeature{{
				Name:        "keyword_create_user",
				DisplayName: "keyword_create_user",
				FeatureType: "keyword",
				Description: "创建人精确过滤",
				IsDefault:   true,
				IsNative:    false,
				Config:      map[string]any{"ignore_above": 1024},
			}},
		},
		{
			Name:         "create_time",
			Type:         "datetime",
			DisplayName:  "create_time",
			OriginalName: "create_time",
			Description:  "创建时间",
		},
		{
			Name:         "update_user",
			Type:         "string",
			DisplayName:  "update_user",
			OriginalName: "update_user",
			Description:  "更新人",
			Features: []interfaces.VegaPropertyFeature{{
				Name:        "keyword_update_user",
				DisplayName: "keyword_update_user",
				FeatureType: "keyword",
				Description: "更新人精确过滤",
				IsDefault:   true,
				IsNative:    false,
				Config:      map[string]any{"ignore_above": 1024},
			}},
		},
		{
			Name:         "update_time",
			Type:         "datetime",
			DisplayName:  "update_time",
			OriginalName: "update_time",
			Description:  "更新时间",
		},
		{
			Name:         "_vector",
			Type:         "vector",
			DisplayName:  "_vector",
			OriginalName: "_vector",
			Description:  "Skill 名称与描述向量",
			Features: []interfaces.VegaPropertyFeature{{
				Name:        "vector_skill",
				DisplayName: "vector_skill",
				FeatureType: "vector",
				Description: "Skill 语义检索向量",
				IsDefault:   true,
				IsNative:    false,
				Config: map[string]any{
					"dimension": dimension,
					"method": map[string]any{
						"name":       "hnsw",
						"space_type": "cosinesimil",
						"engine":     "lucene",
						"parameters": map[string]any{
							"ef_construction": 256,
							"m":               48,
						},
					},
				},
			}},
		},
	}
}
