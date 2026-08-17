package sandbox

import "testing"

// 会话级 env 被控制面存进 t_session.f_env_vars、写进 Pod spec，并由
// GET /api/v1/sessions/{id} 原样读出，生命周期是会话寿命（默认最长 6 小时）。
// 凭据落在那里，等于把一次调用的令牌留成了半天的明文——而发起它的那次执行早已结束。
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
	// 追踪标记仍要留下：会话查询靠它们看出这个槽在替谁干活。
	for _, key := range []string{"source", "task_id", "user_id"} {
		if scoped[key] == nil {
			t.Fatalf("%s 不该被一起滤掉: %v", key, scoped)
		}
	}
}

// 全是凭据时返回 nil 而不是空 map：空 map 会让控制面存下一个 "{}"，
// 平白多出一条看不出用途的记录。
func TestSessionScopedEnvVarsReturnsNilWhenOnlyCredentials(t *testing.T) {
	if got := sessionScopedEnvVars(map[string]any{"BKN_TOKEN": "x"}); got != nil {
		t.Fatalf("应返回 nil，得到 %v", got)
	}
	if got := sessionScopedEnvVars(nil); got != nil {
		t.Fatalf("空入参应返回 nil，得到 %v", got)
	}
}
