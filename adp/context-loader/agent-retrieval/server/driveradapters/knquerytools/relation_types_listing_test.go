// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knquerytools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

type relationListingRESTBackend struct {
	interfaces.BknBackendAccess
	interfaces.RelationTypeReader
	gotBranch      string
	gotObjectTypes []string
	gotLimit       int
	// many, when set, is how many long-commented entries a page carries.
	many int
}

func (b *relationListingRESTBackend) ListRelationTypesBranch(_ context.Context, _, branch string, objectTypeIDs []string,
	_, limit int) (*interfaces.RelationTypePage, error) {
	b.gotBranch, b.gotObjectTypes, b.gotLimit = branch, objectTypeIDs, limit
	if b.many > 0 {
		page := &interfaces.RelationTypePage{TotalCount: int64(b.many * 2), Scanned: b.many}
		for i := 0; i < b.many; i++ {
			page.Entries = append(page.Entries, &interfaces.RelationType{ID: fmt.Sprintf("rt-%04d", i), Name: "属于",
				Comment: strings.Repeat("订单属于客户，按客户编号关联。", 30)})
		}
		return page, nil
	}
	return &interfaces.RelationTypePage{
		Entries: []*interfaces.RelationType{{ID: "rt-1", Name: "属于", MappingRules: []any{"rule"},
			SourceObjectTypeID: "ot-order", TargetObjectTypeID: "ot-customer"}},
		TotalCount: 1, Scanned: 1,
	}, nil
}

// The REST route is the same tool on another face: it lists without ids, by the
// object types and on the branch the body names.
func TestGetRelationTypesRESTListsByObjectTypes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	backend := &relationListingRESTBackend{}
	handler := &knQueryToolsHandler{bknBackend: backend}
	router := gin.New()
	router.POST("/kn/get_relation_types", handler.GetRelationTypes)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/kn/get_relation_types", strings.NewReader(
		`{"kn_id":"kn-1","branch":"release","object_type_ids":["ot-order"],"limit":10}`)))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if backend.gotBranch != "release" || !reflect.DeepEqual(backend.gotObjectTypes, []string{"ot-order"}) || backend.gotLimit != 10 {
		t.Fatalf("asked for %v on %q with limit %d", backend.gotObjectTypes, backend.gotBranch, backend.gotLimit)
	}
	m := map[string]any{}
	if err := json.Unmarshal(response.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	relations, _ := m["relation_types"].([]any)
	entry, _ := relations[0].(map[string]any)
	if _, present := entry["mapping_rules"]; present || m["total_count"] != float64(1) {
		t.Fatalf("want an index entry and its total, got %v", m)
	}
}

// The REST face cuts a page past the size bound as the MCP one does, measured on the
// body it writes.
func TestGetRelationTypesRESTCutsAPagePastTheSizeBound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	saved := interfaces.MaxRelationTypePageChars
	interfaces.MaxRelationTypePageChars = 4000
	t.Cleanup(func() { interfaces.MaxRelationTypePageChars = saved })

	handler := &knQueryToolsHandler{bknBackend: &relationListingRESTBackend{many: 50}}
	router := gin.New()
	router.POST("/kn/get_relation_types", handler.GetRelationTypes)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/kn/get_relation_types",
		strings.NewReader(`{"kn_id":"kn-1","offset":10}`)))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if n := utf8.RuneCount(response.Body.Bytes()); n > 4000 {
		t.Fatalf("body is %d characters, past the bound", n)
	}
	m := map[string]any{}
	if err := json.Unmarshal(response.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	relations, _ := m["relation_types"].([]any)
	if len(relations) == 0 || len(relations) >= 50 || m["next_offset"] != float64(10+len(relations)) || m["notice"] == nil {
		t.Fatalf("kept %d, next_offset %v, notice %v; want a cut page that continues where it stopped",
			len(relations), m["next_offset"], m["notice"])
	}
}
