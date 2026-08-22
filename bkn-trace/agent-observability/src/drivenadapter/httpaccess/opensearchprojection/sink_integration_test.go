// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

//go:build integration

package opensearchprojection_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
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
