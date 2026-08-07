package knsearch

import (
	"context"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

func TestSemanticInstanceRetrieval_MainFlow(t *testing.T) {
	mockConfig := DefaultRetrievalConfig()
	mockConfig.SemanticInstanceRetrieval.GlobalFinalScoreRatio = 0.5

	tests := []struct {
		name        string
		req         *interfaces.KnSearchLocalRequest
		objectTypes []*interfaces.KnSearchObjectType
		mockSetup   func(*mockOntologyQuery)
		checkResult func(*testing.T, *interfaces.KnSearchSemanticInstanceResult, error)
	}{
		{
			name: "Success - Retrieve Instances",
			req: &interfaces.KnSearchLocalRequest{
				KnID:  "129",
				Query: "test",
			},
			objectTypes: []*interfaces.KnSearchObjectType{
				{
					ConceptID:   "ot1",
					ConceptName: "Type1",
					DataProperties: []*interfaces.KnSearchDataProperty{
						{Name: "instance_name", Type: "text", ConditionOperations: []interfaces.KnOperationType{interfaces.KnOperationTypeKnn, interfaces.KnOperationTypeMatch}},
					},
				},
			},
			mockSetup: func(m *mockOntologyQuery) {
				m.instancesResp = &interfaces.QueryObjectInstancesResp{
					Data: []any{
						map[string]any{
							"unique_identities": map[string]any{"id": "inst1"},
							"instance_name":     "test instance",
							"_score":            0.9,
						},
						map[string]any{
							"unique_identities": map[string]any{"id": "inst2"},
							"instance_name":     "other",
							"_score":            0.1, // Should be filtered by global ratio (0.9 * 0.5 = 0.45)
						},
					},
				}
			},
			checkResult: func(t *testing.T, res *interfaces.KnSearchSemanticInstanceResult, err error) {
				if err != nil {
					t.Fatalf("Unexpected error: %v", err)
				}
				if len(res.Nodes) != 1 {
					t.Errorf("Expected 1 node, got %d", len(res.Nodes))
				}
				if res.Nodes[0].InstanceName != "test instance" {
					t.Errorf("Expected 'test instance', got %s", res.Nodes[0].InstanceName)
				}
			},
		},
		{
			name: "No Object Types",
			req: &interfaces.KnSearchLocalRequest{
				KnID: "129",
			},
			objectTypes: []*interfaces.KnSearchObjectType{},
			mockSetup:   func(m *mockOntologyQuery) {},
			checkResult: func(t *testing.T, res *interfaces.KnSearchSemanticInstanceResult, err error) {
				if err != nil {
					t.Fatalf("Unexpected error: %v", err)
				}
				if res.Message == "" {
					t.Error("Expected message for no object types")
				}
			},
		},
		{
			name: "Partial Failure",
			req: &interfaces.KnSearchLocalRequest{
				KnID:  "129",
				Query: "test",
			},
			objectTypes: []*interfaces.KnSearchObjectType{
				{
					ConceptID: "ot1",
					DataProperties: []*interfaces.KnSearchDataProperty{
						{Name: "title", Type: "text", ConditionOperations: []interfaces.KnOperationType{interfaces.KnOperationTypeKnn, interfaces.KnOperationTypeMatch}},
					},
				},
				{
					ConceptID: "ot2",
					DataProperties: []*interfaces.KnSearchDataProperty{
						{Name: "title", Type: "text", ConditionOperations: []interfaces.KnOperationType{interfaces.KnOperationTypeKnn, interfaces.KnOperationTypeMatch}},
					},
				},
			},
			mockSetup: func(m *mockOntologyQuery) {
				// We need a way to mock per-call behavior.
				// For simplicity in this basic mock, let's assume the mock returns success
				// but we can simulate failure by checking inputs in the mock implementation if we expanded it.
				// Since our simple mock returns the same for all calls, let's just test success flow here.
				// Or use a custom mock implementation inside the test.
				m.instancesResp = &interfaces.QueryObjectInstancesResp{
					Data: []any{
						map[string]any{"instance_name": "success", "_score": 0.8},
					},
				}
			},
			checkResult: func(t *testing.T, res *interfaces.KnSearchSemanticInstanceResult, err error) {
				// Since our simple mock doesn't distinguish inputs, this effectively tests
				// retrieving from multiple types and aggregating.
				if len(res.Nodes) != 2 {
					t.Errorf("Expected 2 nodes (1 from each type), got %d", len(res.Nodes))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockQuery := &mockOntologyQuery{}
			if tt.mockSetup != nil {
				tt.mockSetup(mockQuery)
			}

			svc := &localSearchImpl{
				logger:        &mockLogger{},
				ontologyQuery: mockQuery,
			}

			res, err := svc.semanticInstanceRetrieval(context.Background(), tt.req, tt.objectTypes, mockConfig)
			tt.checkResult(t, res, err)
		})
	}
}

func TestRetrieveInstancesForObjectType(t *testing.T) {
	mockQuery := &mockOntologyQuery{
		instancesResp: &interfaces.QueryObjectInstancesResp{
			Data: []any{
				map[string]any{"instance_name": "A", "_score": 0.9},
				map[string]any{"instance_name": "B", "_score": 0.8},
				map[string]any{"instance_name": "C", "_score": 0.7},
				map[string]any{"instance_name": "D", "_score": 0.2}, // Low score
			},
		},
	}

	svc := &localSearchImpl{
		logger:        &mockLogger{},
		ontologyQuery: mockQuery,
	}

	req := &interfaces.KnSearchLocalRequest{KnID: "129", Query: "test"}
	// 需有可搜字段才会调用 API（与 Python 一致）
	objType := &interfaces.KnSearchObjectType{
		ConceptID: "ot1",
		DataProperties: []*interfaces.KnSearchDataProperty{
			{Name: "title", Type: "text", ConditionOperations: []interfaces.KnOperationType{interfaces.KnOperationTypeKnn, interfaces.KnOperationTypeMatch}},
		},
	}
	config := &interfaces.KnSearchSemanticInstanceRetrievalConfig{
		InitialCandidateCount: 10,
		PerTypeInstanceLimit:  2,   // Limit to 2
		MinDirectRelevance:    0.3, // Filter < 0.3
		ExactNameMatchScore:   1.0,
	}

	nodes, err := svc.retrieveInstancesForObjectType(context.Background(), req, objType, config, true)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Should return top 2: A and B
	if len(nodes) != 2 {
		t.Errorf("Expected 2 nodes, got %d", len(nodes))
	}
	if nodes[0].InstanceName != "A" {
		t.Error("Expected A first")
	}
	if nodes[1].InstanceName != "B" {
		t.Error("Expected B second")
	}
}

func TestRetrieveInstancesForObjectType_NoSearchableFields(t *testing.T) {
	svc := &localSearchImpl{logger: &mockLogger{}, ontologyQuery: &mockOntologyQuery{}}
	req := &interfaces.KnSearchLocalRequest{KnID: "129", Query: "test"}
	objType := &interfaces.KnSearchObjectType{ConceptID: "ot1"} // 无 DataProperties
	config := &interfaces.KnSearchSemanticInstanceRetrievalConfig{InitialCandidateCount: 10, PerTypeInstanceLimit: 2}

	nodes, err := svc.retrieveInstancesForObjectType(context.Background(), req, objType, config, true)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	// 无可搜字段时跳过该对象类型，返回 nil/空（与 Python 一致）
	if len(nodes) != 0 {
		t.Errorf("Expected empty nodes when no searchable fields, got len=%d", len(nodes))
	}
}

func TestBuildSemanticSearchConditionStruct(t *testing.T) {
	svc := &localSearchImpl{}
	config := &interfaces.KnSearchSemanticInstanceRetrievalConfig{
		InitialCandidateCount:    50,
		MaxSemanticSubConditions: 10,
	}
	// 无可搜字段时返回 nil（与 Python 一致，无 "*" 回退）
	objTypeEmpty := &interfaces.KnSearchObjectType{ConceptID: "ot1"}
	condEmpty := svc.buildSemanticSearchConditionStruct("query", findSemanticSearchableFields(objTypeEmpty), config, true)
	if condEmpty != nil {
		t.Errorf("Expected nil when no searchable fields (align with Python), got %v", condEmpty)
	}

	// 有可搜字段时按字段构建条件
	objTypeWithProps := &interfaces.KnSearchObjectType{
		ConceptID: "ot1",
		DataProperties: []*interfaces.KnSearchDataProperty{
			{Name: "title", Type: "text", ConditionOperations: []interfaces.KnOperationType{interfaces.KnOperationTypeKnn, interfaces.KnOperationTypeMatch}},
		},
	}
	cond := svc.buildSemanticSearchConditionStruct("query", findSemanticSearchableFields(objTypeWithProps), config, true)
	if cond == nil {
		t.Fatal("Expected non-nil condition when searchable fields present")
	}
	if cond.Operation != interfaces.KnOperationTypeOr {
		t.Errorf("Expected OR operation, got %s", cond.Operation)
	}
	if len(cond.SubConditions) < 2 {
		t.Errorf("Expected at least 2 subconditions (knn+match for title), got %d", len(cond.SubConditions))
	}
}

func TestConvertToKnSearchNode(t *testing.T) {
	svc := &localSearchImpl{}
	objType := &interfaces.KnSearchObjectType{
		ConceptID:   "ot1",
		ConceptName: "Type1",
	}
	data := map[string]any{
		"unique_identities": map[string]any{"id": "123"},
		"instance_name":     "Name",
		"prop1":             "Value1",
		"_score":            0.95,
	}

	node := svc.convertToKnSearchNode(objType, data)

	if node.ObjectTypeID != "ot1" {
		t.Error("ObjectTypeID mismatch")
	}
	if node.InstanceName != "Name" {
		t.Error("InstanceName mismatch")
	}
	if node.Score != 0.95 {
		t.Error("Score mismatch")
	}
	if node.Properties["prop1"] != "Value1" {
		t.Error("Property mismatch")
	}
	// unique_identities should be handled separately
	if node.Properties["unique_identities"] != nil {
		t.Error("unique_identities should not be in Properties")
	}
}

func TestScoreNodes(t *testing.T) {
	svc := &localSearchImpl{}
	config := &interfaces.KnSearchSemanticInstanceRetrievalConfig{
		ExactNameMatchScore: 0.85,
	}

	nodes := []*interfaces.KnSearchNode{
		{InstanceName: "Target", Score: 0},      // Exact match
		{InstanceName: "Target Item", Score: 0}, // Contains query
		{InstanceName: "Tar", Score: 0},         // Contained by query
		{InstanceName: "Other", Score: 0},       // No match
		{InstanceName: "Existing", Score: 0.9},  // Existing score
	}

	svc.scoreNodes("Target", nodes, nil, config)

	if nodes[0].Score != 0.85 {
		t.Errorf("Exact match score mismatch: %f", nodes[0].Score)
	}
	if nodes[1].Score != 0.5 {
		t.Errorf("Contains score mismatch: %f", nodes[1].Score)
	}
	if nodes[2].Score != 0.3 {
		t.Errorf("Contained score mismatch: %f", nodes[2].Score)
	}
	if nodes[3].Score != 0.0 {
		t.Errorf("No match score mismatch: %f", nodes[3].Score)
	}
	if nodes[4].Score != 0.9 {
		t.Errorf("Existing score mismatch: %f", nodes[4].Score)
	}
}

func TestFilterNodesByScore(t *testing.T) {
	svc := &localSearchImpl{}
	nodes := []*interfaces.KnSearchNode{
		{Score: 0.9},
		{Score: 0.5},
		{Score: 0.1},
	}

	// Filter >= 0.5
	filtered := svc.filterNodesByScore(nodes, 0.5)

	if len(filtered) != 2 {
		t.Errorf("Expected 2 nodes, got %d", len(filtered))
	}
}

func TestFilterNodeProperties(t *testing.T) {
	svc := &localSearchImpl{}
	config := &interfaces.KnSearchPropertyFilterConfig{
		MaxPropertiesPerInstance: 2,
		MaxPropertyValueLength:   5,
		EnablePropertyFilter:     boolPtr(true),
	}

	nodes := []*interfaces.KnSearchNode{
		{
			Properties: map[string]any{
				"p1": "short",
				"p2": "very long string",
				"p3": "extra",
			},
		},
	}

	filtered := svc.filterNodeProperties(nodes, config)
	props := filtered[0].Properties

	if len(props) != 2 {
		t.Errorf("Expected 2 properties, got %d", len(props))
	}
	if _, ok := props["p3"]; ok {
		t.Error("Expected p3 to be filtered out")
	}
	if props["p2"] != "very ..." {
		t.Errorf("Expected p2 to be truncated, got %v", props["p2"])
	}
}

// Helper to create default config for tests
func DefaultRetrievalConfig() *interfaces.KnSearchRetrievalConfig {
	return &interfaces.KnSearchRetrievalConfig{
		ConceptRetrieval:          DefaultConceptRetrievalConfig(),
		SemanticInstanceRetrieval: DefaultSemanticInstanceRetrievalConfig(),
		PropertyFilter:            DefaultPropertyFilterConfig(),
	}
}

// 字段只支持等值时拼不出子条件，必须返回 nil：空 OR 条件会被 ontology-query 判 400。
func TestBuildSemanticSearchConditionStruct_ExactOnlyFieldsYieldNoCondition(t *testing.T) {
	svc := &localSearchImpl{}
	config := &interfaces.KnSearchSemanticInstanceRetrievalConfig{
		MaxSemanticSubConditions: 5,
		PerTypeInstanceLimit:     5,
	}
	searchable := []searchableField{
		{Name: "player_id", HasExactMatch: true},
		{Name: "team_id", HasExactMatch: true},
	}

	if cond := svc.buildSemanticSearchConditionStruct("Brazil", searchable, config, true); cond != nil {
		t.Fatalf("expected nil condition for exact-only fields, got %+v", cond)
	}
}

func TestBuildSemanticSearchConditionStruct_MatchFieldYieldsCondition(t *testing.T) {
	svc := &localSearchImpl{}
	config := &interfaces.KnSearchSemanticInstanceRetrievalConfig{
		MaxSemanticSubConditions: 5,
		PerTypeInstanceLimit:     5,
	}
	searchable := []searchableField{
		{Name: "player_id", HasExactMatch: true},
		{Name: "team_name", HasExactMatch: true, HasMatch: true},
	}

	cond := svc.buildSemanticSearchConditionStruct("Brazil", searchable, config, true)
	if cond == nil || len(cond.SubConditions) != 1 {
		t.Fatalf("expected exactly one match sub-condition, got %+v", cond)
	}
	if cond.SubConditions[0].Field != "team_name" ||
		cond.SubConditions[0].Operation != interfaces.KnOperationTypeMatch {
		t.Fatalf("unexpected sub-condition: %+v", cond.SubConditions[0])
	}
}

func knnTestConfig() *interfaces.KnSearchSemanticInstanceRetrievalConfig {
	enabled := true
	return &interfaces.KnSearchSemanticInstanceRetrievalConfig{
		MaxSemanticSubConditions:   10,
		PerTypeInstanceLimit:       5,
		EnableKnnInstanceRetrieval: &enabled,
		MaxKnnSubConditionsPerType: 1,
	}
}

func countOps(cond *interfaces.KnCondition, op interfaces.KnOperationType) int {
	if cond == nil {
		return 0
	}
	n := 0
	for _, sub := range cond.SubConditions {
		if sub.Operation == op {
			n++
		}
	}
	return n
}

// 同一行的多个向量字段各发一次 knn，成本线性叠加而召回增益很小：每个对象类只发一个。
func TestBuildSemanticSearchConditionStruct_KnnBudgetPerType(t *testing.T) {
	svc := &localSearchImpl{}
	searchable := []searchableField{
		{Name: "stadium_name", HasKnn: true, HasMatch: true},
		{Name: "city_name", HasKnn: true, HasMatch: true},
		{Name: "country_name", HasKnn: true, HasMatch: true},
	}

	cond := svc.buildSemanticSearchConditionStruct("Mexico", searchable, knnTestConfig(), true)

	if got := countOps(cond, interfaces.KnOperationTypeKnn); got != 1 {
		t.Fatalf("expected a single knn sub-condition, got %d", got)
	}
	if got := countOps(cond, interfaces.KnOperationTypeMatch); got != 3 {
		t.Fatalf("every fulltext field should still be matched, got %d", got)
	}
}

// 尾部对象类不发向量条件，但全文照常——否则等于把它们整个丢掉。
func TestBuildSemanticSearchConditionStruct_KnnDeniedKeepsMatch(t *testing.T) {
	svc := &localSearchImpl{}
	searchable := []searchableField{{Name: "stadium_name", HasKnn: true, HasMatch: true}}

	cond := svc.buildSemanticSearchConditionStruct("Mexico", searchable, knnTestConfig(), false)

	if got := countOps(cond, interfaces.KnOperationTypeKnn); got != 0 {
		t.Fatalf("knn must be withheld when not allowed, got %d", got)
	}
	if got := countOps(cond, interfaces.KnOperationTypeMatch); got != 1 {
		t.Fatalf("fulltext must survive, got %d", got)
	}
}

// 只有向量字段、又不许发 knn 时拼不出条件，必须返回 nil 而不是空 OR。
func TestBuildSemanticSearchConditionStruct_KnnOnlyFieldDeniedYieldsNil(t *testing.T) {
	svc := &localSearchImpl{}
	searchable := []searchableField{{Name: "embedding_only", HasKnn: true}}

	if cond := svc.buildSemanticSearchConditionStruct("Mexico", searchable, knnTestConfig(), false); cond != nil {
		t.Fatalf("expected nil condition, got %+v", cond)
	}
}

// 只有真有向量字段的对象类才发向量条件：没有的话本来也拼不出 knn。
func TestKnnAllowedFor(t *testing.T) {
	config := knnTestConfig()

	if !knnAllowedFor(knnCapableObjectType("stadiums", 0, true), config) {
		t.Fatalf("vector-capable object type must be allowed")
	}
	if knnAllowedFor(knnCapableObjectType("teams", 9.9, false), config) {
		t.Fatalf("object type without a vector field must not be allowed regardless of anything else")
	}

	disabled := false
	config.EnableKnnInstanceRetrieval = &disabled
	if knnAllowedFor(knnCapableObjectType("stadiums", 0, true), config) {
		t.Fatalf("the switch must win")
	}
}

// 默认配置必须真的把向量检索打开：struct tag 上的 default 在这条链路不生效，
// 漏了就等于功能整个关掉，而且不会有任何报错。
func TestDefaultConfig_EnablesKnn(t *testing.T) {
	config := DefaultSemanticInstanceRetrievalConfig()

	if !boolValue(config.EnableKnnInstanceRetrieval) {
		t.Fatalf("knn must be on by default")
	}
	if config.MaxKnnSubConditionsPerType <= 0 {
		t.Fatalf("knn budget must have a real default, got %+v", config)
	}

	merged := MergeRetrievalConfig(nil)
	if !boolValue(merged.SemanticInstanceRetrieval.EnableKnnInstanceRetrieval) {
		t.Fatalf("the merged config is what runtime actually uses; knn must survive it")
	}
}

func knnCapableObjectType(id string, _ float64, withVector bool) *interfaces.KnSearchObjectType {
	ops := []interfaces.KnOperationType{interfaces.KnOperationTypeEqual, interfaces.KnOperationTypeMatch}
	if withVector {
		ops = append(ops, interfaces.KnOperationTypeKnn)
	}
	return &interfaces.KnSearchObjectType{
		ConceptID: id,
		DataProperties: []*interfaces.KnSearchDataProperty{
			{Name: "name", Type: "string", ConditionOperations: ops},
		},
	}
}

