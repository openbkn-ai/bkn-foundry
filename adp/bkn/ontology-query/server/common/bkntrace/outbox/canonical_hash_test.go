package outbox

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/bytedance/sonic"
)

func TestCanonicalHashFixtures(t *testing.T) {
	fixtures := []struct {
		payload string
		want    string
	}{
		{` { "b" : 2, "a" : 1 } `, "43258cff783fe7036d8a43033f830adfc60ec037382473548ac742b888292777"},
		{`{"items":[3,2,1],"nested":{"z":2,"a":1}}`, "7f1b2afcbb4cec480f8e65ea7fb85338ce2571b5b10251c83f24bb03318d86a4"},
		{`{"number":1.5,"message":"\u4f60\u597d"}`, "b20ba0633e4f6cfc766715ff6e8d996f19602c268eac402951be732f26a9956a"},
	}
	for _, fixture := range fixtures {
		if got := CanonicalHash([]byte(fixture.payload)); got != fixture.want {
			t.Fatalf("CanonicalHash(%s) = %s, want %s", fixture.payload, got, fixture.want)
		}
	}
}

func TestCanonicalHashMatchesEncodingJSON(t *testing.T) {
	payloads := []string{
		`{"message":"a & b < c > d"}`,
		`{"number":1.0,"scientific":1e3}`,
	}
	for _, payload := range payloads {
		var decoded any
		if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
			t.Fatalf("json.Unmarshal(%s): %v", payload, err)
		}
		canonical, err := json.Marshal(decoded)
		if err != nil {
			t.Fatalf("json.Marshal(%s): %v", payload, err)
		}
		sonicCanonical, err := sonic.ConfigStd.Marshal(decoded)
		if err != nil {
			t.Fatalf("sonic.ConfigStd.Marshal(%s): %v", payload, err)
		}
		if string(sonicCanonical) != string(canonical) {
			t.Fatalf("sonic.ConfigStd.Marshal(%s) = %s, want %s", payload, sonicCanonical, canonical)
		}
		sum := sha256.Sum256(canonical)
		want := hex.EncodeToString(sum[:])
		if got := CanonicalHash([]byte(payload)); got != want {
			t.Fatalf("CanonicalHash(%s) = %s, want %s", payload, got, want)
		}
	}
}
