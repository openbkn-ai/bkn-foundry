package logic

import "testing"

func TestHttpCapabilityIDRoundTrip(t *testing.T) {
	id := BuildHttpCapabilityID("box-1", "tool-2")
	boxID, toolID, ok := ParseHttpCapabilityID(id)
	if !ok || boxID != "box-1" || toolID != "tool-2" {
		t.Fatalf("unexpected parse result: ok=%v box=%q tool=%q", ok, boxID, toolID)
	}
}
