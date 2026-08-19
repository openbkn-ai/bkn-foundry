package sandbox

import "testing"

// The session-level env is stored in t_session.f_env_vars by the control plane, written into the Pod spec, and passed by.
// GET /api/v1/sessions/{id} is read as is, and the lifetime is the session lifetime (default is up to 6 hours).
// By leaving the credentials there, you're leaving a call's token in clear text for half a day - long after the execution that initiated it ended.
func TestSessionScopedEnvVarsDropsCredentials(t *testing.T) {
	scoped := sessionScopedEnvVars(map[string]any{
		"source":              "function_debug",
		"task_id":             "t1",
		"user_id":             "u1",
		"BKN_TOKEN":           "bak_secret",
		"BKN_CONVERSATION_ID": "conv_1",
		"BKN_INTERACTION_ID":  "int_1",
	})

	for _, key := range []string{"BKN_TOKEN", "BKN_CONVERSATION_ID", "BKN_INTERACTION_ID"} {
		if _, leaked := scoped[key]; leaked {
			t.Fatalf("%s 不该进入会话级 env: %v", key, scoped)
		}
	}
	// Trace flags are still left: session queries rely on them to see who the slot is working for.
	for _, key := range []string{"source", "task_id", "user_id"} {
		if scoped[key] == nil {
			t.Fatalf("%s 不该被一起滤掉: %v", key, scoped)
		}
	}
}

// The whitelist is blocked by default: keys that have not been registered will not be entered into the session-level env. The blacklist is allowed by default.
// If you add a new credential to the execution env and forget to synchronize it, the database will be silently dropped.
func TestSessionScopedEnvVarsDeniesUnknownKeys(t *testing.T) {
	scoped := sessionScopedEnvVars(map[string]any{
		"source":                "function_debug",
		"SOME_FUTURE_SECRET":    "s3cret",
		"another_unknown_field": "x",
	})
	if len(scoped) != 1 || scoped["source"] != "function_debug" {
		t.Fatalf("未登记的键必须拦下，得到 %v", scoped)
	}
}

// Return nil instead of an empty map when all credentials are used: an empty map will cause the control surface to store a "{}",
// There is just one more record with no apparent purpose.
func TestSessionScopedEnvVarsReturnsNilWhenOnlyCredentials(t *testing.T) {
	if got := sessionScopedEnvVars(map[string]any{"BKN_TOKEN": "x"}); got != nil {
		t.Fatalf("应返回 nil，得到 %v", got)
	}
	if got := sessionScopedEnvVars(nil); got != nil {
		t.Fatalf("空入参应返回 nil，得到 %v", got)
	}
}
