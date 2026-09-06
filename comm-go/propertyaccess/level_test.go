// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package propertyaccess

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestLevelOrdering(t *testing.T) {
	levels := []Level{None, Schema, Masked, Full}
	for leftIndex, left := range levels {
		for rightIndex, right := range levels {
			comparison, err := Compare(left, right)
			if err != nil {
				t.Fatalf("Compare(%q, %q): %v", left, right, err)
			}
			want := 0
			if leftIndex < rightIndex {
				want = -1
			} else if leftIndex > rightIndex {
				want = 1
			}
			if comparison != want {
				t.Errorf("Compare(%q, %q) = %d, want %d", left, right, comparison, want)
			}
		}
	}
}

func TestMinAndMax(t *testing.T) {
	minimum, err := Min(Masked, Schema)
	if err != nil || minimum != Schema {
		t.Fatalf("Min(masked, schema) = %q, %v", minimum, err)
	}
	maximum, err := Max(Masked, Full)
	if err != nil || maximum != Full {
		t.Fatalf("Max(masked, full) = %q, %v", maximum, err)
	}
}

func TestUnknownLevelFailsClosed(t *testing.T) {
	if _, err := Parse("inherit"); !errors.Is(err, ErrInvalidLevel) {
		t.Fatalf("Parse(inherit) error = %v, want ErrInvalidLevel", err)
	}
	if _, err := Min(Level("unknown"), Full); !errors.Is(err, ErrInvalidLevel) {
		t.Fatalf("Min(unknown, full) error = %v, want ErrInvalidLevel", err)
	}
	if _, err := json.Marshal(Level("unknown")); !errors.Is(err, ErrInvalidLevel) {
		t.Fatalf("MarshalJSON(unknown) error = %v, want ErrInvalidLevel", err)
	}

	var decoded Level
	if err := json.Unmarshal([]byte(`"unknown"`), &decoded); !errors.Is(err, ErrInvalidLevel) {
		t.Fatalf("UnmarshalJSON(unknown) error = %v, want ErrInvalidLevel", err)
	}
}

func TestLevelJSONRoundTrip(t *testing.T) {
	for _, want := range []Level{None, Schema, Masked, Full} {
		data, err := json.Marshal(want)
		if err != nil {
			t.Fatalf("MarshalJSON(%q): %v", want, err)
		}
		var got Level
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("UnmarshalJSON(%q): %v", data, err)
		}
		if got != want {
			t.Errorf("round trip = %q, want %q", got, want)
		}
	}
}
