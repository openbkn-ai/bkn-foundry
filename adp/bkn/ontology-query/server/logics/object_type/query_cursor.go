// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package object_type

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"ontology-query/interfaces"
)

const (
	queryCursorVersion = "ontology-query-v1"
	queryCursorTTL     = 15 * time.Minute
)

type queryCursorCodec struct {
	aead cipher.AEAD
	now  func() time.Time
}

type queryCursorPayload struct {
	Version      string                      `json:"v"`
	CallerID     string                      `json:"caller_id"`
	CallerType   string                      `json:"caller_type"`
	KNID         string                      `json:"kn_id"`
	ObjectTypeID string                      `json:"object_type_id"`
	QueryDigest  string                      `json:"query_digest"`
	ModelVersion string                      `json:"model_version"`
	SearchAfter  interfaces.SearchAfterArray `json:"search_after"`
	ExpiresAt    time.Time                   `json:"expires_at"`
}

func newQueryCursorCodec() *queryCursorCodec {
	key := []byte(os.Getenv("BKN_QUERY_CURSOR_KEY"))
	if len(key) == 0 {
		key = make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, key); err != nil {
			panic("cannot initialize ontology-query cursor key")
		}
	}
	derived := sha256.Sum256(key)
	block, err := aes.NewCipher(derived[:])
	if err != nil {
		panic("cannot initialize ontology-query cursor cipher")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		panic("cannot initialize ontology-query cursor cipher")
	}
	return &queryCursorCodec{aead: aead, now: time.Now}
}

func (codec *queryCursorCodec) encode(ctx context.Context, query *interfaces.ObjectQueryBaseOnObjectType,
	modelVersion string, searchAfter []any) (string, error) {
	if len(searchAfter) == 0 {
		return "", nil
	}
	account, ok := queryCursorAccount(ctx)
	if !ok || codec == nil || codec.aead == nil {
		return "", fmt.Errorf("query cursor is unavailable")
	}
	digest, err := objectQueryDigest(query)
	if err != nil {
		return "", err
	}
	payload := queryCursorPayload{
		Version: queryCursorVersion, CallerID: account.ID, CallerType: account.Type,
		KNID: query.KNID, ObjectTypeID: query.ObjectTypeID,
		QueryDigest: digest, ModelVersion: modelVersion,
		SearchAfter: interfaces.SearchAfterArray(searchAfter),
		ExpiresAt:   codec.now().Add(queryCursorTTL).UTC(),
	}
	plaintext, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode query cursor payload: %w", err)
	}
	nonce := make([]byte, codec.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("create query cursor nonce: %w", err)
	}
	ciphertext := codec.aead.Seal(nil, nonce, plaintext, []byte(queryCursorVersion))
	token := append(nonce, ciphertext...)
	return base64.RawURLEncoding.EncodeToString(token), nil
}

func (codec *queryCursorCodec) decode(ctx context.Context, query *interfaces.ObjectQueryBaseOnObjectType,
	modelVersion, token string) (interfaces.SearchAfterArray, error) {
	account, ok := queryCursorAccount(ctx)
	if !ok || codec == nil || codec.aead == nil {
		return nil, fmt.Errorf("query cursor is unavailable")
	}
	tokenBytes, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || base64.RawURLEncoding.EncodeToString(tokenBytes) != token || len(tokenBytes) <= codec.aead.NonceSize() {
		return nil, fmt.Errorf("query cursor is invalid")
	}
	nonce := tokenBytes[:codec.aead.NonceSize()]
	plaintext, err := codec.aead.Open(nil, nonce, tokenBytes[codec.aead.NonceSize():], []byte(queryCursorVersion))
	if err != nil {
		return nil, fmt.Errorf("query cursor is invalid")
	}
	var payload queryCursorPayload
	if err := json.Unmarshal(plaintext, &payload); err != nil {
		return nil, fmt.Errorf("query cursor is invalid")
	}
	digest, err := objectQueryDigest(query)
	if err != nil {
		return nil, err
	}
	if payload.Version != queryCursorVersion || payload.CallerID != account.ID || payload.CallerType != account.Type ||
		payload.KNID != query.KNID || payload.ObjectTypeID != query.ObjectTypeID || payload.QueryDigest != digest ||
		payload.ModelVersion != modelVersion || !codec.now().Before(payload.ExpiresAt) || len(payload.SearchAfter) == 0 {
		return nil, fmt.Errorf("query cursor is invalid")
	}
	return payload.SearchAfter, nil
}

func objectQueryDigest(query *interfaces.ObjectQueryBaseOnObjectType) (string, error) {
	if query == nil {
		return "", fmt.Errorf("query is missing")
	}
	type digestInput struct {
		Branch                  string                      `json:"branch"`
		Condition               map[string]any              `json:"condition,omitempty"`
		Properties              []string                    `json:"properties,omitempty"`
		ObjectQueryInfo         *interfaces.ObjectQueryInfo `json:"object_query_info,omitempty"`
		NeedTotal               bool                        `json:"need_total"`
		Limit                   int                         `json:"limit"`
		Offset                  int                         `json:"offset"`
		Sort                    []*interfaces.SortParams    `json:"sort,omitempty"`
		IncludeTypeInfo         bool                        `json:"include_type_info"`
		IncludeLogicParams      bool                        `json:"include_logic_params"`
		IgnoringStore           bool                        `json:"ignoring_store"`
		ExcludeSystemProperties []string                    `json:"exclude_system_properties,omitempty"`
	}
	body, err := json.Marshal(digestInput{
		Branch: query.Branch, Condition: query.Condition, Properties: query.Properties,
		ObjectQueryInfo: query.ObjectQueryInfo,
		NeedTotal:       query.NeedTotal, Limit: query.Limit, Offset: query.Offset, Sort: query.Sort,
		IncludeTypeInfo: query.IncludeTypeInfo, IncludeLogicParams: query.IncludeLogicParams,
		IgnoringStore: query.IgnoringStore, ExcludeSystemProperties: query.ExcludeSystemProperties,
	})
	if err != nil {
		return "", fmt.Errorf("encode query digest: %w", err)
	}
	digest := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func queryCursorAccount(ctx context.Context) (interfaces.AccountInfo, bool) {
	if ctx == nil {
		return interfaces.AccountInfo{}, false
	}
	account, ok := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	return account, ok && account.ID != "" && account.Type != ""
}
