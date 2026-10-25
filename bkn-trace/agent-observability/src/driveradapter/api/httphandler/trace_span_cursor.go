// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package httphandler

import (
	"cmp"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
)

const traceSpanPageSize = 100
const traceSpanSort = "span_id_asc"
const maxTraceSpanCursorBytes = 2048

var errTraceSpanCursor = errors.New("trace span cursor is invalid or expired")

type traceSpanCursor struct {
	Version  int    `json:"version"`
	TraceID  string `json:"trace_id"`
	After    string `json:"after"`
	Scope    string `json:"scope"`
	Sort     string `json:"sort"`
	PageSize int    `json:"page_size"`
	Expires  int64  `json:"expires"`
}

var defaultTraceSpanCursorKey = randomTraceSpanCursorKey()

func newTraceSpanCursorKey(keys ...[]byte) []byte {
	if len(keys) > 0 && len(keys[0]) > 0 {
		return append([]byte(nil), keys[0]...)
	}
	return append([]byte(nil), defaultTraceSpanCursorKey...)
}

func randomTraceSpanCursorKey() []byte {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic("cannot initialize trace span cursor signing key")
	}
	return key
}

func traceSpanScopeBinding(scope evidencevo.QueryScope) string {
	// Bind actual current grants, not display metadata or an order-sensitive
	// upstream fingerprint. Work on copies: trusted profiles may be shared.
	var profile *evidencevo.AccessProfile
	if scope.AccessProfile != nil {
		canonical := *scope.AccessProfile
		canonical.ActorNameSnapshot = ""
		canonical.Fingerprint = ""
		canonical.Roles = sortedSpanScopeValues(canonical.Roles)
		canonical.ManagedKnowledgeNetworkIDs = sortedSpanScopeValues(canonical.ManagedKnowledgeNetworkIDs)
		canonical.Permissions = append([]evidencevo.Permission(nil), canonical.Permissions...)
		for i := range canonical.Permissions {
			canonical.Permissions[i].Operations = sortedSpanScopeValues(canonical.Permissions[i].Operations)
		}
		compare := func(a, b evidencevo.Permission) int {
			if c := cmp.Compare(a.ResourceType, b.ResourceType); c != 0 {
				return c
			}
			if c := cmp.Compare(a.ResourceID, b.ResourceID); c != 0 {
				return c
			}
			return slices.Compare(a.Operations, b.Operations)
		}
		slices.SortFunc(canonical.Permissions, compare)
		canonical.Permissions = slices.CompactFunc(canonical.Permissions, func(a, b evidencevo.Permission) bool { return compare(a, b) == 0 })
		profile = &canonical
	}
	raw, _ := json.Marshal(struct {
		AccountID   string
		AccountType string
		View        evidencevo.AccessView
		Profile     *evidencevo.AccessProfile
	}{scope.AccountID, scope.AccountType, evidencevo.AccessViewTechnical, profile})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func sortedSpanScopeValues(values []string) []string {
	result := append([]string(nil), values...)
	slices.Sort(result)
	return slices.Compact(result)
}

func encodeTraceSpanCursor(key []byte, traceID, after string, scope evidencevo.QueryScope, now time.Time) (string, error) {
	if !validSpanAfterKey(after) {
		return "", errTraceSpanCursor
	}
	raw, err := json.Marshal(traceSpanCursor{Version: 1, TraceID: traceID, After: after, Scope: traceSpanScopeBinding(scope), Sort: traceSpanSort, PageSize: traceSpanPageSize, Expires: now.Add(15 * time.Minute).Unix()})
	if err != nil {
		return "", errTraceSpanCursor
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(raw)
	cursor := base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if len(cursor) > maxTraceSpanCursorBytes {
		return "", errTraceSpanCursor
	}
	return cursor, nil
}

func decodeTraceSpanCursor(key []byte, value, traceID string, scope evidencevo.QueryScope, now time.Time) (string, error) {
	if value == "" {
		return "", nil
	}
	if len(value) > maxTraceSpanCursorBytes {
		return "", errTraceSpanCursor
	}
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return "", errTraceSpanCursor
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", errTraceSpanCursor
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", errTraceSpanCursor
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(raw)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return "", errTraceSpanCursor
	}
	var cursor traceSpanCursor
	if json.Unmarshal(raw, &cursor) != nil || cursor.Version != 1 || cursor.TraceID != traceID || cursor.Scope != traceSpanScopeBinding(scope) || cursor.Sort != traceSpanSort || cursor.PageSize != traceSpanPageSize || cursor.Expires <= now.Unix() || cursor.Expires > now.Add(15*time.Minute).Unix() || !validSpanAfterKey(cursor.After) {
		return "", errTraceSpanCursor
	}
	return cursor.After, nil
}

func validSpanAfterKey(value string) bool {
	return value != "" && len(value) <= 128 && utf8.ValidString(value) && !strings.ContainsAny(value, "\x00\r\n")
}
