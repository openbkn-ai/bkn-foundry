// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package object_type

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"ontology-query/interfaces"
)

func testQueryCursorCodec(t *testing.T, now time.Time) *queryCursorCodec {
	t.Helper()
	block, err := aes.NewCipher([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	return &queryCursorCodec{aead: aead, now: func() time.Time { return now }}
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
	position := []any{"raw-secret-position", 42}
	token, err := codec.encode(ctx, query, "model-v1", position)
	if err != nil {
		t.Fatalf("encode() error = %v", err)
	}
	decodedToken, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || strings.Contains(string(decodedToken), "raw-secret-position") {
		t.Fatalf("cursor exposes plaintext position: %q", decodedToken)
	}
	got, err := codec.decode(ctx, query, "model-v1", token)
	if err != nil || len(got) != 2 || got[0] != "raw-secret-position" {
		t.Fatalf("decode() = %#v, %v", got, err)
	}

	otherCaller := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY,
		interfaces.AccountInfo{ID: "user-2", Type: "user"})
	if _, err := codec.decode(otherCaller, query, "model-v1", token); err == nil {
		t.Fatal("cursor must be bound to caller")
	}
	changedQuery := *query
	changedQuery.Properties = []string{"email"}
	if _, err := codec.decode(ctx, &changedQuery, "model-v1", token); err == nil {
		t.Fatal("cursor must be bound to query digest")
	}
	if _, err := codec.decode(ctx, query, "model-v2", token); err == nil {
		t.Fatal("cursor must be bound to model version")
	}
	changedFilter := *query
	changedFilter.EffectiveRowFilterDigest = "sha256:row-filter-b"
	if _, err := codec.decode(ctx, &changedFilter, "model-v1", token); err == nil {
		t.Fatal("cursor must be bound to the effective row filter")
	}
	replacement := "A"
	if token[len(token)-1:] == replacement {
		replacement = "B"
	}
	tampered := token[:len(token)-1] + replacement
	if _, err := codec.decode(ctx, query, "model-v1", tampered); err == nil {
		t.Fatal("tampered cursor must fail authentication")
	}
}

func TestQueryCursorExpires(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	codec := testQueryCursorCodec(t, now)
	ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY,
		interfaces.AccountInfo{ID: "user-1", Type: "user"})
	query := &interfaces.ObjectQueryBaseOnObjectType{KNID: "kn-1", Branch: "main", ObjectTypeID: "customer", EffectiveRowFilterDigest: "sha256:row-filter-a"}
	token, err := codec.encode(ctx, query, "model-v1", []any{"position"})
	if err != nil {
		t.Fatal(err)
	}
	codec.now = func() time.Time { return now.Add(queryCursorTTL) }
	if _, err := codec.decode(ctx, query, "model-v1", token); err == nil {
		t.Fatal("expired cursor must fail closed")
	}
}

func TestResourceQueryCursorIsEncryptedAndCannotBeUsedAsSearchAfter(t *testing.T) {
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
	got, offset, err := codec.decodeResource(ctx, query, "model-v1", token)
	if err != nil || got != "vega-secret-cursor" || offset != 0 {
		t.Fatalf("decodeResource() = %q, %d, %v", got, offset, err)
	}
	if _, err := codec.decode(ctx, query, "model-v1", token); err == nil {
		t.Fatal("resource cursor must not be accepted as a search-after cursor")
	}
	codec.now = func() time.Time { return now.Add(5 * time.Minute) }
	if _, _, err := codec.decodeResource(ctx, query, "model-v1", token); err == nil {
		t.Fatal("resource cursor must expire when the Vega cursor expires")
	}
}

func TestResourceOffsetCursorIsBoundToRequestAndExpires(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	codec := testQueryCursorCodec(t, now)
	ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY,
		interfaces.AccountInfo{ID: "user-1", Type: "user"})
	query := &interfaces.ObjectQueryBaseOnObjectType{KNID: "kn-1", Branch: "main", ObjectTypeID: "orders", EffectiveRowFilterDigest: "sha256:row-filter-a"}
	if token, err := codec.encodeResourceOffset(ctx, query, "model-v1", 0); err != nil || token != "" {
		t.Fatalf("a zero offset must not produce a cursor: %q, %v", token, err)
	}
	token, err := codec.encodeResourceOffset(ctx, query, "model-v1", 20)
	if err != nil || token == "" {
		t.Fatalf("encodeResourceOffset() = %q, %v", token, err)
	}
	resourceCursor, offset, err := codec.decodeResource(ctx, query, "model-v1", token)
	if err != nil || resourceCursor != "" || offset != 20 {
		t.Fatalf("decodeResource() = %q, %d, %v", resourceCursor, offset, err)
	}
	if _, err := codec.decode(ctx, query, "model-v1", token); err == nil {
		t.Fatal("offset cursor must not be accepted as a search-after cursor")
	}
	otherQuery := *query
	otherQuery.ObjectTypeID = "customers"
	if _, _, err := codec.decodeResource(ctx, &otherQuery, "model-v1", token); err == nil {
		t.Fatal("offset cursor must be bound to the original query")
	}
	codec.now = func() time.Time { return now.Add(queryCursorTTL) }
	if _, _, err := codec.decodeResource(ctx, query, "model-v1", token); err == nil {
		t.Fatal("offset cursor must expire after the query cursor TTL")
	}
}
