// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package toolbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
)

// fakeBknBackend records what a Cypher function sent and answers with a fixed reply.
type fakeBknBackend struct {
	calls     []*interfaces.CypherQueryReq
	knIDs     []string
	deadlines []time.Duration // time left on each call's context; 0 when it has none
	status    int
	body      string
	err       error
	hang      bool // wait for the call's context to end, like a statement still running
}

func (f *fakeBknBackend) RunCypher(ctx context.Context, knID string, req *interfaces.CypherQueryReq) (*interfaces.CypherQueryResult, error) {
	f.calls = append(f.calls, req)
	f.knIDs = append(f.knIDs, knID)
	var left time.Duration
	if deadline, ok := ctx.Deadline(); ok {
		left = time.Until(deadline)
	}
	f.deadlines = append(f.deadlines, left)
	if f.hang {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if f.err != nil {
		return nil, f.err
	}
	status := f.status
	if status == 0 {
		status = http.StatusOK
	}
	return &interfaces.CypherQueryResult{StatusCode: status, Body: []byte(f.body)}, nil
}

// cypherSpec stores inputs the way the parser does: as the request body schema.
func cypherSpec(t *testing.T, inputs map[string]*openapi3.Schema) string {
	t.Helper()
	body := openapi3.NewObjectSchema()
	for name, schema := range inputs {
		body.Properties[name] = openapi3.NewSchemaRef("", schema)
		body.Required = append(body.Required, name)
	}
	spec := &interfaces.APISpec{RequestBody: &interfaces.RequestBody{Content: openapi3.NewContentWithJSONSchema(body)}}
	return spec.ToJSON()
}

func cypherFunction(t *testing.T, shape interfaces.CypherResultShape, inputs map[string]*openapi3.Schema) *model.FunctionMetadataDB {
	return &model.FunctionMetadataDB{
		Version:     "v1",
		ScriptType:  model.ScriptTypeCypher,
		KnID:        "kn-supply",
		ResultShape: string(shape),
		Code:        "MATCH (s:supplier {supplier_id: $id}) RETURN count(s)",
		APISpec:     cypherSpec(t, inputs),
	}
}

func idInput() map[string]*openapi3.Schema {
	return map[string]*openapi3.Schema{"id": openapi3.NewStringSchema()}
}

// newCypherExecutor returns a service whose sandbox proxy must never be called.
func newCypherExecutor(t *testing.T, backend interfaces.BknBackendClient, function interfaces.IMetadataDB) *ToolServiceImpl {
	ctrl := gomock.NewController(t)
	metadataService := mocks.NewMockIMetadataService(ctrl)
	metadataService.EXPECT().GetMetadataBySource(gomock.Any(), "s1", model.SourceTypeFunction).
		Return(true, function, nil).AnyTimes()
	return &ToolServiceImpl{
		Logger:          logger.DefaultLogger(),
		MetadataService: metadataService,
		Proxy:           mocks.NewMockProxyHandler(ctrl), // no EXPECT: a Cypher function never reaches it
		BknBackend:      backend,
	}
}

func runCypherTool(t *testing.T, s *ToolServiceImpl, body any, timeout int) *interfaces.HTTPResponse {
	t.Helper()
	resp, err := s.executeTool(context.Background(), &interfaces.ExecuteToolReq{
		UserID: "u1", BoxID: "b1", ToolID: "t1", Timeout: timeout,
		HTTPRequestParams: interfaces.HTTPRequestParams{Body: body},
	}, &model.ToolDB{ToolID: "t1", BoxID: "b1", SourceID: "s1", SourceType: model.SourceTypeFunction}, "")
	if err != nil {
		t.Fatalf("executeTool: %v", err)
	}
	return resp
}

func bodyJSON(t *testing.T, resp *interfaces.HTTPResponse) string {
	t.Helper()
	raw, err := json.Marshal(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestCypherValueFunctionResults(t *testing.T) {
	cases := []struct {
		name       string
		reply      string
		wantStatus int
		wantBody   string
	}{
		{"one row", `{"columns":[{"name":"n","type":"integer"}],"entries":[{"n":12345678901234567890}]}`,
			http.StatusOK, `{"value":12345678901234567890}`},
		{"no row is null", `{"columns":[{"name":"n"}],"entries":[]}`, http.StatusOK, `{"value":null}`},
		{"null value", `{"columns":[{"name":"n"}],"entries":[{"n":null}]}`, http.StatusOK, `{"value":null}`},
		{"string value", `{"columns":[{"name":"n"}],"entries":[{"n":"in transit"}]}`, http.StatusOK, `{"value":"in transit"}`},
		{"more than one row", `{"columns":[{"name":"n"}],"entries":[{"n":1},{"n":2}]}`, http.StatusBadRequest, "at most one row, the statement returned 2"},
		{"more than one row, cut by the default limit", `{"columns":[{"name":"n"}],"entries":[{"n":1},{"n":2}],"limited":true}`,
			http.StatusBadRequest, "returned 2 or more"},
		{"two columns", `{"columns":[{"name":"a"},{"name":"b"}],"entries":[{"a":1,"b":2}]}`, http.StatusBadRequest, "exactly one column"},
		{"node by value type", `{"columns":[{"name":"s"}],"entries":[{"s":{"id":"S1"}}],"value_types":{"s":{"kind":"object"}}}`,
			http.StatusBadRequest, `column \"s\" is of kind object`},
		{"list without value type", `{"columns":[{"name":"l"}],"entries":[{"l":[1,2]}]}`, http.StatusBadRequest, "returned a list"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			backend := &fakeBknBackend{body: tc.reply}
			s := newCypherExecutor(t, backend, cypherFunction(t, interfaces.CypherResultShapeValue, idInput()))
			resp := runCypherTool(t, s, map[string]any{"id": "S1"}, 0)
			if resp.StatusCode != tc.wantStatus || !strings.Contains(bodyJSON(t, resp), tc.wantBody) {
				t.Fatalf("got %d %s, want %d containing %s", resp.StatusCode, bodyJSON(t, resp), tc.wantStatus, tc.wantBody)
			}
		})
	}
}

func TestCypherTableFunctionPassesRowsThrough(t *testing.T) {
	backend := &fakeBknBackend{body: `{"columns":[{"name":"name","type":"string"}],"entries":[{"name":"A"},{"name":"B"}],"limited":true,"plan":{"x":1}}`}
	s := newCypherExecutor(t, backend, cypherFunction(t, interfaces.CypherResultShapeTable, idInput()))
	resp := runCypherTool(t, s, map[string]any{"id": "S1"}, 0)
	want := `{"columns":[{"name":"name","type":"string"}],"entries":[{"name":"A"},{"name":"B"}],"limited":true}`
	if resp.StatusCode != http.StatusOK || bodyJSON(t, resp) != want {
		t.Fatalf("got %d %s, want %s", resp.StatusCode, bodyJSON(t, resp), want)
	}
	if backend.knIDs[0] != "kn-supply" || backend.calls[0].Explain {
		t.Fatalf("sent kn %q explain %v", backend.knIDs[0], backend.calls[0].Explain)
	}
}

func TestCypherFunctionPassesARefusalThrough(t *testing.T) {
	backend := &fakeBknBackend{status: http.StatusForbidden, body: `{"error_code":"PublicError_Forbidden","description":"no"}`}
	s := newCypherExecutor(t, backend, cypherFunction(t, interfaces.CypherResultShapeValue, idInput()))
	resp := runCypherTool(t, s, map[string]any{"id": "S1"}, 0)
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(bodyJSON(t, resp), "PublicError_Forbidden") {
		t.Fatalf("got %d %s", resp.StatusCode, bodyJSON(t, resp))
	}
}

func TestCypherFunctionArguments(t *testing.T) {
	inputs := map[string]*openapi3.Schema{
		"id":     openapi3.NewStringSchema(),
		"min":    openapi3.NewFloat64Schema(),
		"active": openapi3.NewBoolSchema(),
		"ids":    openapi3.NewArraySchema().WithItems(openapi3.NewFloat64Schema()),
	}
	t.Run("converted to the declared types", func(t *testing.T) {
		backend := &fakeBknBackend{body: `{"columns":[],"entries":[]}`}
		s := newCypherExecutor(t, backend, cypherFunction(t, interfaces.CypherResultShapeTable, inputs))
		// Arguments may also arrive as a JSON string; undeclared ones are left out.
		runCypherTool(t, s, `{"id": 42, "min": "5", "active": "true", "ids": ["1", 2], "extra": "x"}`, 7)
		got, _ := json.Marshal(backend.calls[0].Parameters)
		if string(got) != `{"active":true,"id":"42","ids":[1,2],"min":5}` {
			t.Fatalf("parameters = %s", got)
		}
	})
	refusals := []struct {
		name string
		body any
		want string
	}{
		{"missing", map[string]any{"id": "S1", "min": 1, "active": true}, "missing required parameters: ids"},
		{"null counts as missing", map[string]any{"id": nil, "min": 1, "active": true, "ids": []any{1}}, "missing required parameters: id"},
		{"wrong type", map[string]any{"id": "S1", "min": "many", "active": true, "ids": []any{1}}, "min must be a number, got a string"},
		{"null in a list", map[string]any{"id": "S1", "min": 1, "active": true, "ids": []any{1, nil}}, "holds a null"},
		{"not an object", "[1,2]", "arguments must be a JSON object"},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			backend := &fakeBknBackend{}
			s := newCypherExecutor(t, backend, cypherFunction(t, interfaces.CypherResultShapeTable, inputs))
			resp := runCypherTool(t, s, tc.body, 0)
			if resp.StatusCode != http.StatusBadRequest || !strings.Contains(bodyJSON(t, resp), tc.want) {
				t.Fatalf("got %d %s, want 400 containing %q", resp.StatusCode, bodyJSON(t, resp), tc.want)
			}
			if len(backend.calls) != 0 {
				t.Fatal("arguments that do not fit must not reach bkn-backend")
			}
		})
	}
}

// A number sent for a string input is a code written without quotes: it must reach
// the statement as the same digits, not as 7.6904023e+07, and a long one must not
// be rounded on the way.
func TestCypherFunctionKeepsTheDigitsOfANumberSentAsAString(t *testing.T) {
	inputs := map[string]*openapi3.Schema{"id": openapi3.NewStringSchema(), "min": openapi3.NewFloat64Schema()}
	cases := []struct {
		name string
		body any
		want string
	}{
		{"decoded body", map[string]any{"id": float64(76904023), "min": 1}, `{"id":"76904023","min":1}`},
		{"decoded fraction", map[string]any{"id": 0.5, "min": 1}, `{"id":"0.5","min":1}`},
		{"JSON string body", `{"id": 12345678901234567890, "min": 1}`, `{"id":"12345678901234567890","min":1}`},
		{"long number written as a string", map[string]any{"id": "S1", "min": "12345678901234567890"},
			`{"id":"S1","min":12345678901234567890}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			backend := &fakeBknBackend{body: `{"columns":[],"entries":[]}`}
			s := newCypherExecutor(t, backend, cypherFunction(t, interfaces.CypherResultShapeTable, inputs))
			resp := runCypherTool(t, s, tc.body, 0)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("got %d %s", resp.StatusCode, bodyJSON(t, resp))
			}
			got, _ := json.Marshal(backend.calls[0].Parameters)
			if string(got) != tc.want {
				t.Fatalf("parameters = %s, want %s", got, tc.want)
			}
		})
	}
}

// An argument refusal is the tool's own reply, so it carries the same rendered
// description any error response of the service does.
func TestCypherFunctionArgumentRefusalIsDescribed(t *testing.T) {
	s := newCypherExecutor(t, &fakeBknBackend{}, cypherFunction(t, interfaces.CypherResultShapeValue, idInput()))
	resp := runCypherTool(t, s, map[string]any{}, 0)
	body, ok := resp.Body.(httperrors.BaseError)
	if !ok {
		t.Fatalf("body is %T", resp.Body)
	}
	if body.Description == "" {
		t.Fatalf("refusal has no description: %s", bodyJSON(t, resp))
	}
}

// A tool timeout is how long the caller waits, not a request for a longer
// statement: ontology-query always sends 300 seconds, which bkn-backend refuses as
// timeout_sec, while its own configured limit already ends the statement sooner.
func TestCypherFunctionTimeoutIsHowLongTheCallWaits(t *testing.T) {
	backend := &fakeBknBackend{body: `{"columns":[{"name":"n"}],"entries":[{"n":1}]}`}
	s := newCypherExecutor(t, backend, cypherFunction(t, interfaces.CypherResultShapeValue, idInput()))
	resp := runCypherTool(t, s, map[string]any{"id": "S1"}, 300)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d %s", resp.StatusCode, bodyJSON(t, resp))
	}
	raw, _ := json.Marshal(backend.calls[0])
	if strings.Contains(string(raw), "timeout") {
		t.Fatalf("the tool timeout reached bkn-backend: %s", raw)
	}
	if left := backend.deadlines[0]; left <= 290*time.Second || left > 300*time.Second {
		t.Fatalf("call deadline = %v, want about 300s", left)
	}
	runCypherTool(t, s, map[string]any{"id": "S1"}, 0)
	if backend.deadlines[1] != 0 {
		t.Fatalf("no timeout must set no deadline, got %v", backend.deadlines[1])
	}
}

// Past the caller's timeout the function answers 504, as a Python function does.
func TestCypherFunctionPastItsTimeoutIs504(t *testing.T) {
	backend := &fakeBknBackend{hang: true}
	s := newCypherExecutor(t, backend, cypherFunction(t, interfaces.CypherResultShapeValue, idInput()))
	resp := runCypherTool(t, s, map[string]any{"id": "S1"}, 1)
	if resp.StatusCode != http.StatusGatewayTimeout || !strings.Contains(bodyJSON(t, resp), "within 1 seconds") {
		t.Fatalf("got %d %s", resp.StatusCode, bodyJSON(t, resp))
	}
}

func TestCypherFunctionTransportFailureIsAnError(t *testing.T) {
	backend := &fakeBknBackend{err: errors.New("down")}
	s := newCypherExecutor(t, backend, cypherFunction(t, interfaces.CypherResultShapeValue, idInput()))
	_, err := s.executeTool(context.Background(), &interfaces.ExecuteToolReq{
		HTTPRequestParams: interfaces.HTTPRequestParams{Body: map[string]any{"id": "S1"}},
	}, &model.ToolDB{SourceID: "s1", SourceType: model.SourceTypeFunction}, "")
	if err == nil {
		t.Fatal("expected the transport failure to be returned")
	}
}

func TestValidateCypherFunctionMetadata(t *testing.T) {
	ctx := context.Background()
	t.Run("explains with typed placeholders and examples", func(t *testing.T) {
		withExample := openapi3.NewStringSchema()
		withExample.Example = "2024-01-31"
		inputs := map[string]*openapi3.Schema{
			"day": withExample, "n": openapi3.NewFloat64Schema(), "b": openapi3.NewBoolSchema(),
			"ids": openapi3.NewArraySchema().WithItems(openapi3.NewStringSchema()),
			"since": openapi3.NewStringSchema(),
		}
		backend := &fakeBknBackend{body: `{"plan":{"columns":[{"name":"c","type":{"kind":"integer"}}]}}`}
		s := &ToolServiceImpl{BknBackend: backend}
		if err := s.validateCypherFunctionMetadata(ctx, cypherFunction(t, interfaces.CypherResultShapeValue, inputs)); err != nil {
			t.Fatalf("validate: %v", err)
		}
		if !backend.calls[0].Explain {
			t.Fatal("validation must explain, not run")
		}
		got, _ := json.Marshal(backend.calls[0].Parameters)
		// A string without an example gets a date: bkn-backend refuses a string
		// that is not one against a date property, and any string suits any other.
		if string(got) != `{"b":false,"day":"2024-01-31","ids":["2000-01-01"],"n":0,"since":"2000-01-01"}` {
			t.Fatalf("explain parameters = %s", got)
		}
	})
	refusals := []struct {
		name       string
		shape      interfaces.CypherResultShape
		status     int
		reply      string
		wantStatus int
		want       string
	}{
		{"bkn-backend refuses the statement", interfaces.CypherResultShapeValue, http.StatusBadRequest,
			`{"error_code":"BknBackend.Cypher.SyntaxError","description":"Syntax error","error_details":"line 1: unexpected RETRUN"}`,
			// The details say what to change; the description only names the kind of error.
			http.StatusBadRequest, "the cypher statement was refused: line 1: unexpected RETRUN"},
		{"the author cannot read it", interfaces.CypherResultShapeTable, http.StatusForbidden,
			`{"error_code":"PublicError_Forbidden","description":"Forbidden"}`, http.StatusForbidden, "Forbidden"},
		{"bkn-backend failing is a gateway error", interfaces.CypherResultShapeTable, http.StatusInternalServerError,
			`oops`, http.StatusBadGateway, "refused by bkn-backend"},
		{"value returning two columns", interfaces.CypherResultShapeValue, http.StatusOK,
			`{"plan":{"columns":[{"name":"a"},{"name":"b"}]}}`, http.StatusBadRequest, "returns 2"},
		{"value returning a node", interfaces.CypherResultShapeValue, http.StatusOK,
			`{"plan":{"columns":[{"name":"s","type":{"kind":"object"}}]}}`, http.StatusBadRequest, `column "s" is of kind object`},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			s := &ToolServiceImpl{BknBackend: &fakeBknBackend{status: tc.status, body: tc.reply}}
			err := s.validateCypherFunctionMetadata(ctx, cypherFunction(t, tc.shape, idInput()))
			var httpErr *httperrors.HTTPError
			if !errors.As(err, &httpErr) || httpErr.HTTPCode != tc.wantStatus ||
				!strings.Contains(fmt.Sprint(httpErr.ErrorDetails), tc.want) {
				t.Fatalf("err = %v, want %d containing %q", err, tc.wantStatus, tc.want)
			}
		})
	}
	t.Run("a table may return any columns", func(t *testing.T) {
		s := &ToolServiceImpl{BknBackend: &fakeBknBackend{body: `{"plan":{"columns":[{"name":"s","type":{"kind":"object"}},{"name":"b"}]}}`}}
		if err := s.validateCypherFunctionMetadata(ctx, cypherFunction(t, interfaces.CypherResultShapeTable, idInput())); err != nil {
			t.Fatalf("validate: %v", err)
		}
	})
	t.Run("a python function is not sent to bkn-backend", func(t *testing.T) {
		backend := &fakeBknBackend{}
		s := &ToolServiceImpl{BknBackend: backend}
		if err := s.validateCypherFunctionMetadata(ctx, &model.FunctionMetadataDB{ScriptType: "python"}); err != nil {
			t.Fatal(err)
		}
		if len(backend.calls) != 0 {
			t.Fatal("python function reached bkn-backend")
		}
	})
}

func newEditService(t *testing.T, stored interfaces.IMetadataDB) *ToolServiceImpl {
	ctrl := gomock.NewController(t)
	metadataService := mocks.NewMockIMetadataService(ctrl)
	metadataService.EXPECT().GetMetadataBySource(gomock.Any(), "s1", model.SourceTypeFunction).
		Return(true, stored, nil).AnyTimes()
	return &ToolServiceImpl{Logger: logger.DefaultLogger(), MetadataService: metadataService}
}

func TestBuildFunctionInputKeepsTheStoredCypherDefinition(t *testing.T) {
	stored := cypherFunction(t, interfaces.CypherResultShapeValue, idInput())
	s := newEditService(t, stored)
	// An editor that changes only the statement sends nothing else.
	input, err := s.buildFunctionInput(context.Background(), &interfaces.UpdateToolReq{
		ToolName: "n", ToolDesc: "d",
		FunctionInputEdit: &interfaces.FunctionInputEdit{
			ScriptType: interfaces.ScriptTypeCypher,
			Code:       "MATCH (s:supplier {supplier_id: $id}) RETURN s.name",
		},
	}, &model.ToolDB{SourceID: "s1", SourceType: model.SourceTypeFunction})
	if err != nil {
		t.Fatalf("buildFunctionInput: %v", err)
	}
	if input.KnID != "kn-supply" || input.ResultShape != interfaces.CypherResultShapeValue {
		t.Fatalf("kn/shape = %q/%q, want the stored ones", input.KnID, input.ResultShape)
	}
	if len(input.Inputs) != 1 || input.Inputs[0].Name != "id" {
		t.Fatalf("inputs = %+v, want the stored id", input.Inputs)
	}
	if input.Dependencies != nil || input.DependenciesURL != "" {
		t.Fatal("a cypher function must not pick up dependencies")
	}
}

func TestBuildFunctionInputRefusesChangingTheScriptType(t *testing.T) {
	cases := map[string]struct {
		stored interfaces.IMetadataDB
		edit   interfaces.ScriptType
	}{
		"cypher to python": {cypherFunction(t, interfaces.CypherResultShapeValue, idInput()), interfaces.ScriptTypePython},
		"python to cypher": {&model.FunctionMetadataDB{ScriptType: "python", Code: "def handler(e): pass"}, interfaces.ScriptTypeCypher},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s := newEditService(t, tc.stored)
			_, err := s.buildFunctionInput(context.Background(), &interfaces.UpdateToolReq{
				FunctionInputEdit: &interfaces.FunctionInputEdit{ScriptType: tc.edit, Code: "x"},
			}, &model.ToolDB{SourceID: "s1", SourceType: model.SourceTypeFunction})
			if err == nil || !strings.Contains(fmt.Sprint(err), "create a new function instead") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}
