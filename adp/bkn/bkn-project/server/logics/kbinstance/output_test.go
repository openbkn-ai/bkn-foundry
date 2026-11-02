// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestUnsignedValuesUseFullDeclaredRange(t *testing.T) {
	for _, test := range []struct {
		number string
		valid  bool
	}{{"18446744073709551615", true}, {"18446744073709551616", false}, {"-1", false}, {"1.5", false}} {
		if validInstanceValue("unsigned integer", json.Number(test.number)) != test.valid {
			t.Errorf("unsigned value %s has wrong range validation", test.number)
		}
	}
}
func TestCitationOrderDoesNotChangeMaterializedRowIdentity(t *testing.T) {
	_, _, input, _ := extractionFixture(t, "patent", true)
	citations := []InstanceCitation{{BlockID: "native-1", Quote: "ABC"}, {BlockID: "native-1", Quote: "25.50"}}
	first, err := locateInstances(input, citations, true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := locateInstances(input, []InstanceCitation{citations[1], citations[0]}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("citation ordering changes stable source row identity")
	}
}
