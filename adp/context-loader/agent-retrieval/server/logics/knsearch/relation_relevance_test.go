package knsearch

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// Regression for #788.
//
// Every scorer on the relation path can come back with nothing relevant: the reranker is not
// registered, coarse recall is off so every _score is zero, the literal name matcher finds no
// substring in a multi-word query. The old code still cut Top-K out of that all-zero list, so a
// relation nothing had vouched for reached the caller - and relation endpoint completion then
// pulled its two object types in behind it, turning one meaningless row into three that the caller
// could not tell from real hits.

// irrelevantQuery matches no relation name or comment in buildEndpointsLastNetwork, so the literal
// matcher - the last rung of the ladder - scores every one of them zero.
const irrelevantQuery = "完全不相干的问题 对象类 字段"

func relationNames(relations []*interfaces.KnSearchRelationType) []string {
	out := make([]string, 0, len(relations))
	for _, rel := range relations {
		out = append(out, rel.ConceptName)
	}
	return out
}

func objectIDs(objects []*interfaces.KnSearchObjectType) []string {
	out := make([]string, 0, len(objects))
	for _, obj := range objects {
		out = append(out, obj.ConceptID)
	}
	return out
}

// endpointIDs are the object types reachable only as relation endpoints in this fixture: the
// relations hang off obj_6..obj_9, which sit at the tail of definition order. If one of them shows
// up while relation_types is empty, endpoint completion leaked.
func endpointIDs() map[string]struct{} {
	return map[string]struct{}{"obj_6": {}, "obj_7": {}, "obj_8": {}, "obj_9": {}}
}

func runRelationRelevanceCase(
	t *testing.T,
	query string,
	rerank interfaces.DrivenMFModelAPIClient,
	coarseObjectScores bool,
	relationScores map[string]float64,
) *interfaces.KnSearchConceptResult {
	t.Helper()

	net := buildEndpointsLastNetwork()
	for _, rel := range net.RelationTypes {
		rel.Score = relationScores[rel.ID]
	}

	backend := &mockBknBackend{networkDetail: net}
	if coarseObjectScores {
		backend.objectTypesResp = &interfaces.ObjectTypeConcepts{
			Entries: buildScoredObjectEntries(net, "对象_0"),
		}
	} else {
		backend.objectTypesError = fmt.Errorf("backend unavailable")
	}

	svc := &localSearchImpl{logger: &mockLogger{}, bknBackend: backend, rerankClient: rerank}

	cfg := DefaultConceptRetrievalConfig()
	cfg.TopK = 1

	res, err := svc.conceptRetrieval(context.Background(),
		&interfaces.KnSearchLocalRequest{KnID: "kn_endpoints_last", Query: query, EnableRerank: true}, cfg)
	if err != nil {
		t.Fatalf("conceptRetrieval failed: %v", err)
	}
	return res
}

// The reported failure: no reranker, no coarse signal, nothing the literal matcher can vouch for.
func TestNoRelevantRelationYieldsEmptyList(t *testing.T) {
	res := runRelationRelevanceCase(t, irrelevantQuery,
		&mockRerankClient{rerankError: fmt.Errorf("ModelFactory.ExternalSmallModel.Used.NameNotExist")},
		true, nil)

	if got := relationNames(res.RelationTypes); len(got) != 0 {
		t.Errorf("expected no relation type when nothing scored above zero, got %v", got)
	}
	// The second half of the bug: an unvouched-for relation used to drag its endpoints in with it.
	leaked := endpointIDs()
	for _, id := range objectIDs(res.ObjectTypes) {
		if _, isEndpoint := leaked[id]; isEndpoint {
			t.Errorf("relation endpoint %s leaked into object_types with no relation returned: %v",
				id, objectIDs(res.ObjectTypes))
		}
	}
	if len(res.ObjectTypes) == 0 {
		t.Error("object types must still be returned; only the unvouched-for relations go away")
	}
}

// The ladder's middle rung: coarse recall scored some relations and not others. ok=true must not
// license dragging the unscored ones along to fill Top-K.
func TestCoarseScoreFallbackDropsUnscoredRelations(t *testing.T) {
	res := runRelationRelevanceCase(t, irrelevantQuery,
		&mockRerankClient{rerankError: fmt.Errorf("reranker unavailable")},
		true, map[string]float64{"rel_1": 2.5})

	got := relationNames(res.RelationTypes)
	if len(got) != 1 || got[0] != "关系_B" {
		t.Fatalf("expected only the coarse-scored relation, got %v", got)
	}
}

// All coarse _scores zero drops to the literal matcher, which cannot vouch for anything here.
func TestSimpleMatchFallbackAllZeroYieldsEmptyList(t *testing.T) {
	res := runRelationRelevanceCase(t, irrelevantQuery,
		&mockRerankClient{rerankError: fmt.Errorf("reranker unavailable")},
		true, nil)

	if got := relationNames(res.RelationTypes); len(got) != 0 {
		t.Errorf("expected empty relation list from the literal matcher, got %v", got)
	}
}

// A working reranker still answers, and only with what it rated above zero.
func TestRerankKeepsOnlyRelationsItRated(t *testing.T) {
	rerank := &mockRerankClient{
		rerankFunc: func(query string, documents []string, model string) (*interfaces.RerankResp, error) {
			resp := &interfaces.RerankResp{}
			for i, doc := range documents {
				score := 0.0
				if strings.Contains(doc, "关系_B") {
					score = 0.88
				}
				resp.Results = append(resp.Results, interfaces.RerankResult{Index: i, RelevanceScore: score})
			}
			return resp, nil
		},
	}

	cfg := DefaultConceptRetrievalConfig()
	cfg.TopK = 3
	net := buildEndpointsLastNetwork()
	svc := &localSearchImpl{
		logger: &mockLogger{},
		bknBackend: &mockBknBackend{
			networkDetail:   net,
			objectTypesResp: &interfaces.ObjectTypeConcepts{Entries: buildScoredObjectEntries(net, "对象_0")},
		},
		rerankClient: rerank,
	}

	res, err := svc.conceptRetrieval(context.Background(),
		&interfaces.KnSearchLocalRequest{KnID: "kn_endpoints_last", Query: "关系_B", EnableRerank: true}, cfg)
	if err != nil {
		t.Fatalf("conceptRetrieval failed: %v", err)
	}

	got := relationNames(res.RelationTypes)
	if len(got) != 1 || got[0] != "关系_B" {
		t.Fatalf("expected only the relation the reranker rated, got %v", got)
	}
}

// A vendor that returns only its top N can answer without scoring a single relation. Recall order
// is not relevance, so handing back its head would reintroduce the bug through a success path.
func TestRerankScoringNoRelationTakesTheDegradeLadder(t *testing.T) {
	net := buildEndpointsLastNetwork()
	objectCount := len(net.ObjectTypes)
	rerank := &mockRerankClient{
		rerankFunc: func(query string, documents []string, model string) (*interfaces.RerankResp, error) {
			resp := &interfaces.RerankResp{}
			for i := 0; i < objectCount && i < len(documents); i++ {
				resp.Results = append(resp.Results, interfaces.RerankResult{Index: i, RelevanceScore: 0.6})
			}
			return resp, nil
		},
	}

	res := runRelationRelevanceCase(t, irrelevantQuery, rerank, true, nil)
	if got := relationNames(res.RelationTypes); len(got) != 0 {
		t.Errorf("expected the degrade ladder to yield nothing, got %v", got)
	}
}
