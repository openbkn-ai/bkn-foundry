package ledgervo

import "testing"

func TestCanonicalPayloadHashFixtures(t *testing.T) {
	fixtures := []struct {
		payload string
		want    string
	}{
		{` { "b" : 2, "a" : 1 } `, "43258cff783fe7036d8a43033f830adfc60ec037382473548ac742b888292777"},
		{`{"items":[3,2,1],"nested":{"z":2,"a":1}}`, "7f1b2afcbb4cec480f8e65ea7fb85338ce2571b5b10251c83f24bb03318d86a4"},
		{`{"number":1.5,"message":"\u4f60\u597d"}`, "b20ba0633e4f6cfc766715ff6e8d996f19602c268eac402951be732f26a9956a"},
	}
	for _, fixture := range fixtures {
		if got := CanonicalPayloadHash([]byte(fixture.payload)); got != fixture.want {
			t.Fatalf("CanonicalPayloadHash(%s) = %s, want %s", fixture.payload, got, fixture.want)
		}
	}
}
