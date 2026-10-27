// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledge_base

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// fakeKBAPI serves kb-api's two in-cluster identity endpoints over a fixed set of knowledge
// bases. The listing matches names by substring and pages like kb-api does.
func fakeKBAPI(t *testing.T, names map[string]string, status int) (*httptest.Server, *[]string) {
	t.Helper()
	var requests []string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/kb/in/v1/knowledge-bases/", func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		id := r.URL.Path[len("/api/kb/in/v1/knowledge-bases/"):]
		name, ok := names[id]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"id": id, "name": name})
	})
	mux.HandleFunc("/api/kb/in/v1/authorization-resources", func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.RequestURI())
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		if r.URL.Query().Get("resource_type") != "knowledge_base" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		want := r.URL.Query().Get("name")
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		all := make([]map[string]string, 0)
		for i := 1; i <= len(names); i++ {
			id := strconv.Itoa(i)
			if name, ok := names[id]; ok && (want == "" || strings.Contains(name, want)) {
				all = append(all, map[string]string{"id": id, "name": name})
			}
		}
		end := min(offset+limit, len(all))
		page := []map[string]string{}
		if offset < len(all) {
			page = all[offset:end]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"entries": page, "total": len(all)})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server, &requests
}

func TestGetKnowledgeBase(t *testing.T) {
	server, requests := fakeKBAPI(t, map[string]string{"1": "合同库"}, http.StatusOK)
	access := NewKnowledgeBaseAccess(server.URL + "/")

	kb, err := access.GetKnowledgeBase(t.Context(), "1")
	if err != nil || kb == nil || kb.ID != "1" || kb.Name != "合同库" {
		t.Fatalf("GetKnowledgeBase(1) = %+v, %v", kb, err)
	}
	kb, err = access.GetKnowledgeBase(t.Context(), "9")
	if err != nil || kb != nil {
		t.Fatalf("GetKnowledgeBase(missing) = %+v, %v; want nil, nil", kb, err)
	}
	if (*requests)[0] != "/api/kb/in/v1/knowledge-bases/1" {
		t.Fatalf("request path = %q", (*requests)[0])
	}
}

func TestFindKnowledgeBasesByNameKeepsExactMatchesAcrossPages(t *testing.T) {
	names := map[string]string{}
	for i := 1; i <= 250; i++ {
		names[strconv.Itoa(i)] = fmt.Sprintf("合同库-%d", i)
	}
	names["7"] = "合同库"
	names["240"] = "合同库"
	server, requests := fakeKBAPI(t, names, http.StatusOK)
	access := NewKnowledgeBaseAccess(server.URL)

	found, err := access.FindKnowledgeBasesByName(t.Context(), " 合同库 ")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 || found[0].ID != "7" || found[1].ID != "240" {
		t.Fatalf("FindKnowledgeBasesByName = %+v; want ids 7 and 240", found)
	}
	if len(*requests) != 3 {
		t.Fatalf("requests = %d; want three pages for 250 substring matches", len(*requests))
	}

	none, err := access.FindKnowledgeBasesByName(t.Context(), "合同")
	if err != nil || len(none) != 0 {
		t.Fatalf("a substring-only name = %+v, %v; want no exact match", none, err)
	}
}

func TestUnconfiguredKnowledgeBaseServiceIsUnavailable(t *testing.T) {
	access := NewKnowledgeBaseAccess("  ")
	if _, err := access.GetKnowledgeBase(t.Context(), "1"); !errors.Is(err, interfaces.ErrKnowledgeBaseUnavailable) {
		t.Fatalf("GetKnowledgeBase = %v; want ErrKnowledgeBaseUnavailable", err)
	}
	if _, err := access.FindKnowledgeBasesByName(t.Context(), "x"); !errors.Is(err, interfaces.ErrKnowledgeBaseUnavailable) {
		t.Fatalf("FindKnowledgeBasesByName = %v; want ErrKnowledgeBaseUnavailable", err)
	}
}

func TestKBAPIFailuresAreErrors(t *testing.T) {
	server, _ := fakeKBAPI(t, map[string]string{"1": "合同库"}, http.StatusServiceUnavailable)
	access := NewKnowledgeBaseAccess(server.URL)
	if kb, err := access.GetKnowledgeBase(t.Context(), "1"); err == nil || kb != nil {
		t.Fatalf("GetKnowledgeBase on 503 = %+v, %v; want an error", kb, err)
	}
	if found, err := access.FindKnowledgeBasesByName(t.Context(), "合同库"); err == nil || found != nil {
		t.Fatalf("FindKnowledgeBasesByName on 503 = %+v, %v; want an error", found, err)
	}
}
