package skill

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/drivenadapters"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-comm-go/otel/oteltrace"
)

const (
	executionFactoryCatalogID     = "bkn_execution_factory_catalog"
	executionFactoryCatalogDesc   = "执行工厂的逻辑命名空间"
	executionFactorySkillDataset  = "bkn_execution_factory_skill_dataset"
	executionFactoryDatasetDesc   = "执行工厂的Skill索引数据集"
	executionFactoryDatasetStatus = "active"
	// legacy* 是 kweaver 品牌期建的内置目录/数据集 ID(issue #372)。
	// 存量环境沿用旧 ID：ID 是索引数据的落点，换 ID 等于重建索引，
	// 因此只把「展示名」迁到新品牌名，ID 保持不动、不产生两套目录。
	legacyExecutionFactoryCatalogID    = "kweaver_execution_factory_catalog"
	legacyExecutionFactorySkillDataset = "kweaver_execution_factory_skill_dataset"
	// internalCatalogTag 让内置目录自带「内置」语义标签。Studio 目前不读后端的
	// internal 字段，靠 metadata/tag/名称前缀启发式判定内置目录，该 tag 命中它的
	// 内置标签集合 —— 前端零改就能正确显示「内置」并收起管理操作。
	internalCatalogTag = "internal"
	// vegaMaxTags 与 vega 的 TAGS_MAX_NUMBER 对齐(超出会 400)
	vegaMaxTags = 5
	// embeddingModelConfigKey 是向量特征 config 里的模型键。只用于读：曾经把模型
	// 快照写在这里，但 vega 会把向量属性的 feature config 原样拷进 OpenSearch
	// knn_vector mapping，OpenSearch 以 unknown parameter 拒绝，索引建不出来。
	// 写路径改用资源级 index_config.default_embedding_model。
	embeddingModelConfigKey = "embedding_model"
	// embeddingModelTagPrefix 是该快照的旧载体(resource tag)。vega 的 tag 校验禁掉了
	// ':'，带这种 tag 的建 dataset 请求会 400，因此只保留读路径兼容老 dataset。
	embeddingModelTagPrefix = "embedding_model:"
)

type skillIndexSync struct {
	modelManager interfaces.MFModelManager
	modelAPI     interfaces.MFModelAPIClient
	vegaClient   interfaces.VegaBackendClient
	logger       interfaces.Logger
	mu           sync.RWMutex
	initialized  bool
	// datasetID 为本进程实际使用的数据集 ID：新装是 bkn_*，存量环境解析到
	// legacy 的 kweaver_*；空值表示尚未解析，取新装默认值。
	datasetID string
	// embeddingModelName 该系统 skill dataset 建时锁定的 embedding 模型名(系统默认快照)，
	// 受 mu 保护；upsert 读回它生成向量，而非每次重取当前默认。
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

// EnsureDataset 确保Skill索引数据集存在
// 如果不存在，则创建
// 如果存在，则检查是否为最新版本
// 如果不是最新版本，则更新
// 如果是最新版本，则返回成功
func (s *skillIndexSync) Init(ctx context.Context) (err error) {
	// 记录可观测
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
		// dataset 已存在：读回建时锁定的模型名(建模型==查模型)。
		// 旧 dataset 两种形态都兜住，都取不到时回退按名 "embedding"，与改造前行为一致。
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
	// 首次创建 dataset：用系统默认 embedding 模型(接口式可配)；未配置默认时回退按名 "embedding"。
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
		// 建时锁定的模型名快照进资源级 index_config：vega 解析向量模型时拿它兜底，
		// 且它不进 OpenSearch mapping。不能放 tag(vega tag 校验禁 ':' 会 400)，也不能
		// 放向量属性的 feature config(会被拷进 knn_vector mapping，OpenSearch 拒绝)。
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

// ensureCatalog 解析并保证内置目录存在，返回本进程实际使用的目录 ID。
//
// 新装取 bkn_execution_factory_catalog；存量环境(kweaver 品牌期建的目录)沿用旧
// ID，只把展示名迁到新品牌名 —— 换 ID 会新建一套目录并让已建索引失联，
// 见 issue #372。
func (s *skillIndexSync) ensureCatalog(ctx context.Context) (string, error) {
	catalog, err := s.vegaClient.GetCatalogByID(ctx, executionFactoryCatalogID)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("get catalog failed during ensure dataset, catalog_id=%s, err=%v", executionFactoryCatalogID, err)
		return "", err
	}
	if catalog == nil {
		legacy, err := s.vegaClient.GetCatalogByID(ctx, legacyExecutionFactoryCatalogID)
		if err != nil {
			s.logger.WithContext(ctx).Errorf("get legacy catalog failed, catalog_id=%s, err=%v", legacyExecutionFactoryCatalogID, err)
			return "", err
		}
		if legacy != nil {
			s.logger.WithContext(ctx).Infof("adopting legacy catalog, catalog_id=%s", legacy.ID)
			s.reconcileCatalog(ctx, legacy)
			return legacy.ID, nil
		}
		s.logger.WithContext(ctx).Infof("catalog not found, creating catalog, catalog_id=%s", executionFactoryCatalogID)
		_, err = s.vegaClient.CreateCatalog(ctx, &interfaces.VegaCatalogRequest{
			ID:          executionFactoryCatalogID,
			Name:        executionFactoryCatalogID,
			Tags:        []string{"execution-factory", "索引", internalCatalogTag},
			Description: executionFactoryCatalogDesc,
			// 系统内部目录：仅超级管理员可见，业务角色（数据管理员等）的 catalog:* 授权匹配不到
			Internal: true,
			// 逻辑目录若建成 disabled，其下 dataset 的读写会被 vega 以 409
			// Catalog.IsDisabled 拒绝(bkn-backend 的内置目录同样显式置 true)
			Enabled: true,
		})
		if err != nil {
			s.logger.WithContext(ctx).Errorf("create catalog failed, catalog_id=%s, err=%v", executionFactoryCatalogID, err)
			return "", err
		}
		return executionFactoryCatalogID, nil
	}
	s.reconcileCatalog(ctx, catalog)
	return catalog.ID, nil
}

// reconcileCatalog 把存量目录对齐到当前预期：展示名迁到新品牌名、补 internal 标签、
// 目录置为启用。三个动作都是尽力而为——失败只告警不阻断启动，索引读写本身不依赖
// 它们成功。
func (s *skillIndexSync) reconcileCatalog(ctx context.Context, catalog *interfaces.VegaCatalog) {
	tags := appendInternalTag(catalog.Tags)
	// vega 的 tag 数量上限是 5；超限时放弃补标签，保住「改名」这个主目标，
	// 否则整个 PUT 会 400，改名和补标签一起永久失败。
	if len(tags) > vegaMaxTags {
		s.logger.WithContext(ctx).Warnf("skip internal tag backfill, tag limit reached, catalog_id=%s, tags=%d", catalog.ID, len(tags))
		tags = catalog.Tags
	}
	if catalog.Name != executionFactoryCatalogID || len(tags) != len(catalog.Tags) {
		req := &interfaces.VegaCatalogRequest{
			ID:            catalog.ID,
			Name:          executionFactoryCatalogID,
			Tags:          tags,
			Description:   catalog.Description,
			Internal:      true,
			Enabled:       catalog.Enabled,
			ConnectorType: catalog.ConnectorType,
		}
		if err := s.vegaClient.UpdateCatalog(ctx, req); err != nil {
			s.logger.WithContext(ctx).Warnf("reconcile catalog failed, catalog_id=%s, name=%s, err=%v", catalog.ID, executionFactoryCatalogID, err)
		} else {
			s.logger.WithContext(ctx).Infof("catalog reconciled, catalog_id=%s, name=%s, tags=%v", catalog.ID, executionFactoryCatalogID, tags)
		}
	}
	if !catalog.Enabled {
		if err := s.vegaClient.EnableCatalog(ctx, catalog.ID); err != nil {
			s.logger.WithContext(ctx).Warnf("enable catalog failed, catalog_id=%s, err=%v", catalog.ID, err)
		} else {
			s.logger.WithContext(ctx).Infof("catalog enabled, catalog_id=%s", catalog.ID)
		}
	}
}

// appendInternalTag 补上 internal 标签；已有(忽略大小写与首尾空格)则原样返回。
func appendInternalTag(tags []string) []string {
	for _, tag := range tags {
		if strings.EqualFold(strings.TrimSpace(tag), internalCatalogTag) {
			return tags
		}
	}
	return append(append([]string{}, tags...), internalCatalogTag)
}

// resolveDataset 解析本进程使用的 skill dataset：新装取 bkn_*，存量环境沿用
// kweaver_* 旧 ID(只迁展示名)。返回的 resource 为 nil 表示两者都不存在，需新建。
func (s *skillIndexSync) resolveDataset(ctx context.Context) (string, *interfaces.VegaResource, error) {
	resource, err := s.vegaClient.GetResourceByID(ctx, executionFactorySkillDataset)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("get resource failed during ensure dataset, resource_id=%s, err=%v", executionFactorySkillDataset, err)
		return "", nil, err
	}
	if resource != nil {
		return resource.ID, resource, nil
	}
	legacy, err := s.vegaClient.GetResourceByID(ctx, legacyExecutionFactorySkillDataset)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("get legacy resource failed, resource_id=%s, err=%v", legacyExecutionFactorySkillDataset, err)
		return "", nil, err
	}
	if legacy != nil {
		// 只收养、不改名：vega 的 Update 无条件对「已存的」schema 跑模型校验，而所有
		// 存量 dataset 的 _vector 都没有 embedding_model(快照机制之前建的)，vega 回退
		// 到常量 "embedding" 并因该模型未注册而 400 —— 改名请求对这批 dataset 必然
		// 失败(VM 实测)。dataset 藏在内置目录下、仅超管可见，旧显示名无碍。
		s.logger.WithContext(ctx).Infof("adopting legacy skill dataset, resource_id=%s", legacy.ID)
		return legacy.ID, legacy, nil
	}
	return executionFactorySkillDataset, nil, nil
}

// resolveBuildEmbeddingModel 建 dataset 时确定 embedding 模型：优先系统默认(接口式)，未配置则回退按名 "embedding"(改造前行为)。
func (s *skillIndexSync) resolveBuildEmbeddingModel(ctx context.Context) (*interfaces.EmbeddingModel, error) {
	model, err := s.modelManager.GetDefaultEmbeddingModel(ctx, interfaces.SmallModelTypeEmbedding)
	if err != nil {
		s.logger.WithContext(ctx).Warnf("get default embedding model failed, fallback to named '%s': %v", interfaces.SmallModelTypeEmbedding, err)
	} else if model != nil && model.EmbeddingDim > 0 {
		return model, nil
	}
	return s.modelManager.GetEmbeddingModel(ctx, interfaces.SmallModelTypeEmbedding, interfaces.SmallModelTypeEmbedding)
}

// extractEmbeddingModelFromIndexConfig 从资源级 index_config 读回建时锁定的模型名(当前写法)
func extractEmbeddingModelFromIndexConfig(indexConfig *interfaces.VegaResourceIndexConfig) string {
	if indexConfig == nil {
		return ""
	}
	return indexConfig.DefaultEmbeddingModel
}

// extractEmbeddingModelFromSchema 从向量特征的 config.embedding_model 读回模型名。
// 只服务于短暂写过该位置的 dataset，新建 dataset 不再往 schema 里写模型名。
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

// extractEmbeddingModelFromTags 从 resource tags 解析建时锁定的 embedding 模型名。
// 只服务于 tag 快照时期建的老 dataset，新建 dataset 不再写这个 tag。
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

// getDatasetID 返回本进程实际使用的 dataset ID；未解析时取新装默认值。
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
	// 读回建 dataset 时锁定的模型(建模型==查模型)，而非每次重取当前系统默认
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

// buildSkillIndexSchema 生成 skill 索引 schema。模型名不进这里 —— 向量属性的
// feature config 会被 vega 原样拷进 OpenSearch mapping，多余键会让建索引失败。
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
				RefProperty: "skill_id",
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
					RefProperty: "name",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{"ignore_above": 1024},
				},
				{
					Name:        "fulltext_name",
					DisplayName: "fulltext_name",
					FeatureType: "fulltext",
					Description: "Skill 名称全文检索",
					RefProperty: "name",
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
					RefProperty: "description",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{"ignore_above": 1024},
				},
				{
					Name:        "fulltext_description",
					DisplayName: "fulltext_description",
					FeatureType: "fulltext",
					Description: "Skill 描述全文检索",
					RefProperty: "description",
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
				RefProperty: "version",
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
				RefProperty: "category",
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
				RefProperty: "create_user",
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
				RefProperty: "update_user",
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
				RefProperty: "_vector",
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
