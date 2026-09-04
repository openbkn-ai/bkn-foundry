package skill

import (
	"context"
	"net/http"
	"strings"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/drivenadapters"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
)

const (
	defaultSkillSearchTopK = 10
	maxSkillSearchTopK     = 100
	// maxSkillSearchWhitelist bounds the terms filter. OpenSearch accepts far more
	// (index.max_terms_count defaults to 65536); this is about keeping one request small enough
	// to stay debuggable. Past this point the caller should resolve the whitelist server-side.
	maxSkillSearchWhitelist = 1000
	// vegaPagingModeSingle is the one-page read mode. Vega rejects anything but "single" or
	// "cursor" with 400 paging.mode must be either single or cursor.
	vegaPagingModeSingle = "single"
)

var (
	skillSearchOnce sync.Once
	skillSearchInst interfaces.SkillSearchService
)

type skillSearchService struct {
	logger       interfaces.Logger
	vegaClient   interfaces.VegaBackendClient
	modelManager interfaces.MFModelManager
	modelAPI     interfaces.MFModelAPIClient
}

func NewSkillSearchService() interfaces.SkillSearchService {
	skillSearchOnce.Do(func() {
		conf := config.NewConfigLoader()
		skillSearchInst = &skillSearchService{
			logger:       conf.GetLogger(),
			vegaClient:   drivenadapters.NewVegaBackendClient(),
			modelManager: drivenadapters.NewMFModelManager(),
			modelAPI:     drivenadapters.NewMFModelAPIClient(),
		}
	})
	return skillSearchInst
}

// SearchSkills retrieves skills from the index, restricted to an explicit whitelist.
//
// The whitelist is a pre-filter inside the query, not a post-filter over the top_k: filtering
// after retrieval would make a caller with few bound skills get empty results whenever the
// platform holds more popular ones.
func (s *skillSearchService) SearchSkills(ctx context.Context,
	req *interfaces.SearchSkillsReq) (*interfaces.SearchSkillsResp, error) {
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	var err error
	defer func() { oteltrace.EndSpan(ctx, err) }()

	empty := &interfaces.SearchSkillsResp{Entries: []*interfaces.SearchSkillHit{}}

	// Fail closed. A missing or empty whitelist means "no skill is in scope", never "do not
	// filter": the second reading would hand every skill on the platform to a caller that has
	// bound none.
	ids := normalizeSkillIDs(req.SkillIDs)
	if len(ids) == 0 {
		s.logger.WithContext(ctx).Infof("skill search short-circuited: empty whitelist")
		return empty, nil
	}
	if len(ids) > maxSkillSearchWhitelist {
		err = errors.DefaultHTTPError(ctx, http.StatusBadRequest,
			"skill_ids exceeds the maximum of 1000 entries")
		return nil, err
	}

	query := strings.TrimSpace(req.Query)
	topK := req.TopK
	if topK <= 0 {
		topK = defaultSkillSearchTopK
	}
	if topK > maxSkillSearchTopK {
		topK = maxSkillSearchTopK
	}

	whitelist := map[string]any{
		"field":      "skill_id",
		"operation":  "in",
		"value":      ids,
		"value_from": "const",
	}

	matchedBy := interfaces.SkillMatchedByMatch
	condition := whitelist
	if query != "" {
		channels := []map[string]any{
			{"fields": []string{"name", "description"}, "operation": "match", "value": query, "value_from": "const"},
		}
		if vector := s.embedQuery(ctx, query); len(vector) > 0 {
			channels = append(channels, map[string]any{
				"field": "_vector", "operation": "knn_vector", "value": vector, "value_from": "const", "k": topK,
			})
			matchedBy = interfaces.SkillMatchedByKnn
		}
		condition = map[string]any{
			"operation": "and",
			"sub_conditions": []map[string]any{
				whitelist,
				{"operation": "or", "sub_conditions": channels},
			},
		}
	} else {
		// No query text: this is an enumeration of the whitelist, so nothing was matched by
		// content and the channel is reported as the literal filter.
		matchedBy = interfaces.SkillMatchedByLike
	}

	resp, err := s.vegaClient.QueryDatasetData(ctx, executionFactorySkillDataset, &interfaces.VegaDataQueryParams{
		FilterCondition: condition,
		// Vega only accepts "single" or "cursor" here; there is no offset mode. One page is all
		// this endpoint needs — a whitelist-scoped top_k has no second page to walk.
		Paging:       &interfaces.VegaDataPaging{Mode: vegaPagingModeSingle, Limit: topK},
		OutputFields: []string{"skill_id", "name", "description"},
	})
	if err != nil {
		s.logger.WithContext(ctx).Errorf("skill search query failed: %v", err)
		return nil, errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
	}

	entries := make([]*interfaces.SearchSkillHit, 0, len(resp.Entries))
	for _, entry := range resp.Entries {
		hit := &interfaces.SearchSkillHit{
			SkillID:     stringField(entry, "skill_id"),
			Name:        stringField(entry, "name"),
			Description: stringField(entry, "description"),
			MatchedBy:   matchedBy,
			Score:       floatField(entry, "_score"),
		}
		if hit.SkillID == "" {
			continue
		}
		entries = append(entries, hit)
	}
	return &interfaces.SearchSkillsResp{Entries: entries}, nil
}

// embedQuery vectorises the query with the model the dataset was built with. It returns nil when
// the vector channel cannot be used, and the search then runs on the full-text channel alone:
// a query embedded with a different model than the documents produces confident nonsense, which
// is worse than a narrower recall.
func (s *skillSearchService) embedQuery(ctx context.Context, query string) []float32 {
	log := s.logger.WithContext(ctx)

	resource, err := s.vegaClient.GetResourceByID(ctx, executionFactorySkillDataset)
	if err != nil || resource == nil {
		log.Warnf("skill search: dataset not readable, falling back to full-text: %v", err)
		return nil
	}

	model, err := s.resolveEmbeddingModel(ctx)
	if err != nil || model == nil {
		log.Warnf("skill search: embedding model unavailable, falling back to full-text: %v", err)
		return nil
	}
	if resource.IndexConfig == nil || resource.IndexConfig.DefaultEmbeddingModel != model.ModelID {
		log.Warnf("skill search: dataset was built with a different embedding model, falling back to full-text")
		return nil
	}

	embeddingResp, err := s.modelAPI.Embeddings(ctx, &interfaces.EmbeddingReq{
		Model: model.ModelName,
		Input: []string{query},
	})
	if err != nil || embeddingResp == nil || len(embeddingResp.Data) == 0 {
		log.Warnf("skill search: embedding call failed, falling back to full-text: %v", err)
		return nil
	}
	return embeddingResp.Data[0].Embedding
}

func (s *skillSearchService) resolveEmbeddingModel(ctx context.Context) (*interfaces.EmbeddingModel, error) {
	model, err := s.modelManager.GetDefaultEmbeddingModel(ctx, interfaces.SmallModelTypeEmbedding)
	if err == nil && model != nil {
		return model, nil
	}
	return s.modelManager.GetEmbeddingModel(ctx, interfaces.SmallModelTypeEmbedding, interfaces.SmallModelTypeEmbedding)
}

func normalizeSkillIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	normalized := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		normalized = append(normalized, id)
	}
	return normalized
}

func stringField(entry map[string]any, key string) string {
	if value, ok := entry[key].(string); ok {
		return value
	}
	return ""
}

func floatField(entry map[string]any, key string) float64 {
	switch value := entry[key].(type) {
	case float64:
		return value
	case float32:
		return float64(value)
	case int:
		return float64(value)
	case int64:
		return float64(value)
	}
	return 0
}
