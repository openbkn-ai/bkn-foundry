// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package object_type

import (
	"context"
	"encoding/json"
	"sync"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

const (
	maxObjectMetricMemoQueries = 32
	maxObjectMetricMemoRows    = 128
)

type objectMetricMemoContextKey struct{}

type objectMetricRequestMemo struct {
	mu      sync.Mutex
	queries map[objectMetricMemoKey]*objectMetricMemoCall
}

type objectMetricMemoKey struct {
	knID, branch, metricID string
	version                int
	queryJSON              string
}

type objectMetricMemoCall struct {
	done   chan struct{}
	result interfaces.ObjectMetricDataV1
	err    error
}

func withObjectMetricRequestMemo(ctx context.Context) context.Context {
	return context.WithValue(ctx, objectMetricMemoContextKey{}, &objectMetricRequestMemo{
		queries: make(map[objectMetricMemoKey]*objectMetricMemoCall),
	})
}

// Deduplicate exact object-set queries only. Instance metrics must keep their
// per-object identity; a cache never outlives the enclosing object request.
// Failed or large results are shared with concurrent waiters but not retained.
func queryObjectMetricWithRequestMemo(ctx context.Context, service interfaces.ObjectMetricQueryServiceV1,
	knID, branch, metricID string, version int, query *interfaces.ObjectMetricQueryRequestV1,
) (interfaces.ObjectMetricDataV1, error) {
	memo, ok := ctx.Value(objectMetricMemoContextKey{}).(*objectMetricRequestMemo)
	if !ok || !objectMetricMemoSafeQuery(query) {
		return service.QueryObjectMetricDataV1(ctx, knID, branch, metricID, version, query)
	}
	encoded, err := json.Marshal(query)
	if err != nil {
		return service.QueryObjectMetricDataV1(ctx, knID, branch, metricID, version, query)
	}
	key := objectMetricMemoKey{knID: knID, branch: branch, metricID: metricID,
		version: version, queryJSON: string(encoded)}
	memo.mu.Lock()
	if call, exists := memo.queries[key]; exists {
		memo.mu.Unlock()
		select {
		case <-call.done:
			if err := ctx.Err(); err != nil {
				return interfaces.ObjectMetricDataV1{}, err
			}
			return call.result, call.err
		case <-ctx.Done():
			return interfaces.ObjectMetricDataV1{}, ctx.Err()
		}
	}
	if len(memo.queries) >= maxObjectMetricMemoQueries {
		memo.mu.Unlock()
		return service.QueryObjectMetricDataV1(ctx, knID, branch, metricID, version, query)
	}
	call := &objectMetricMemoCall{done: make(chan struct{})}
	memo.queries[key] = call
	memo.mu.Unlock()

	result, err := service.QueryObjectMetricDataV1(ctx, knID, branch, metricID, version, query)
	memo.mu.Lock()
	call.result, call.err = result, err
	if err != nil || len(result.Entries) > maxObjectMetricMemoRows {
		delete(memo.queries, key)
	}
	close(call.done)
	memo.mu.Unlock()
	return result, err
}

// Cache only requests with fields whose JSON representation is unambiguous.
// Arbitrary filter literals may contain custom values or invalid UTF-8; the
// metric service still evaluates them, but each such call remains independent.
func objectMetricMemoSafeQuery(query *interfaces.ObjectMetricQueryRequestV1) bool {
	if query == nil || len(query.InstanceIdentities) != 0 || query.Filter != nil ||
		query.Having != nil || len(query.OrderBy) != 0 || query.Cursor != "" ||
		query.BucketSelection != nil || query.RuntimeAnchor != nil ||
		query.WindowOverride != nil || len(query.SelectedBuckets) != 0 {
		return false
	}
	for _, ref := range query.GroupBy {
		if !utf8.ValidString(ref.ObjectTypeID) || !utf8.ValidString(ref.RelationPathAlias) ||
			!utf8.ValidString(ref.PropertyName) {
			return false
		}
	}
	return true
}
