package bkntrace

import "testing"

func TestExtractTraceGapMarkersRemovesOnlyValidWarnings(t *testing.T) {
	reason := "trace_call_unrecorded:run_cypher:req_92343e69-c464-49e9-86b8-a3cc2aba92d9"
	input := "business stderr\n[BKN_TRACE_GAP]{\"partial_reason\":\"" + reason + "\"}\ninvalid [BKN_TRACE_GAP]{bad}\n"

	cleaned, reasons := ExtractTraceGapMarkers(input)

	if cleaned != "business stderrinvalid [BKN_TRACE_GAP]{bad}\n" {
		t.Fatalf("cleaned stderr = %q", cleaned)
	}
	if len(reasons) != 1 || reasons[0] != reason {
		t.Fatalf("reasons = %#v", reasons)
	}
}
