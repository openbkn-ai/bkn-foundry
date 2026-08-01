package conf

import "testing"

func TestTraceReadAuthzDefaultsFailClosedWithoutAdminBypass(t *testing.T) {
	t.Setenv("TRACE_READ_AUTHZ_ENFORCE", "")
	t.Setenv("TRACE_READ_AUTHZ_ADMIN_TYPES", "super_admin,admin,audit")
	config := NewTraceReadAuthzConfig()
	if !config.Enforce {
		t.Fatal("trace reads must default to enforced identity scoping")
	}
}
