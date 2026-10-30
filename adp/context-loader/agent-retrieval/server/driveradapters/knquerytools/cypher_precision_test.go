// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knquerytools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/kncypher"
)

type preciseCypherResult struct{ resp *interfaces.CypherQueryResp }

func (s preciseCypherResult) RunCypher(context.Context, *kncypher.RunCypherReq) (*interfaces.CypherQueryResp, error) {
	return s.resp, nil
}

func TestRunCypherRESTPreservesExactNumbers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	want := []map[string]any{{
		"safe":          json.Number("9007199254740991"),
		"even":          json.Number("9007199254740992"),
		"odd":           json.Number("9007199254740993"),
		"negative_safe": json.Number("-9007199254740991"),
		"negative_even": json.Number("-9007199254740992"),
		"negative":      json.Number("-9007199254740993"),
		"exact_text":    "9007199254740993",
		"nested":        []any{map[string]any{"value": json.Number("9007199254740993")}},
	}}
	handler := &knQueryToolsHandler{cypher: preciseCypherResult{resp: &interfaces.CypherQueryResp{Entries: want}}}
	engine := gin.New()
	engine.POST("/kn/run_cypher", handler.RunCypher)
	req := httptest.NewRequest(http.MethodPost, "/kn/run_cypher", strings.NewReader(
		`{"kn_id":"kn-1","query":"MATCH (p:precision_sample) RETURN p"}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var got struct {
		Entries []map[string]any `json:"entries"`
	}
	decoder := json.NewDecoder(recorder.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Entries, want) {
		t.Fatalf("REST entries = %#v, want %#v", got.Entries, want)
	}
}
