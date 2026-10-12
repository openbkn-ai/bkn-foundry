// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionvo

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestEvidenceSnapshotCarriesInternalIntegrityVersion(t *testing.T) {
	interaction := Interaction{ID: "int"}
	field := reflect.ValueOf(&interaction).Elem().FieldByName("IntegritySourceVersion")
	if !field.IsValid() {
		t.Fatal("interaction lacks independent integrity source version")
	}
	field.SetUint(9)
	snapshot, err := CopyEvidenceSnapshot(interaction, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	version := reflect.ValueOf(snapshot.Interaction).FieldByName("IntegritySourceVersion")
	if !version.IsValid() || version.Uint() != 9 {
		t.Fatal("consistent snapshot discarded integrity source version")
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "integrity") {
		t.Fatalf("internal metadata leaked onto wire: %s", raw)
	}
}
