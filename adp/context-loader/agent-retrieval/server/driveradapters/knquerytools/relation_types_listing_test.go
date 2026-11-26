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

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
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
	// empty, when set, makes the listing find nothing.
	empty bool
}

func (b *relationListingRESTBackend) ListRelationTypesBranch(_ context.Context, _, branch string, objectTypeIDs []string,
	_, limit int) (*interfaces.RelationTypePage, error) {
	b.gotBranch, b.gotObjectTypes, b.gotLimit = branch, objectTypeIDs, limit
	if b.empty {
		return &interfaces.RelationTypePage{}, nil
	}
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

type objectListingRESTBackend struct {
	interfaces.BknBackendAccess
	total int
}

func (b *objectListingRESTBackend) ListObjectTypes(_ context.Context, _ string, offset, limit int) (*interfaces.ObjectTypePage, error) {
	page := &interfaces.ObjectTypePage{TotalCount: int64(b.total)}
	for i := offset; i < offset+limit && i < b.total; i++ {
		page.Entries = append(page.Entries, &interfaces.ObjectType{ID: fmt.Sprintf("ot-%04d", i), Name: "订单",
			Comment: strings.Repeat("订单是客户下的购买记录，按订单编号关联。", 30)})
	}
	page.Scanned = len(page.Entries)
	return page, nil
}

// The REST face shortens listed comments, cuts a page past the size bound and a walk over
// the cut pages returns every object type once.
func TestGetObjectTypesRESTCutsAPageAndWalks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	saved := interfaces.MaxObjectTypePageChars
	interfaces.MaxObjectTypePageChars = 4000
	t.Cleanup(func() { interfaces.MaxObjectTypePageChars = saved })

	handler := &knQueryToolsHandler{bknBackend: &objectListingRESTBackend{total: 60}}
	router := gin.New()
	router.POST("/kn/get_object_types", handler.GetObjectTypes)
	seen := map[string]bool{}
	offset, calls := 0, 0
	for {
		calls++
		if calls > 60 {
			t.Fatal("the walk does not end")
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/kn/get_object_types",
			strings.NewReader(fmt.Sprintf(`{"kn_id":"kn-1","offset":%d,"limit":200}`, offset))))
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
		for _, o := range m["object_types"].([]any) {
			entry := o.(map[string]any)
			if c, _ := entry["comment"].(string); utf8.RuneCountInString(c) != interfaces.ObjectTypeIndexCommentRunes+1 {
				t.Fatalf("a listed comment has %d runes", utf8.RuneCountInString(c))
			}
			id := entry["id"].(string)
			if seen[id] {
				t.Fatalf("%s came back twice", id)
			}
			seen[id] = true
		}
		next, more := m["next_offset"].(float64)
		if !more {
			break
		}
		if m["notice"] == nil {
			t.Fatalf("a cut page must say why: %v", m)
		}
		offset = int(next)
	}
	if len(seen) != 60 || calls < 2 {
		t.Fatalf("the walk saw %d of 60 object types in %d calls", len(seen), calls)
	}
}

// An object type filter that matches no relation type answers with an empty list and
// total_count 0, in JSON and TOON, instead of leaving the total out (#3414).
func TestGetRelationTypesRESTSaysTotalCountZeroForAnEmptyListing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, format := range []rest.ResponseFormat{rest.FormatJSON, rest.FormatTOON} {
		handler := &knQueryToolsHandler{bknBackend: &relationListingRESTBackend{empty: true}}
		router := gin.New()
		router.Use(func(c *gin.Context) {
			c.Request = c.Request.WithContext(common.SetResponseFormatToCtx(c.Request.Context(), format))
			c.Next()
		})
		router.POST("/kn/get_relation_types", handler.GetRelationTypes)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/kn/get_relation_types", strings.NewReader(
			`{"kn_id":"kn-1","branch":"main","object_type_ids":["ot003"],"offset":0,"limit":50}`)))
		if response.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, body = %s", format, response.Code, response.Body.String())
		}
		body := response.Body.String()
		if format == rest.FormatTOON {
			if !strings.Contains(body, "total_count: 0") || strings.Contains(body, "next_offset") {
				t.Fatalf("TOON body = %q, want total_count: 0 and no next_offset", body)
			}
			continue
		}
		m := map[string]any{}
		if err := json.Unmarshal(response.Body.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		relations, ok := m["relation_types"].([]any)
		if total, present := m["total_count"]; !ok || len(relations) != 0 || !present || total != float64(0) {
			t.Fatalf("JSON body = %v, want relation_types [] and total_count 0", m)
		}
		if _, present := m["next_offset"]; present {
			t.Fatalf("an empty listing has no next page: %v", m)
		}
	}
}
