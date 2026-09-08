// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package capability

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/drivenadapters"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
)

const (
	defaultSearchTopK = 10
	maxSearchTopK     = 100
	// maxSearchWhitelist bounds one request. OpenSearch accepts far more terms
	// (index.max_terms_count defaults to 65536); this keeps a single query small enough to read
	// in a log. Past this point the caller should resolve the whitelist server-side.
	maxSearchWhitelist = 1000
	// rrfK is the reciprocal-rank-fusion constant. 60 is the value the instance retrieval path
	// uses, kept the same so the two fused rankings behave alike.
	rrfK = 60

	channelKnn   = "knn"
	channelMatch = "match"
)

var (
	searchOnce sync.Once
	searchInst interfaces.CapabilitySearchService
)

type capabilitySearchService struct {
	logger     interfaces.Logger
	vegaClient interfaces.VegaBackendClient
	modelAPI   interfaces.MFModelAPIClient
	// indexSync supplies the embedding model the documents were vectorised with. Resolving the
	// current system default here instead would silently put the query in a different vector
	// space from the index the moment the default changes.
	indexSync *capabilityIndexSync
}

// NewCapabilitySearchService returns the capability search singleton.
func NewCapabilitySearchService() interfaces.CapabilitySearchService {
	searchOnce.Do(func() {
		conf := config.NewConfigLoader()
		NewCapabilityIndexSyncService()
		searchInst = &capabilitySearchService{
			logger:     conf.GetLogger(),
			vegaClient: drivenadapters.NewVegaBackendClient(),
			modelAPI:   drivenadapters.NewMFModelAPIClient(),
			indexSync:  syncInst,
		}
	})
	return searchInst
}

// SearchCapabilities ranks the whitelisted capabilities against the query.
//
// The whitelist is a pre-filter inside the query, never a filter applied to the results. Taking
// the globally nearest documents first and intersecting afterwards would give a network that
// mounted three capabilities nothing at all, because on a platform holding hundreds none of its
// three is in the global top-k. Fewer mounts would mean worse recall, which is backwards.
func (s *capabilitySearchService) SearchCapabilities(ctx context.Context,
	req *interfaces.SearchCapabilitiesReq) (*interfaces.SearchCapabilitiesResp, error) {
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	var err error
	defer func() { oteltrace.EndSpan(ctx, err) }()

	empty := &interfaces.SearchCapabilitiesResp{Entries: []*interfaces.CapabilityHit{}}
	if req == nil {
		return empty, nil
	}

	// Fail closed. An empty whitelist means "no capability is in scope", never "do not filter":
	// the second reading would hand every capability on the platform to a caller that mounted
	// none.
	keys := whitelistKeys(req.Refs, req.Types)
	if len(keys) == 0 {
		s.logger.WithContext(ctx).Infof("capability search short-circuited: empty whitelist")
		return empty, nil
	}
	if len(keys) > maxSearchWhitelist {
		err = errors.DefaultHTTPError(ctx, http.StatusBadRequest,
			fmt.Sprintf("refs exceeds the maximum of %d entries", maxSearchWhitelist))
		return nil, err
	}

	topK := req.TopK
	if topK <= 0 {
		topK = defaultSearchTopK
	}
	if topK > maxSearchTopK {
		topK = maxSearchTopK
	}

	whitelist := map[string]any{
		"field":      "capability_key",
		"operation":  "in",
		"value":      keys,
		"value_from": "const",
	}

	query := strings.TrimSpace(req.Query)
	if query == "" {
		// No query text: this is an enumeration of the whitelist. Nothing was matched by content,
		// so the channel is reported as the literal filter rather than as a ranking.
		return s.enumerate(ctx, whitelist, topK)
	}
	return s.rank(ctx, query, whitelist, topK)
}

// enumerate lists the whitelist without ranking it.
func (s *capabilitySearchService) enumerate(ctx context.Context, whitelist map[string]any,
	topK int) (*interfaces.SearchCapabilitiesResp, error) {
	entries, err := s.fetch(ctx, whitelist, topK)
	if err != nil {
		return nil, errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
	}
	hits := make([]*interfaces.CapabilityHit, 0, len(entries))
	for _, entry := range entries {
		if hit := toHit(entry, interfaces.CapabilityMatchedByLike); hit != nil {
			hits = append(hits, hit)
		}
	}
	return &interfaces.SearchCapabilitiesResp{Entries: hits}, nil
}

// rank runs the two retrieval channels and fuses them by rank.
//
// The channels are two separate requests, not one OR. Combined into a single boolean query
// OpenSearch simply adds the clause scores, and a knn score lives in 0..1 while BM25 has no upper
// bound — every vector hit would be pushed out of the candidate pool by a lexical one, and no
// amount of reordering afterwards recovers it.
func (s *capabilitySearchService) rank(ctx context.Context, query string, whitelist map[string]any,
	topK int) (*interfaces.SearchCapabilitiesResp, error) {
	type outcome struct {
		name    string
		entries []map[string]any
		err     error
	}

	conditions := make([]struct {
		name string
		cond map[string]any
	}, 0, 2)

	if knnCond, err := s.buildKnnCondition(ctx, query, whitelist, topK); err != nil {
		// A vector channel that cannot be built is not fatal: lexical recall that reports itself
		// honestly beats failing a search the other channel could have answered.
		s.logger.WithContext(ctx).Warnf("capability knn channel unavailable: %v", err)
	} else {
		conditions = append(conditions, struct {
			name string
			cond map[string]any
		}{channelKnn, knnCond})
	}
	conditions = append(conditions, struct {
		name string
		cond map[string]any
	}{channelMatch, buildMatchCondition(query, whitelist)})

	outcomes := make([]outcome, len(conditions))
	var wg sync.WaitGroup
	for i := range conditions {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			entries, err := s.fetch(ctx, conditions[idx].cond, topK)
			outcomes[idx] = outcome{name: conditions[idx].name, entries: entries, err: err}
		}(i)
	}
	wg.Wait()

	live := make([]outcome, 0, len(outcomes))
	for _, o := range outcomes {
		if o.err != nil {
			// One failed channel must not take down the search. knn answers 400 on a dataset
			// whose vector field never got a mapping, and that used to fail the whole query.
			s.logger.WithContext(ctx).Warnf("capability channel %s failed: %v", o.name, o.err)
			continue
		}
		live = append(live, o)
	}
	if len(live) == 0 {
		return nil, errors.DefaultHTTPError(ctx, http.StatusInternalServerError,
			"all capability retrieval channels failed")
	}

	type fused struct {
		hit      *interfaces.CapabilityHit
		score    float64
		channels map[string]struct{}
	}
	byKey := make(map[string]*fused)
	order := make([]string, 0)
	for _, o := range live {
		for rank, entry := range o.entries {
			hit := toHit(entry, o.name)
			if hit == nil {
				continue
			}
			key := capabilityKey(hit.CapabilityRef)
			existing, ok := byKey[key]
			if !ok {
				existing = &fused{hit: hit, channels: map[string]struct{}{}}
				byKey[key] = existing
				order = append(order, key)
			}
			existing.channels[o.name] = struct{}{}
			// Rank, not score: the two channels' scores are not on one scale, which is the whole
			// reason they were issued separately.
			existing.score += 1 / float64(rrfK+rank+1)
		}
	}

	hits := make([]*interfaces.CapabilityHit, 0, len(order))
	// 2(k+1) brings "first place in both channels" back to 1.0, matching the instance path.
	norm := 2 * float64(rrfK+1)
	for _, key := range order {
		entry := byKey[key]
		entry.hit.Score = entry.score * norm
		entry.hit.MatchedBy = matchedBy(entry.channels)
		hits = append(hits, entry.hit)
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > topK {
		hits = hits[:topK]
	}
	return &interfaces.SearchCapabilitiesResp{Entries: hits}, nil
}

// buildKnnCondition vectorises the query and puts the whitelist inside the ANN query.
//
// The whitelist goes in as a sub-condition, which vega renders as the knn field object's own
// "filter". That placement is what makes it a pre-filter: the engine searches within the filtered
// set instead of intersecting afterwards. Vega used to emit it as a sibling of "knn", which
// OpenSearch rejected outright — fixed in #1296, verified against a live engine before this
// channel was turned on.
func (s *capabilitySearchService) buildKnnCondition(ctx context.Context, query string,
	whitelist map[string]any, topK int) (map[string]any, error) {
	vector, err := s.embedQuery(ctx, query)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"field":          "_vector",
		"operation":      "knn_vector",
		"value":          vector,
		"value_from":     "const",
		"sub_conditions": []map[string]any{whitelist},
		"limit_key":      "k",
		"limit_value":    topK,
	}, nil
}

// buildMatchCondition is the lexical channel: the whitelist and a full-text match on either field.
func buildMatchCondition(query string, whitelist map[string]any) map[string]any {
	return map[string]any{
		"operation": "and",
		"sub_conditions": []map[string]any{
			whitelist,
			{
				"operation": "or",
				"sub_conditions": []map[string]any{
					{"field": "name", "operation": "match", "value": query, "value_from": "const"},
					{"field": "description", "operation": "match", "value": query, "value_from": "const"},
				},
			},
		},
	}
}

// embedQuery vectorises the query with the model the documents were built with.
func (s *capabilitySearchService) embedQuery(ctx context.Context, query string) ([]float32, error) {
	modelName := interfaces.SmallModelTypeEmbedding
	if s.indexSync != nil {
		modelName = s.indexSync.getEmbeddingModelName()
	}
	resp, err := s.modelAPI.Embeddings(ctx, &interfaces.EmbeddingReq{
		Model: modelName,
		Input: []string{query},
	})
	if err != nil {
		return nil, err
	}
	if resp == nil || len(resp.Data) == 0 || len(resp.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("embedding result is empty")
	}
	return resp.Data[0].Embedding, nil
}

func (s *capabilitySearchService) fetch(ctx context.Context, condition map[string]any,
	topK int) ([]map[string]any, error) {
	resp, err := s.vegaClient.QueryDatasetData(ctx, capabilityDataset, &interfaces.VegaDataQueryParams{
		FilterCondition: condition,
		// Vega accepts only "single" or "cursor" here. A whitelist-scoped top_k has no second
		// page to walk.
		Paging:       &interfaces.VegaDataPaging{Mode: vegaPagingModeSingle, Limit: topK},
		OutputFields: []string{"capability_type", "owner_id", "capability_id", "name", "description"},
	})
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, nil
	}
	return resp.Entries, nil
}

// whitelistKeys turns the mounted references into the composite keys the filter matches on,
// dropping malformed entries and applying the optional type narrowing.
//
// Malformed entries are dropped rather than rejected: the list is assembled from stored bindings,
// and one stale row should narrow the search, not fail the request.
func whitelistKeys(refs []interfaces.CapabilityRef, types []string) []string {
	allowed := make(map[string]struct{}, len(types))
	for _, t := range types {
		if t = strings.TrimSpace(t); t != "" {
			allowed[t] = struct{}{}
		}
	}

	seen := make(map[string]struct{}, len(refs))
	keys := make([]string, 0, len(refs))
	for _, ref := range refs {
		if validateRef(ref) != nil {
			continue
		}
		if len(allowed) > 0 {
			if _, ok := allowed[strings.TrimSpace(ref.CapabilityType)]; !ok {
				continue
			}
		}
		key := capabilityKey(ref)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	return keys
}

func toHit(entry map[string]any, matchedBy string) *interfaces.CapabilityHit {
	ref := interfaces.CapabilityRef{
		CapabilityType: stringField(entry, "capability_type"),
		OwnerID:        stringField(entry, "owner_id"),
		CapabilityID:   stringField(entry, "capability_id"),
	}
	if validateRef(ref) != nil {
		return nil
	}
	return &interfaces.CapabilityHit{
		CapabilityRef: ref,
		Name:          stringField(entry, "name"),
		Description:   stringField(entry, "description"),
		MatchedBy:     matchedBy,
		Score:         floatField(entry, "_score"),
	}
}

// matchedBy reports which channels found the document. A document both channels returned is
// labelled hybrid rather than being attributed to whichever one happened to be read first.
func matchedBy(channels map[string]struct{}) string {
	_, knn := channels[channelKnn]
	_, match := channels[channelMatch]
	switch {
	case knn && match:
		return interfaces.CapabilityMatchedByHybrid
	case knn:
		return interfaces.CapabilityMatchedByKnn
	default:
		return interfaces.CapabilityMatchedByMatch
	}
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
