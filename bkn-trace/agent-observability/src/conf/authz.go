package conf

import (
	"os"
	"strings"
)

// TraceReadAuthzConfig gates the trace READ endpoints (_search, by-conversation).
// The evidence WRITE endpoint keeps its own ingest-token guard; this only
// concerns who may read traces.
//
// Authorization is enforced by default. Enforce=false is an explicit local
// development override, not a production rollout mode:
//
//   - Enforce=false: a request without a resolvable account identity
//     is allowed but logged; a normal caller's query is NOT actually scoped,
//     only logged as "would be scoped". This surfaces the access pattern and
//     can be used for local gateway integration diagnostics.
//   - Enforce=true (default): a request without identity
//     is rejected 401; a normal caller's query is scoped to their own account.
type TraceReadAuthzConfig struct {
	Enforce bool
}

// NewTraceReadAuthzConfig reads the config from the environment.
//
//	TRACE_READ_AUTHZ_ENFORCE=true|false   (default true)
func NewTraceReadAuthzConfig() TraceReadAuthzConfig {
	return TraceReadAuthzConfig{
		Enforce: !strings.EqualFold(strings.TrimSpace(os.Getenv("TRACE_READ_AUTHZ_ENFORCE")), "false"),
	}
}
