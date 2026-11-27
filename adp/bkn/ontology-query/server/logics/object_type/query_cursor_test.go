// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package object_type

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common/cursorcodec"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

func testQueryCursorCodec(t *testing.T, now time.Time) *queryCursorCodec {
	t.Helper()
	opaque, err := cursorcodec.New([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	return &queryCursorCodec{opaque: opaque, now: func() time.Time { return now }}
}

func TestQueryCursorIsEncryptedAndBoundToRequest(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	codec := testQueryCursorCodec(t, now)
	ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY,
		interfaces.AccountInfo{ID: "user-1", Type: "user"})
	query := &interfaces.ObjectQueryBaseOnObjectType{
		KNID: "kn-1", Branch: interfaces.MAIN_BRANCH, ObjectTypeID: "customer",
		Properties:               []string{"mobile"},
		PageQuery:                interfaces.PageQuery{Limit: 10, Sort: []*interfaces.SortParams{{Field: "id", Direction: "asc"}}},
		EffectiveRowFilterDigest: "sha256:row-filter-a",
	}
	position := "raw-secret-position"
	token, err := codec.encodeResource(ctx, query, "model-v1", position, nil)
	if err != nil {
		t.Fatalf("encode() error = %v", err)
	}
	decodedToken, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || strings.Contains(string(decodedToken), "raw-secret-position") {
		t.Fatalf("cursor exposes plaintext position: %q", decodedToken)
	}
	got, err := codec.decodeResource(ctx, query, "model-v1", token)
	if err != nil || got != position {
		t.Fatalf("decode() = %#v, %v", got, err)
	}

	otherCaller := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY,
		interfaces.AccountInfo{ID: "user-2", Type: "user"})
	if _, err := codec.decodeResource(otherCaller, query, "model-v1", token); err == nil {
		t.Fatal("cursor must be bound to caller")
	}
	changedQuery := *query
	changedQuery.Properties = []string{"email"}
	if _, err := codec.decodeResource(ctx, &changedQuery, "model-v1", token); err == nil {
		t.Fatal("cursor must be bound to query digest")
	}
	if _, err := codec.decodeResource(ctx, query, "model-v2", token); err == nil {
		t.Fatal("cursor must be bound to model version")
	}
	changedFilter := *query
	changedFilter.EffectiveRowFilterDigest = "sha256:row-filter-b"
	if _, err := codec.decodeResource(ctx, &changedFilter, "model-v1", token); err == nil {
		t.Fatal("cursor must be bound to the effective row filter")
	}
	replacement := "A"
	if token[len(token)-1:] == replacement {
		replacement = "B"
	}
	tampered := token[:len(token)-1] + replacement
	if _, err := codec.decodeResource(ctx, query, "model-v1", tampered); err == nil {
		t.Fatal("tampered cursor must fail authentication")
	}
}

func TestQueryCursorExpires(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	codec := testQueryCursorCodec(t, now)
	ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY,
		interfaces.AccountInfo{ID: "user-1", Type: "user"})
	query := &interfaces.ObjectQueryBaseOnObjectType{KNID: "kn-1", Branch: "main", ObjectTypeID: "customer", EffectiveRowFilterDigest: "sha256:row-filter-a"}
	token, err := codec.encodeResource(ctx, query, "model-v1", "position", nil)
	if err != nil {
		t.Fatal(err)
	}
	codec.now = func() time.Time { return now.Add(queryCursorTTL) }
	if _, err := codec.decodeResource(ctx, query, "model-v1", token); err == nil {
		t.Fatal("expired cursor must fail closed")
	}
}

func TestResourceQueryCursorRespectsVegaExpiry(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	codec := testQueryCursorCodec(t, now)
	ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY,
		interfaces.AccountInfo{ID: "user-1", Type: "user"})
	query := &interfaces.ObjectQueryBaseOnObjectType{KNID: "kn-1", Branch: "main", ObjectTypeID: "orders", EffectiveRowFilterDigest: "sha256:row-filter-a"}
	vegaExpiry := now.Add(5 * time.Minute).Unix()
	token, err := codec.encodeResource(ctx, query, "model-v1", "vega-secret-cursor", &vegaExpiry)
	if err != nil {
		t.Fatalf("encodeResource() error = %v", err)
	}
	decodedToken, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || strings.Contains(string(decodedToken), "vega-secret-cursor") {
		t.Fatalf("cursor exposes plaintext Vega cursor: %q", decodedToken)
	}
	got, err := codec.decodeResource(ctx, query, "model-v1", token)
	if err != nil || got != "vega-secret-cursor" {
		t.Fatalf("decodeResource() = %q, %v", got, err)
	}
	codec.now = func() time.Time { return now.Add(5 * time.Minute) }
	if _, err := codec.decodeResource(ctx, query, "model-v1", token); err == nil {
		t.Fatal("resource cursor must expire when the Vega cursor expires")
	}
}

// Pin the pre-844 digest encoding across the public parameter rename.
func TestObjectQueryDigestLegacyCompatibility(t *testing.T) {
	legacyJSON := `{"branch":"main","need_total":true,"limit":10,"offset":0,"include_type_info":false,"include_logic_params":false,"ignoring_store":false}`
	legacyDefault := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(legacyJSON)))
	legacyBypass := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(strings.Replace(legacyJSON, `"ignoring_store":false`, `"ignoring_store":true`, 1))))
	query := &interfaces.ObjectQueryBaseOnObjectType{
		Branch: "main", PageQuery: interfaces.PageQuery{NeedTotal: true, Limit: 10},
	}
	defaultDigest, err := objectQueryDigest(query)
	if err != nil || defaultDigest != legacyDefault {
		t.Fatalf("default digest = %q, %v; legacy = %q", defaultDigest, err, legacyDefault)
	}
	query.IgnoreLocalIndex = true
	bypassDigest, err := objectQueryDigest(query)
	if err != nil || bypassDigest == defaultDigest || bypassDigest != legacyBypass {
		t.Fatalf("bypass digest = %q, %v; legacy = %q", bypassDigest, err, legacyBypass)
	}
}
