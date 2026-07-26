package conf

import (
	"os"
	"strings"
)

// TraceReadAuthzConfig gates the trace READ endpoints (_search, by-conversation).
// The evidence WRITE endpoint keeps its own ingest-token guard; this only
// concerns who may read traces.
//
// Rollout is staged, per the compatibility rule (default permissive -> shadow
// log -> flip to enforce):
//
//   - Enforce=false (default): a request without a resolvable account identity
//     is allowed but logged; a normal caller's query is NOT actually scoped,
//     only logged as "would be scoped". This surfaces the access pattern and
//     confirms the gateway injects account baggage before anything is blocked.
//   - Enforce=true (TRACE_READ_AUTHZ_ENFORCE=true): a request without identity
//     is rejected 401; a normal caller's query is scoped to their own account.
//
// Admin-class account types (super_admin/admin/audit by default) always see
// every account's traces — diagnose and audit are cross-account by nature.
type TraceReadAuthzConfig struct {
	Enforce    bool
	AdminTypes map[string]bool
}

// NewTraceReadAuthzConfig reads the config from the environment.
//
//	TRACE_READ_AUTHZ_ENFORCE=true|false   (default false — shadow mode)
//	TRACE_READ_AUTHZ_ADMIN_TYPES=a,b,c    (default super_admin,admin,audit)
func NewTraceReadAuthzConfig() TraceReadAuthzConfig {
	admin := map[string]bool{}
	raw := strings.TrimSpace(os.Getenv("TRACE_READ_AUTHZ_ADMIN_TYPES"))
	if raw == "" {
		raw = "super_admin,admin,audit"
	}
	for _, t := range strings.Split(raw, ",") {
		if t = strings.TrimSpace(t); t != "" {
			admin[t] = true
		}
	}
	return TraceReadAuthzConfig{
		Enforce:    os.Getenv("TRACE_READ_AUTHZ_ENFORCE") == "true",
		AdminTypes: admin,
	}
}
