// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

//go:build integration

package opensearchprojection_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchprojection"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionoutbox"
)

func TestOpenSearchProjectionVersionValidationAndAliasSwitch(t *testing.T) {
	endpoint := os.Getenv("BKN_TRACE_TEST_OPENSEARCH_ENDPOINT")
	if endpoint == "" {
		t.Skip("BKN_TRACE_TEST_OPENSEARCH_ENDPOINT is not set")
	}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	alias := "bkn-trace-core-integration-" + suffix
	version := alias + "-v1"
	username := os.Getenv("BKN_TRACE_TEST_OPENSEARCH_USERNAME")
	client := opensearch.New(endpoint, opensearch.AuthConfig{
		Enabled:  username != "",
		Username: username,
		Password: os.Getenv("BKN_TRACE_TEST_OPENSEARCH_PASSWORD"),
	}, 10*time.Second)
	sink := opensearchprojection.New(client, alias)
	item := iprojectionoutbox.Item{
		AggregateType:    "interaction",
		AggregateID:      "interaction-" + suffix,
		AggregateVersion: 2,
		EventType:        "interaction.snapshot",
		EventID:          "interaction:" + suffix,
		Payload:          []byte(`{"state":"completed","evidence_status":"complete"}`),
	}

	if err := sink.PrepareVersion(context.Background(), version); err != nil {
		t.Fatalf("prepare OpenSearch version: %v", err)
	}
	if err := sink.ProjectVersion(context.Background(), version, item); err != nil {
		t.Fatalf("project OpenSearch version: %v", err)
	}
	stale := item
	stale.AggregateVersion = 1
	stale.Payload = []byte(`{"state":"active","evidence_status":"assembling"}`)
	if err := sink.ProjectVersion(context.Background(), version, stale); err != nil {
		t.Fatalf("stale projection must be acknowledged without overwrite: %v", err)
	}
	divergent := item
	divergent.Payload = []byte(`{"state":"failed","evidence_status":"incomplete"}`)
	if err := sink.ProjectVersion(context.Background(), version, divergent); err != nil {
		t.Fatalf("same-version projection must be acknowledged without overwrite: %v", err)
	}
	if err := sink.ValidateVersion(
		context.Background(), version, []iprojectionoutbox.Item{item},
	); err != nil {
		t.Fatalf("validate OpenSearch version: %v", err)
	}
	count, err := sink.CountVersion(context.Background(), version)
	if err != nil || count != 1 {
		t.Fatalf("count OpenSearch version: count=%d err=%v", count, err)
	}
	if err := sink.SwitchAlias(context.Background(), alias, version); err != nil {
		t.Fatalf("switch OpenSearch alias: %v", err)
	}
	document, err := client.GetDocument(
		context.Background(), alias, iprojectionoutbox.DocumentID(item),
	)
	if err != nil {
		t.Fatalf("read projection through alias: %v", err)
	}
	if !canonicalEqual(document.Source, item.Payload) {
		t.Fatalf("stale projection regressed alias document: %s", document.Source)
	}
}

func canonicalEqual(left, right []byte) bool {
	var leftValue, rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return false
	}
	leftCanonical, _ := json.Marshal(leftValue)
	rightCanonical, _ := json.Marshal(rightValue)
	return bytes.Equal(leftCanonical, rightCanonical)
}

// This regression uses only fresh, randomly named test-owned indexes in the
// configured existing OpenSearch instance; cleanup never deletes other indexes.
func TestOpenSearchBootstrapPreservesLegacyEnvelopeMappings(t *testing.T) {
	endpoint := strings.TrimRight(os.Getenv("BKN_TRACE_TEST_OPENSEARCH_ENDPOINT"), "/")
	if endpoint == "" {
		t.Skip("BKN_TRACE_TEST_OPENSEARCH_ENDPOINT is not set")
	}
	username := os.Getenv("BKN_TRACE_TEST_OPENSEARCH_USERNAME")
	password := os.Getenv("BKN_TRACE_TEST_OPENSEARCH_PASSWORD")
	httpClient := &http.Client{Timeout: 10 * time.Second}
	request := func(method, path string, body []byte) (int, []byte, error) {
		req, err := http.NewRequestWithContext(context.Background(), method, endpoint+path, bytes.NewReader(body))
		if err != nil {
			return 0, nil, err
		}
		if username != "" {
			req.SetBasicAuth(username, password)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := httpClient.Do(req)
		if err != nil {
			return 0, nil, err
		}
		defer func() { _ = resp.Body.Close() }()
		response, err := io.ReadAll(resp.Body)
		return resp.StatusCode, response, err
	}
	for _, kind := range []string{"scalar", "disabled", "object"} {
		t.Run(kind, func(t *testing.T) {
			var nonce [8]byte
			if _, err := rand.Read(nonce[:]); err != nil {
				t.Fatal(err)
			}
			index := "bkn-trace-bootstrap-test-" + hex.EncodeToString(nonce[:])
			alias := index + "-alias"
			field := map[string]any{"type": "object"}
			if kind == "scalar" {
				field["type"] = "keyword"
			}
			if kind == "disabled" {
				field["enabled"] = false
			}
			if kind == "object" {
				field["properties"] = map[string]any{"legacy": map[string]any{"type": "long"}}
			}
			settings := map[string]any{"number_of_shards": 1, "number_of_replicas": 0}
			if kind == "object" {
				settings["index.mapping.total_fields.limit"] = 24
			}
			definition, _ := json.Marshal(map[string]any{"settings": settings, "mappings": map[string]any{"properties": map[string]any{"envelope": field}}, "aliases": map[string]any{alias: map[string]any{}}})
			status, _, err := request(http.MethodPut, "/"+index, definition)
			if err != nil || status < 200 || status >= 300 {
				t.Fatalf("create test-owned index: status=%d err=%v", status, err)
			}
			t.Cleanup(func() {
				status, _, err := request(http.MethodDelete, "/"+index, nil)
				if err != nil || status < 200 || status >= 300 {
					t.Errorf("delete only test-owned index: status=%d err=%v", status, err)
				}
			})
			arbitrary := map[string]any{}
			for i := 0; i < 40; i++ {
				arbitrary[fmt.Sprintf("opaque_%d", i)] = i
			}
			var envelope any = map[string]any{"legacy": 7, "arbitrary": arbitrary}
			if kind == "scalar" {
				envelope = "legacy scalar envelope"
			}
			payload, _ := json.Marshal(map[string]any{"event_id": "owned-event", "envelope": envelope})
			if kind == "object" {
				status, response, err := request(http.MethodPut, "/"+index+"/_doc/before-boundary", payload)
				if err != nil || status != http.StatusBadRequest || !strings.Contains(string(response), "total fields") {
					t.Fatalf("baseline dynamic growth must exceed unchanged test field limit: status=%d err=%v", status, err)
				}
			}
			client := opensearch.New(endpoint, opensearch.AuthConfig{Enabled: username != "", Username: username, Password: password}, 10*time.Second)
			sink := opensearchprojection.New(client, alias)
			if err := sink.EnsureBootstrap(context.Background(), "unused-version"); err != nil {
				t.Fatal(err)
			}
			mappings, err := client.GetMappings(context.Background(), alias)
			if err != nil || len(mappings) != 1 {
				t.Fatalf("bootstrap must preserve the sole original alias target: targets=%d err=%v", len(mappings), err)
			}
			var mapping struct {
				Properties map[string]map[string]any `json:"properties"`
			}
			if err := json.Unmarshal(mappings[index], &mapping); err != nil {
				t.Fatal(err)
			}
			got := mapping.Properties["envelope"]
			switch kind {
			case "scalar":
				if got["type"] != "keyword" {
					t.Fatal("scalar envelope type changed")
				}
			case "disabled":
				if got["enabled"] != false {
					t.Fatal("disabled envelope was re-enabled")
				}
			case "object":
				if got["dynamic"] != false && got["dynamic"] != "false" {
					t.Fatal("object envelope lacks dynamic boundary")
				}
				properties, ok := got["properties"].(map[string]any)
				if !ok || properties["legacy"].(map[string]any)["type"] != "long" {
					t.Fatal("legacy inner field type changed")
				}
			}
			item := iprojectionoutbox.Item{AggregateType: "evidence_event", AggregateID: "owned-event", AggregateVersion: 1, Payload: payload}
			if err := sink.Project(context.Background(), item); err != nil {
				t.Fatal(err)
			}
			document, err := client.GetDocument(context.Background(), alias, iprojectionoutbox.DocumentID(item))
			if err != nil || !canonicalEqual(document.Source, payload) {
				t.Fatalf("opaque source changed: err=%v", err)
			}
			if err := sink.ValidateVersion(context.Background(), index, []iprojectionoutbox.Item{item}); err != nil {
				t.Fatal(err)
			}
			// A second bootstrap must remain safe against its own existing mapping.
			if err := sink.EnsureBootstrap(context.Background(), "unused-version"); err != nil {
				t.Fatal(err)
			}
		})
	}
}
