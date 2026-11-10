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
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knmetrics"
)

type detailRESTBackend struct {
	interfaces.BknBackendAccess
	detail *interfaces.KnowledgeNetworkDetail
}

func (b *detailRESTBackend) GetKnowledgeNetworkDetail(context.Context, string) (*interfaces.KnowledgeNetworkDetail, error) {
	return b.detail, nil
}

func (b *detailRESTBackend) ListKNCapabilities(context.Context, string, string, string) ([]*interfaces.CapabilityRef, error) {
	return nil, nil
}

func (b *detailRESTBackend) ListMetricsByObjectTypes(context.Context, string, []string) ([]*interfaces.RelatedMetric, error) {
	return nil, nil
}

type allowReads struct{}

func (allowReads) AuthorizeRead(context.Context, string) error { return nil }

type grantAll struct{ properties int }

func (g grantAll) GetObjectTypeSchema(context.Context, string, string) (*interfaces.ObjectTypeSchemaResp, error) {
	permissions := map[string]interfaces.PropertyAccessLevel{}
	for j := range g.properties {
		permissions[fmt.Sprintf("property_%02d", j)] = interfaces.PropertyAccessFull
	}
	return &interfaces.ObjectTypeSchemaResp{EffectivePermissions: permissions}, nil
}

func restNetwork(objectTypes, properties int) *interfaces.KnowledgeNetworkDetail {
	detail := &interfaces.KnowledgeNetworkDetail{ID: "kn-1", Name: "供应链", Comment: "采购、库存与交付",
		ConceptGroups: []*interfaces.ConceptGroup{{ID: "cg-1", Name: "采购"}}}
	for i := range objectTypes {
		o := &interfaces.ObjectType{ID: fmt.Sprintf("ot-%04d", i), Name: fmt.Sprintf("对象类-%04d", i),
			DataSource: &interfaces.ResourceInfo{Type: "resource", ID: "res"}}
		for j := range properties {
			o.DataProperties = append(o.DataProperties, &interfaces.DataProperty{Name: fmt.Sprintf("property_%02d", j), Type: "string"})
		}
		detail.ObjectTypes = append(detail.ObjectTypes, o)
	}
	return detail
}

func getKnDetailREST(t *testing.T, detail *interfaces.KnowledgeNetworkDetail, properties int, body string) (string, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	backend := &detailRESTBackend{detail: detail}
	handler := &knQueryToolsHandler{
		bknBackend:   backend,
		metrics:      knmetrics.NewKnMetricsServiceWith(nil, backend, nil),
		schemaAccess: grantAll{properties: properties},
		knAuthz:      allowReads{},
	}
	router := gin.New()
	router.POST("/kn/get_kn_detail", handler.GetKnDetail)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/kn/get_kn_detail", strings.NewReader(body)))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	m := map[string]any{}
	if err := json.Unmarshal(response.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	return response.Body.String(), m
}

// The REST route is the same tool on another face, so it answers within the same limit.
func TestGetKnDetailRESTNarrowsWithinTheCharacterLimit(t *testing.T) {
	raw, m := getKnDetailREST(t, restNetwork(60, 40), 40, `{"kn_id":"kn-1"}`)
	if n := utf8.RuneCountInString(raw); n > interfaces.MaxKnDetailChars {
		t.Fatalf("answer is %d characters, past the %d limit", n, interfaces.MaxKnDetailChars)
	}
	objectTypes, _ := m["object_types"].([]any)
	if len(objectTypes) != 60 {
		t.Fatalf("outline carries %d object types, want 60", len(objectTypes))
	}
	if first, _ := objectTypes[0].(map[string]any); first["data_property_count"] != float64(40) {
		t.Fatalf("want property counts in place of lists, got %v", first)
	}
	if notice, _ := m["notice"].(string); notice == "" {
		t.Fatalf("a narrowed answer must say so")
	}
}

func TestGetKnDetailRESTBrief(t *testing.T) {
	_, m := getKnDetailREST(t, restNetwork(2, 1), 1, `{"kn_id":"kn-1","detail_level":"brief"}`)
	if m["name"] != "供应链" || m["comment"] != "采购、库存与交付" {
		t.Fatalf("brief must carry the network's name and description, got %v / %v", m["name"], m["comment"])
	}
	if got, present := m["object_types"]; !present || got != nil {
		t.Fatalf("object_types should be present and null, got %v", got)
	}
	if m["object_type_count"] != float64(2) {
		t.Fatalf("object_type_count = %v, want 2", m["object_type_count"])
	}
}
