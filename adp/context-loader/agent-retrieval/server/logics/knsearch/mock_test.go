package knsearch

import (
	"context"
	"fmt"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// mockLogger simulates the Logger interface. Instance recall will issue multiple queries concurrently, and both channels will write logs.
// So logs must be locked - otherwise it will explode under -race.
type mockLogger struct {
	mu   sync.Mutex
	logs []string
}

func (m *mockLogger) append(line string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logs = append(m.logs, line)
}

// entries returns a log snapshot for use by assertions.
func (m *mockLogger) entries() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.logs...)
}

func (m *mockLogger) WithContext(ctx context.Context) interfaces.Logger {
	return m
}

func (m *mockLogger) Info(v ...interface{}) {
	m.append(fmt.Sprint(v...))
}

func (m *mockLogger) Debug(v ...interface{}) {
	m.append(fmt.Sprint(v...))
}

func (m *mockLogger) Warn(v ...interface{}) {
	m.append(fmt.Sprint(v...))
}

func (m *mockLogger) Error(v ...interface{}) {
	m.append(fmt.Sprint(v...))
}

func (m *mockLogger) Infof(format string, args ...interface{}) {
	m.append(fmt.Sprintf("[INFO] "+format, args...))
}

func (m *mockLogger) Debugf(format string, args ...interface{}) {
	m.append(fmt.Sprintf("[DEBUG] "+format, args...))
}

func (m *mockLogger) Warnf(format string, args ...interface{}) {
	m.append(fmt.Sprintf("[WARN] "+format, args...))
}

func (m *mockLogger) Errorf(format string, args ...interface{}) {
	m.append(fmt.Sprintf("[ERROR] "+format, args...))
}

// mockBknBackend simulates the BknBackendAccess interface.
type mockBknBackend struct {
	networkDetail      *interfaces.KnowledgeNetworkDetail
	networkError       error
	networkCalls       int
	objectTypesResp    *interfaces.ObjectTypeConcepts
	objectTypesError   error
	objectTypesReq     *interfaces.QueryConceptsReq
	objectDetailResp   []*interfaces.ObjectType
	objectDetailError  error
	objectDetailKnID   string
	objectDetailIDs    []string
	objectDetailCalls  int
	relationTypesResp  *interfaces.RelationTypeConcepts
	relationTypesError error
	relationTypesReq   *interfaces.QueryConceptsReq
	actionTypesResp    *interfaces.ActionTypeConcepts
	actionTypesError   error
	actionTypesReq     *interfaces.QueryConceptsReq
}

func (m *mockBknBackend) GetKnowledgeNetworkDetail(ctx context.Context, knID string) (*interfaces.KnowledgeNetworkDetail, error) {
	m.networkCalls++
	return m.networkDetail, m.networkError
}

func (m *mockBknBackend) ListKnowledgeNetworks(ctx context.Context, req *interfaces.ListKnReq) (*interfaces.ListKnResp, error) {
	return &interfaces.ListKnResp{}, nil
}

func (m *mockBknBackend) SearchObjectTypes(ctx context.Context, req *interfaces.QueryConceptsReq) (*interfaces.ObjectTypeConcepts, error) {
	m.objectTypesReq = req
	if m.objectTypesResp == nil && m.objectTypesError == nil && m.networkDetail != nil {
		return &interfaces.ObjectTypeConcepts{Entries: m.networkDetail.ObjectTypes}, nil
	}
	return m.objectTypesResp, m.objectTypesError
}

func (m *mockBknBackend) SearchRelationTypes(ctx context.Context, req *interfaces.QueryConceptsReq) (*interfaces.RelationTypeConcepts, error) {
	m.relationTypesReq = req
	if m.relationTypesResp == nil && m.relationTypesError == nil && m.networkDetail != nil {
		return &interfaces.RelationTypeConcepts{Entries: m.networkDetail.RelationTypes}, nil
	}
	return m.relationTypesResp, m.relationTypesError
}

// The following is an empty implementation of other methods in the interface, which satisfies the interface definition.
func (m *mockBknBackend) GetObjectTypeDetail(ctx context.Context, knID string, otIds []string, includeDetail bool) ([]*interfaces.ObjectType, error) {
	m.objectDetailCalls++
	m.objectDetailKnID = knID
	m.objectDetailIDs = append([]string(nil), otIds...)
	if m.objectDetailResp == nil && m.objectDetailError == nil && m.networkDetail != nil {
		requested := make(map[string]struct{}, len(otIds))
		for _, id := range otIds {
			requested[id] = struct{}{}
		}
		result := make([]*interfaces.ObjectType, 0, len(otIds))
		for _, objectType := range m.networkDetail.ObjectTypes {
			if objectType != nil {
				if _, ok := requested[objectType.ID]; ok {
					result = append(result, objectType)
				}
			}
		}
		return result, nil
	}
	return m.objectDetailResp, m.objectDetailError
}

func (m *mockBknBackend) GetRelationTypeDetail(ctx context.Context, knID string, rtIDs []string, includeDetail bool) ([]*interfaces.RelationType, error) {
	return nil, nil
}

func (m *mockBknBackend) SearchActionTypes(ctx context.Context, req *interfaces.QueryConceptsReq) (actionTypes *interfaces.ActionTypeConcepts, err error) {
	m.actionTypesReq = req
	if m.actionTypesResp == nil && m.actionTypesError == nil && m.networkDetail != nil {
		return &interfaces.ActionTypeConcepts{Entries: m.networkDetail.ActionTypes}, nil
	}
	return m.actionTypesResp, m.actionTypesError
}

type allowAllQueryCandidateAuthorizer struct{}

func (allowAllQueryCandidateAuthorizer) FilterObjectTypeIDs(_ context.Context, _ string,
	candidateIDs []string,
) ([]string, error) {
	return append([]string(nil), candidateIDs...), nil
}

func (m *mockBknBackend) ListMetricsByObjectTypes(ctx context.Context, knID string, otIDs []string) ([]*interfaces.RelatedMetric, error) {
	return nil, nil
}

func (m *mockBknBackend) SearchMetricTypes(ctx context.Context, query *interfaces.QueryConceptsReq) (*interfaces.MetricTypeConcepts, error) {
	return nil, nil
}

func (m *mockBknBackend) GetActionTypeDetail(ctx context.Context, knID string, atIDs []string, includeDetail bool) ([]*interfaces.ActionType, error) {
	return nil, nil
}

// mockOntologyQuery simulates the DrivenOntologyQuery interface.
//
// instancesFunc allows use cases to branch responses based on request conditions - instance recall splits knn and match into two queries.
// The fusion behavior can be measured only when the respective returns are different. Set it and leave it, otherwise return a single instancesResp.
type mockOntologyQuery struct {
	mu             sync.Mutex
	instancesResp  *interfaces.QueryObjectInstancesResp
	instancesError error
	instancesFunc  func(*interfaces.QueryObjectInstancesReq) (*interfaces.QueryObjectInstancesResp, error)
	callCount      int
	conds          []*interfaces.KnCondition
}

func (m *mockOntologyQuery) QueryObjectInstances(ctx context.Context, req *interfaces.QueryObjectInstancesReq) (*interfaces.QueryObjectInstancesResp, error) {
	m.mu.Lock()
	m.callCount++
	m.conds = append(m.conds, req.Cond)
	fn := m.instancesFunc
	resp, err := m.instancesResp, m.instancesError
	m.mu.Unlock()

	if fn != nil {
		return fn(req)
	}
	return resp, err
}

// calls returns a snapshot of the number of calls.
func (m *mockOntologyQuery) calls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callCount
}

func (m *mockOntologyQuery) QueryLogicProperties(ctx context.Context, req *interfaces.QueryLogicPropertiesReq) (*interfaces.QueryLogicPropertiesResp, error) {
	return nil, nil
}

func (m *mockOntologyQuery) QueryActions(ctx context.Context, req *interfaces.QueryActionsRequest) (*interfaces.QueryActionsResponse, error) {
	return nil, nil
}

func (m *mockOntologyQuery) ExecuteActions(ctx context.Context, req *interfaces.ExecuteActionsRequest) (*interfaces.ExecuteActionsResponse, error) {
	return nil, nil
}

func (m *mockOntologyQuery) GetActionExecution(ctx context.Context, req *interfaces.GetActionExecutionRequest) (map[string]any, error) {
	return nil, nil
}

func (m *mockOntologyQuery) ListActionExecutions(ctx context.Context, req *interfaces.ListActionExecutionsRequest) (map[string]any, error) {
	return nil, nil
}

func (m *mockOntologyQuery) QueryMetricData(ctx context.Context, knID, metricID string, fillNull bool,
	req *interfaces.MetricQueryDownstreamReq) (*interfaces.MetricQueryDownstreamResp, error) {
	return &interfaces.MetricQueryDownstreamResp{}, nil
}

func (m *mockOntologyQuery) QueryInstanceSubgraph(ctx context.Context, req *interfaces.QueryInstanceSubgraphReq) (resp *interfaces.QueryInstanceSubgraphResp, err error) {
	return nil, nil
}

func (m *mockOntologyQuery) ExploreSubgraph(ctx context.Context, req *interfaces.ExploreSubgraphReq) (resp *interfaces.ExploreSubgraphResp, err error) {
	return nil, nil
}

// mockRerankClient simulates the DrivenMFModelAPIClient interface.
type mockRerankClient struct {
	mu          sync.Mutex
	rerankResp  *interfaces.RerankResp
	rerankError error
	rerankFunc  func(query string, documents []string, model string) (*interfaces.RerankResp, error)
	calls       int
	lastQuery   string
	lastDocs    []string
	lastModel   string
}

func (m *mockRerankClient) Rerank(ctx context.Context, query string, documents []string, model string) (*interfaces.RerankResp, error) {
	m.mu.Lock()
	m.calls++
	m.lastQuery = query
	m.lastDocs = append([]string(nil), documents...)
	m.lastModel = model
	fn := m.rerankFunc
	resp, err := m.rerankResp, m.rerankError
	m.mu.Unlock()

	if fn != nil {
		return fn(query, documents, model)
	}
	return resp, err
}

func (m *mockRerankClient) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func (m *mockRerankClient) documents() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.lastDocs...)
}

func (m *mockRerankClient) Chat(ctx context.Context, req *interfaces.LLMChatReq) (string, error) {
	return "", nil
}

// createMockNetworkDetail creates knowledge network details for testing.
func createMockNetworkDetail(objectCount, relationCount, actionCount int) *interfaces.KnowledgeNetworkDetail {
	detail := &interfaces.KnowledgeNetworkDetail{
		ID:            "129",
		ObjectTypes:   make([]*interfaces.ObjectType, objectCount),
		RelationTypes: make([]*interfaces.RelationType, relationCount),
		ActionTypes:   make([]*interfaces.ActionType, actionCount),
	}

	// Generate object type (at least one attribute supports semantic retrieval for use in semantic instance recall testing)
	for i := 0; i < objectCount; i++ {
		detail.ObjectTypes[i] = &interfaces.ObjectType{
			ID:      fmt.Sprintf("obj_%d", i),
			Name:    fmt.Sprintf("对象类型_%d", i),
			Comment: fmt.Sprintf("对象注释_%d", i),
			DataProperties: []*interfaces.DataProperty{
				{
					Name:                "prop1",
					DisplayName:         "属性1",
					Type:                "text",
					ConditionOperations: []interfaces.KnOperationType{interfaces.KnOperationTypeKnn, interfaces.KnOperationTypeMatch},
				},
				{Name: "prop2", DisplayName: "属性2"},
			},
		}
	}

	// Generate relationship type.
	for i := 0; i < relationCount; i++ {
		detail.RelationTypes[i] = &interfaces.RelationType{
			ID:                 fmt.Sprintf("rel_%d", i),
			Name:               fmt.Sprintf("关系_%d", i),
			Comment:            fmt.Sprintf("关系注释_%d", i),
			SourceObjectTypeID: fmt.Sprintf("obj_%d", i%objectCount),
			TargetObjectTypeID: fmt.Sprintf("obj_%d", (i+1)%objectCount),
		}
	}

	// Generate operation type.
	for i := 0; i < actionCount; i++ {
		detail.ActionTypes[i] = &interfaces.ActionType{
			ID:           fmt.Sprintf("action_%d", i),
			Name:         fmt.Sprintf("操作_%d", i),
			Comment:      fmt.Sprintf("操作注释_%d", i),
			ObjectTypeID: fmt.Sprintf("obj_%d", i%objectCount),
		}
	}

	return detail
}

// createMockInstanceData creates instance data for testing (reserved for extended testing)
//
//nolint:unused
func createMockInstanceData(count int) []interface{} {
	data := make([]interface{}, count)
	for i := 0; i < count; i++ {
		data[i] = map[string]interface{}{
			"unique_identities": map[string]interface{}{
				"id": fmt.Sprintf("inst_%d", i),
			},
			"instance_name": fmt.Sprintf("实例_%d", i),
			"field1":        fmt.Sprintf("值_%d", i),
			"_score":        0.9 - float64(i)*0.1,
		}
	}
	return data
}
