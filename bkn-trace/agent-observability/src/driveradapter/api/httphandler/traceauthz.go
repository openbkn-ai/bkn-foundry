package httphandler

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/conf"
)

// accountAttrField is the indexed OpenSearch field carrying the account that
// produced a span. Account identity is propagated across services as W3C
// baggage (bkn.account.id) by the phase-one trace-context baseline and lands on
// the span as an attribute; the by-conversation query proves the indexing
// convention is attributes.<key>.keyword.
const accountAttrField = "attributes.bkn.account.id.keyword"

const headerBaggage = "baggage"

// identity is the caller resolved from gateway-propagated baggage. Trace
// services do not verify tokens themselves — the gateway authenticates and
// forwards bkn.account.id / bkn.account.type, the same trusted-header model the
// rest of the platform uses.
type identity struct {
	accountID   string
	accountType string
	present     bool
}

// identityFromRequest pulls the account out of the baggage header
// ("bkn.account.type=user,bkn.account.id=u-1,..."). present is false when no
// account id is carried.
func identityFromRequest(r *http.Request) identity {
	bag := parseBaggage(r.Header.Get(headerBaggage))
	id := identity{
		accountID:   bag["bkn.account.id"],
		accountType: bag["bkn.account.type"],
	}
	id.present = id.accountID != ""
	return id
}

// parseBaggage parses a W3C baggage header into a map. Values are used as-is;
// this service only reads two well-known keys.
func parseBaggage(header string) map[string]string {
	out := map[string]string{}
	for _, pair := range strings.Split(header, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out
}

// traceReadAuthz applies the read-authorization decision to a trace search.
type traceReadAuthz struct {
	cfg conf.TraceReadAuthzConfig
}

func newTraceReadAuthz(cfg conf.TraceReadAuthzConfig) traceReadAuthz {
	return traceReadAuthz{cfg: cfg}
}

func (a traceReadAuthz) isAdmin(accountType string) bool {
	return a.cfg.AdminTypes[accountType]
}

// authorize decides what query actually runs for a trace read. It returns the
// (possibly account-scoped) query body, or an HTTP status+message when the
// request must be refused.
//
// The staged behaviour lives here:
//
//   - no identity + enforce  -> 401
//   - no identity + shadow   -> log, run the caller's query unchanged
//   - admin account          -> run unchanged (cross-account by design)
//   - normal + shadow        -> log "would scope", run unchanged
//   - normal + enforce       -> inject a term filter on the caller's account
func (a traceReadAuthz) authorize(id identity, body json.RawMessage) (effective json.RawMessage, status int, message string) {
	if !id.present {
		if a.cfg.Enforce {
			return nil, http.StatusUnauthorized, "trace read requires an authenticated account"
		}
		slog.Warn("trace read without account identity", "mode", "shadow", "action", "allowed_unscoped")
		return body, 0, ""
	}
	if a.isAdmin(id.accountType) {
		return body, 0, ""
	}
	if !a.cfg.Enforce {
		slog.Info("trace read would be account-scoped", "mode", "shadow", "account_id", id.accountID, "action", "allowed_unscoped")
		return body, 0, ""
	}
	scoped, err := scopeQueryToAccount(body, id.accountID)
	if err != nil {
		return nil, http.StatusBadRequest, fmt.Sprintf("cannot scope query: %v", err)
	}
	return scoped, 0, ""
}

// scopeQueryToAccount rewrites an OpenSearch search body so it can only match
// the given account's spans. The caller's original query becomes a must clause
// under a bool, and a term filter on the account attribute is added alongside
// it; size, sort, aggregations and every other top-level key are preserved.
//
// A body with no query is treated as match_all before scoping — an unscoped
// "give me everything" collapses to "everything of mine", never everything.
func scopeQueryToAccount(body json.RawMessage, accountID string) (json.RawMessage, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("search body must be a JSON object: %w", err)
	}
	if root == nil {
		root = map[string]json.RawMessage{}
	}

	inner := root["query"]
	if len(inner) == 0 {
		inner = json.RawMessage(`{"match_all":{}}`)
	}

	scoped := map[string]any{
		"bool": map[string]any{
			"must": []json.RawMessage{inner},
			"filter": []map[string]any{
				{"term": map[string]string{accountAttrField: accountID}},
			},
		},
	}
	scopedRaw, err := json.Marshal(scoped)
	if err != nil {
		return nil, err
	}
	root["query"] = scopedRaw
	return json.Marshal(root)
}
