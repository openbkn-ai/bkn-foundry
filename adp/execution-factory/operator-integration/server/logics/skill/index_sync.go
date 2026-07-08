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
	executionFactoryCatalogID     = "kweaver_execution_factory_catalog"
	executionFactoryCatalogDesc   = "执行工厂的逻辑命名空间"
	executionFactorySkillDataset  = "kweaver_execution_factory_skill_dataset"
	executionFactoryDatasetDesc   = "执行工厂的Skill索引数据集"
	executionFactoryDatasetStatus = "active"
	// embeddingModelTagPrefix 把"建 dataset 时锁定的 embedding 模型名"快照进 resource tag，
	// 重启/实时同步时读回，保证写入向量用的模型与建索引时一致(建模型==查模型)。
	embeddingModelTagPrefix = "embedding_model:"
)

type skillIndexSync struct {
	modelManager interfaces.MFModelManager
	modelAPI     interfaces.MFModelAPIClient
	vegaClient   interfaces.VegaBackendClient
	logger       interfaces.Logger
	mu           sync.RWMutex
	initialized  bool
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
	catalog, err := s.vegaClient.GetCatalogByID(ctx, executionFactoryCatalogID)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("get catalog failed during ensure dataset, catalog_id=%s, err=%v", executionFactoryCatalogID, err)
		return err
	}
	if catalog == nil {
		s.logger.WithContext(ctx).Infof("catalog not found, creating catalog, catalog_id=%s", executionFactoryCatalogID)
		_, err = s.vegaClient.CreateCatalog(ctx, &interfaces.VegaCatalogRequest{
			ID:          executionFactoryCatalogID,
			Name:        executionFactoryCatalogID,
			Tags:        []string{"execution-factory", "索引"},
			Description: executionFactoryCatalogDesc,
			// 系统内部目录：仅超级管理员可见，业务角色（数据管理员等）的 catalog:* 授权匹配不到
			Internal: true,
		})
		if err != nil {
			s.logger.WithContext(ctx).Errorf("create catalog failed, catalog_id=%s, err=%v", executionFactoryCatalogID, err)
			return err
		}
	}

	resource, err := s.vegaClient.GetResourceByID(ctx, executionFactorySkillDataset)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("get resource failed during ensure dataset, resource_id=%s, err=%v", executionFactorySkillDataset, err)
		return err
	}
	if resource != nil {
		// dataset 已存在：从 tag 读回建时锁定的模型名(建模型==查模型)。
		// 旧 dataset(改造前创建)无该 tag，回退到按名 "embedding"，与改造前行为一致。
		modelName := extractEmbeddingModelFromTags(resource.Tags)
		if modelName == "" {
			modelName = interfaces.SmallModelTypeEmbedding
		}
		s.setEmbeddingModelName(modelName)
		initialized = true
		s.logger.WithContext(ctx).Infof("resource already exists, resource_id=%s, embedding_model=%s", executionFactorySkillDataset, modelName)
		return nil
	}
	// 首次创建 dataset：用系统默认 embedding 模型(接口式可配)；未配置默认时回退按名 "embedding"。
	embeddingModel, err := s.resolveBuildEmbeddingModel(ctx)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("resolve embedding model failed, resource_id=%s, err=%v", executionFactorySkillDataset, err)
		return err
	}
	s.logger.WithContext(ctx).Infof("creating skill dataset resource, resource_id=%s, embedding_model=%s, dimension=%d",
		executionFactorySkillDataset, embeddingModel.ModelName, embeddingModel.EmbeddingDim)
	_, err = s.vegaClient.CreateResource(ctx, &interfaces.VegaResourceRequest{
		ID:        executionFactorySkillDataset,
		CatalogID: executionFactoryCatalogID,
		Name:      executionFactorySkillDataset,
		// 把建时锁定的模型名快照进 tag，供重启/实时同步读回
		Tags:             []string{"execution-factory", "skill", "索引", embeddingModelTagPrefix + embeddingModel.ModelName},
		Description:      executionFactoryDatasetDesc,
		Category:         "dataset",
		Status:           executionFactoryDatasetStatus,
		SourceIdentifier: executionFactorySkillDataset,
		SchemaDefinition: buildSkillIndexSchema(embeddingModel.EmbeddingDim),
	})
	if err != nil {
		s.logger.WithContext(ctx).Errorf("create skill dataset resource failed, resource_id=%s, err=%v", executionFactorySkillDataset, err)
		return err
	}
	s.setEmbeddingModelName(embeddingModel.ModelName)
	initialized = true
	return nil
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

// extractEmbeddingModelFromTags 从 resource tags 解析建时锁定的 embedding 模型名
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
	log.Infof("upsert skill index document, skill_id=%s, resource_id=%s", skill.SkillID, executionFactorySkillDataset)
	if err = s.vegaClient.WriteDatasetDocuments(ctx, executionFactorySkillDataset, []map[string]any{document}); err != nil {
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
	log.Infof("update skill index document, skill_id=%s, resource_id=%s", skill.SkillID, executionFactorySkillDataset)
	if err = s.vegaClient.UpdateDatasetDocuments(ctx, executionFactorySkillDataset, []map[string]any{document}); err != nil {
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
	s.logger.Infof("delete skill index document, skill_id=%s, resource_id=%s", skillID, executionFactorySkillDataset)
	if err := s.vegaClient.DeleteDatasetDocumentByID(ctx, executionFactorySkillDataset, skillID); err != nil {
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
