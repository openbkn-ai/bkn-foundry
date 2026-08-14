package opensearchtraceaccess

import "testing"

func TestTraceIDsUsesFrozenCallFactsAndPreservesExistingIDs(t *testing.T) {
	ids, err := traceIDs([]byte(`{"call_facts":[{"trace_id":"trace-b"},{"trace_id":"trace-a"},{"trace_id":""}],"technical_trace_ids":["trace-a","trace-c"]}`))
	if err != nil {
		t.Fatalf("traceIDs returned error: %v", err)
	}
	want := []string{"trace-a", "trace-b", "trace-c"}
	if len(ids) != len(want) {
		t.Fatalf("ids = %#v, want %#v", ids, want)
	}
	for index := range want {
		if ids[index] != want[index] {
			t.Fatalf("ids = %#v, want %#v", ids, want)
		}
	}
}
