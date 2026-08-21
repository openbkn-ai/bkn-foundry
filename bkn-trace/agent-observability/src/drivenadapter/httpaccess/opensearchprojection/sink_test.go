package opensearchprojection_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchprojection"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionoutbox"
)

func TestSinkIndexesByStableAggregateID(t *testing.T) {
	t.Parallel()

	var path string
	var query string
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		query = r.URL.RawQuery
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(server.Close)
	client := opensearch.New(server.URL, opensearch.AuthConfig{}, time.Second)
	sink := opensearchprojection.New(client, "bkn-trace-core-v3")
	item := iprojectionoutbox.Item{
		ID: 1, AggregateType: "interaction", AggregateID: "int-1",
		AggregateVersion: 7,
		EventID:          "evt-1", EventType: "interaction.completed",
		Payload: []byte(`{"interaction_id":"int-1"}`),
	}

	if err := sink.Project(context.Background(), item); err != nil {
		t.Fatalf("project: %v", err)
	}
	if path != "/bkn-trace-core-v3/_doc/interaction:int-1" || string(body) != string(item.Payload) {
		t.Fatalf("unexpected projection request: path=%q body=%s", path, body)
	}
	if query != "version=7&version_type=external" {
		t.Fatalf("projection did not enforce aggregate version: %q", query)
	}
}

func TestSinkAcknowledgesStaleAggregateVersionWithoutRetry(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "version_conflict_engine_exception", http.StatusConflict)
	}))
	t.Cleanup(server.Close)
	sink := opensearchprojection.New(
		opensearch.New(server.URL, opensearch.AuthConfig{}, time.Second),
		"bkn-trace-core",
	)
	err := sink.Project(context.Background(), iprojectionoutbox.Item{
		AggregateType: "interaction", AggregateID: "int-1",
		AggregateVersion: 1, Payload: []byte(`{"row_version":1}`),
	})
	if err != nil {
		t.Fatalf("stale aggregate version must be safely superseded: %v", err)
	}
}

func TestPrepareVersionDefinesMappingsRequiredByEmptyReceiptQuery(t *testing.T) {
	t.Parallel()

	var mapping map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/bkn-trace-core-v014" {
			t.Fatalf("unexpected index preparation request: %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&mapping); err != nil {
			t.Fatalf("decode index mapping: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(server.Close)

	sink := opensearchprojection.New(
		opensearch.New(server.URL, opensearch.AuthConfig{}, time.Second),
		"bkn-trace-core",
	)
	if err := sink.PrepareVersion(context.Background(), "bkn-trace-core-v014"); err != nil {
		t.Fatalf("prepare projection index: %v", err)
	}

	properties, ok := mapping["mappings"].(map[string]any)["properties"].(map[string]any)
	if !ok {
		t.Fatalf("receipt projection mapping must define properties: %#v", mapping)
	}
	issuedAt, ok := properties["issued_at"].(map[string]any)
	if !ok || issuedAt["type"] != "date" {
		t.Fatalf("issued_at must be mapped as date for empty-index sorting: %#v", issuedAt)
	}
	requestID, ok := properties["request_id"].(map[string]any)
	if !ok || requestID["fields"].(map[string]any)["keyword"].(map[string]any)["type"] != "keyword" {
		t.Fatalf("request_id must retain the exact keyword subfield used by summary queries: %#v", requestID)
	}
	receiptStatus, ok := properties["receipt_status"].(map[string]any)
	if !ok || receiptStatus["fields"].(map[string]any)["keyword"].(map[string]any)["type"] != "keyword" {
		t.Fatalf("receipt_status must retain the exact keyword subfield used by runtime logs: %#v", receiptStatus)
	}
	terminalAt, ok := properties["terminal_at"].(map[string]any)
	if !ok || terminalAt["type"] != "date" {
		t.Fatalf("terminal_at must be mapped as date for runtime-log sorting: %#v", terminalAt)
	}
	createdAt, ok := properties["created_at"].(map[string]any)
	if !ok || createdAt["type"] != "date" {
		t.Fatalf("created_at must be mapped as date for empty-index conversation sorting: %#v", createdAt)
	}
	externalConversationKey, ok := properties["external_conversation_key"].(map[string]any)
	if !ok || externalConversationKey["type"] != "text" || externalConversationKey["fields"].(map[string]any)["keyword"].(map[string]any)["type"] != "keyword" {
		t.Fatalf("external_conversation_key must preserve the legacy dynamic text+keyword mapping: %#v", externalConversationKey)
	}
	generation, ok := properties["generation"].(map[string]any)
	if !ok || generation["type"] != "long" {
		t.Fatalf("generation must be mapped for empty-index conversation filtering: %#v", generation)
	}
}

func TestEnsureBootstrapCreatesVersionedIndexAndAliasWhenNeitherExists(t *testing.T) {
	t.Parallel()

	requests := make([]string, 0, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch r.Method + " " + r.URL.Path {
		case http.MethodGet + " /_alias/bkn-trace-core":
			http.NotFound(w, r)
		case http.MethodHead + " /bkn-trace-core":
			w.WriteHeader(http.StatusNotFound)
		case http.MethodPut + " /bkn-trace-core-v014-r1":
			w.WriteHeader(http.StatusCreated)
		case http.MethodPost + " /_aliases":
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected bootstrap request: %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)

	sink := opensearchprojection.New(opensearch.New(server.URL, opensearch.AuthConfig{}, time.Second), "bkn-trace-core")
	if err := sink.EnsureBootstrap(context.Background(), "bkn-trace-core-v014-r1"); err != nil {
		t.Fatalf("bootstrap projection alias: %v", err)
	}
	want := []string{
		"GET /_alias/bkn-trace-core",
		"HEAD /bkn-trace-core",
		"PUT /bkn-trace-core-v014-r1",
		"POST /_aliases",
	}
	if len(requests) != len(want) {
		t.Fatalf("unexpected bootstrap sequence: got=%v want=%v", requests, want)
	}
	for index := range want {
		if requests[index] != want[index] {
			t.Fatalf("unexpected bootstrap sequence: got=%v want=%v", requests, want)
		}
	}
}

func TestEnsureBootstrapDoesNotReplaceAnExistingAlias(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case http.MethodGet + " /_alias/bkn-trace-core", http.MethodPut + " /bkn-trace-core/_mapping":
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("bootstrap changed an existing alias: %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)

	sink := opensearchprojection.New(opensearch.New(server.URL, opensearch.AuthConfig{}, time.Second), "bkn-trace-core")
	if err := sink.EnsureBootstrap(context.Background(), "bkn-trace-core-v014-r1"); err != nil {
		t.Fatalf("accept existing projection alias: %v", err)
	}
}

func TestEnsureBootstrapAddsConversationMappingToExistingAlias(t *testing.T) {
	t.Parallel()

	requests := make([]string, 0, 2)
	var mapping map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch r.Method + " " + r.URL.Path {
		case http.MethodGet + " /_alias/bkn-trace-core":
			w.WriteHeader(http.StatusOK)
		case http.MethodPut + " /bkn-trace-core/_mapping":
			if err := json.NewDecoder(r.Body).Decode(&mapping); err != nil {
				t.Fatalf("decode mapping update: %v", err)
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected bootstrap request: %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)

	sink := opensearchprojection.New(opensearch.New(server.URL, opensearch.AuthConfig{}, time.Second), "bkn-trace-core")
	if err := sink.EnsureBootstrap(context.Background(), "bkn-trace-core-v014-r1"); err != nil {
		t.Fatalf("upgrade existing projection alias: %v", err)
	}
	want := []string{"GET /_alias/bkn-trace-core", "PUT /bkn-trace-core/_mapping"}
	if len(requests) != len(want) {
		t.Fatalf("unexpected existing-alias upgrade requests: got=%v want=%v", requests, want)
	}
	for index := range want {
		if requests[index] != want[index] {
			t.Fatalf("unexpected existing-alias upgrade requests: got=%v want=%v", requests, want)
		}
	}
	properties, ok := mapping["properties"].(map[string]any)
	if !ok {
		t.Fatalf("mapping update must define properties: %#v", mapping)
	}
	createdAt, ok := properties["created_at"].(map[string]any)
	if !ok || createdAt["type"] != "date" {
		t.Fatalf("created_at must be mapped as date for empty-index conversation sorting: %#v", createdAt)
	}
}

func TestEnsureBootstrapRejectsAConcreteIndexWithTheAliasName(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case http.MethodGet + " /_alias/bkn-trace-core":
			http.NotFound(w, r)
		case http.MethodHead + " /bkn-trace-core":
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("bootstrap must not mutate a concrete alias-name index: %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)

	sink := opensearchprojection.New(opensearch.New(server.URL, opensearch.AuthConfig{}, time.Second), "bkn-trace-core")
	err := sink.EnsureBootstrap(context.Background(), "bkn-trace-core-v014-r1")
	if err == nil || !strings.Contains(err.Error(), "concrete index") {
		t.Fatalf("bootstrap must reject concrete index collision: %v", err)
	}
}

func TestValidateVersionRejectsCorruptedDocumentContent(t *testing.T) {
	t.Parallel()

	client := opensearch.NewWithHTTPClient(
		"http://opensearch.test",
		opensearch.AuthConfig{},
		&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.Path != "/bkn-trace-core-v4/_mget" {
				t.Fatalf("unexpected validation path: %s", request.URL.Path)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(bytes.NewBufferString(
					`{"docs":[{"_id":"interaction:int-1","found":true,"_source":{"state":"failed"}}]}`,
				)),
				Header: make(http.Header),
			}, nil
		})},
	)
	sink := opensearchprojection.New(client, "bkn-trace-core")
	err := sink.ValidateVersion(context.Background(), "bkn-trace-core-v4", []iprojectionoutbox.Item{{
		AggregateType: "interaction",
		AggregateID:   "int-1",
		Payload:       []byte(`{"state":"completed"}`),
	}})
	if err == nil {
		t.Fatal("corrupted projection content must fail validation")
	}
}

func TestValidateVersionAcceptsCanonicalEquivalentJSON(t *testing.T) {
	t.Parallel()

	client := opensearch.NewWithHTTPClient(
		"http://opensearch.test",
		opensearch.AuthConfig{},
		&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(bytes.NewBufferString(
					`{"docs":[{"_id":"interaction:int-1","found":true,"_source":{"state":"completed","count":1}}]}`,
				)),
				Header: make(http.Header),
			}, nil
		})},
	)
	sink := opensearchprojection.New(client, "bkn-trace-core")
	err := sink.ValidateVersion(context.Background(), "bkn-trace-core-v4", []iprojectionoutbox.Item{{
		AggregateType: "interaction",
		AggregateID:   "int-1",
		Payload:       []byte(`{"count":1,"state":"completed"}`),
	}})
	if err != nil {
		t.Fatalf("canonical equivalent projection content: %v", err)
	}
}

func TestValidateVersionUsesHighestAggregateVersionPerDocument(t *testing.T) {
	t.Parallel()

	var requestedIDs []string
	client := opensearch.NewWithHTTPClient(
		"http://opensearch.test",
		opensearch.AuthConfig{},
		&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			var body struct {
				IDs []string `json:"ids"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatalf("decode multi-get request: %v", err)
			}
			requestedIDs = body.IDs
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(bytes.NewBufferString(
					`{"docs":[{"_id":"interaction:int-1","found":true,"_source":{"row_version":3}}]}`,
				)),
				Header: make(http.Header),
			}, nil
		})},
	)
	sink := opensearchprojection.New(client, "bkn-trace-core")
	err := sink.ValidateVersion(
		context.Background(),
		"bkn-trace-core-v4",
		[]iprojectionoutbox.Item{
			{
				AggregateType: "interaction", AggregateID: "int-1",
				AggregateVersion: 2, Payload: []byte(`{"row_version":2}`),
			},
			{
				AggregateType: "interaction", AggregateID: "int-1",
				AggregateVersion: 3, Payload: []byte(`{"row_version":3}`),
			},
		},
	)
	if err != nil {
		t.Fatalf("validate highest aggregate version: %v", err)
	}
	if len(requestedIDs) != 1 || requestedIDs[0] != "interaction:int-1" {
		t.Fatalf("multi-get request was not deduplicated: %#v", requestedIDs)
	}
}

func TestValidateVersionRejectsSameVersionDifferentPayload(t *testing.T) {
	t.Parallel()

	requested := false
	client := opensearch.NewWithHTTPClient(
		"http://opensearch.test",
		opensearch.AuthConfig{},
		&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requested = true
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(bytes.NewBufferString(
					`{"docs":[{"_id":"interaction:int-1","found":true,"_source":{"state":"failed"}}]}`,
				)),
				Header: make(http.Header),
			}, nil
		})},
	)
	sink := opensearchprojection.New(client, "bkn-trace-core")
	err := sink.ValidateVersion(
		context.Background(),
		"bkn-trace-core-v4",
		[]iprojectionoutbox.Item{
			{
				AggregateType: "interaction", AggregateID: "int-1",
				AggregateVersion: 3, Payload: []byte(`{"state":"completed"}`),
			},
			{
				AggregateType: "interaction", AggregateID: "int-1",
				AggregateVersion: 3, Payload: []byte(`{"state":"failed"}`),
			},
		},
	)
	if err == nil {
		t.Fatal("same aggregate version with different payload must fail validation")
	}
	if requested {
		t.Fatal("contract conflict must be rejected before querying OpenSearch")
	}
}

func TestValidateVersionRejectsOlderVersionPayloadConflictAfterNewerVersion(t *testing.T) {
	t.Parallel()

	requested := false
	client := opensearch.NewWithHTTPClient(
		"http://opensearch.test",
		opensearch.AuthConfig{},
		&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requested = true
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(bytes.NewBufferString(
					`{"docs":[{"_id":"interaction:int-1","found":true,"_source":{"state":"completed"}}]}`,
				)),
				Header: make(http.Header),
			}, nil
		})},
	)
	sink := opensearchprojection.New(client, "bkn-trace-core")
	err := sink.ValidateVersion(
		context.Background(),
		"bkn-trace-core-v4",
		[]iprojectionoutbox.Item{
			{
				AggregateType: "interaction", AggregateID: "int-1",
				AggregateVersion: 2, Payload: []byte(`{"state":"active"}`),
			},
			{
				AggregateType: "interaction", AggregateID: "int-1",
				AggregateVersion: 3, Payload: []byte(`{"state":"completed"}`),
			},
			{
				AggregateType: "interaction", AggregateID: "int-1",
				AggregateVersion: 2, Payload: []byte(`{"state":"failed"}`),
			},
		},
	)
	if err == nil {
		t.Fatal("older aggregate version with conflicting payload must fail validation")
	}
	if requested {
		t.Fatal("contract conflict must be rejected before querying OpenSearch")
	}
}

func TestSinkClassifiesInvalidDocumentAsPermanent(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "mapper_parsing_exception", http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)
	sink := opensearchprojection.New(
		opensearch.New(server.URL, opensearch.AuthConfig{}, time.Second),
		"bkn-trace-core",
	)

	err := sink.Project(context.Background(), iprojectionoutbox.Item{
		AggregateType: "evidence_event", AggregateID: "evt-invalid",
		AggregateVersion: 1, EventID: "evt-invalid", Payload: []byte(`{"broken":true}`),
	})
	if !iprojectionoutbox.IsPermanent(err) {
		t.Fatalf("invalid document error was not classified as permanent: %v", err)
	}
}

func TestSinkLeavesOpenSearchOutageRetryable(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	sink := opensearchprojection.New(
		opensearch.New(server.URL, opensearch.AuthConfig{}, time.Second),
		"bkn-trace-core",
	)

	err := sink.Project(context.Background(), iprojectionoutbox.Item{
		AggregateType: "evidence_event", AggregateID: "evt-retry",
		AggregateVersion: 1, EventID: "evt-retry", Payload: []byte(`{"valid":true}`),
	})
	if err == nil || iprojectionoutbox.IsPermanent(err) {
		t.Fatalf("OpenSearch outage must remain retryable: %v", err)
	}
}

func TestCountVersionRefreshesIndexBeforeValidation(t *testing.T) {
	t.Parallel()

	var paths []string
	client := opensearch.NewWithHTTPClient(
		"http://opensearch.test",
		opensearch.AuthConfig{},
		&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			paths = append(paths, request.URL.Path)
			body := `{}`
			if request.URL.Path == "/bkn-trace-core-v4/_search" {
				body = `{"hits":{"total":{"value":1}}}`
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBufferString(body)),
				Header:     make(http.Header),
			}, nil
		})},
	)
	sink := opensearchprojection.New(client, "bkn-trace-core")

	count, err := sink.CountVersion(context.Background(), "bkn-trace-core-v4")
	if err != nil || count != 1 {
		t.Fatalf("count rebuilt version: count=%d err=%v", count, err)
	}
	if len(paths) != 2 ||
		paths[0] != "/bkn-trace-core-v4/_refresh" ||
		paths[1] != "/bkn-trace-core-v4/_search" {
		t.Fatalf("version was not refreshed before validation: %#v", paths)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
