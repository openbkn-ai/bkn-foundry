// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package connector

import (
	"context"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/licverify"

	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/interfaces"
)

// fakeConnector is the smallest thing that satisfies interfaces.Connector.
// Only the metadata getters carry real values — those are what the socket reads.
type fakeConnector struct {
	typ      string
	enabled  bool
	category string
}

func (f *fakeConnector) GetType() string     { return f.typ }
func (f *fakeConnector) GetName() string     { return f.typ }
func (f *fakeConnector) GetMode() string     { return interfaces.ConnectorModeLocal }
func (f *fakeConnector) GetCategory() string { return f.category }
func (f *fakeConnector) GetEnabled() bool    { return f.enabled }
func (f *fakeConnector) SetEnabled(v bool)   { f.enabled = v }

func (f *fakeConnector) GetSensitiveFields() []string { return []string{"password"} }
func (f *fakeConnector) GetFieldConfig() map[string]interfaces.ConnectorFieldConfig {
	return map[string]interfaces.ConnectorFieldConfig{
		"host": {Name: "主机地址", Type: "string", Required: true},
	}
}

func (f *fakeConnector) New(interfaces.ConnectorConfig) (interfaces.Connector, error) {
	return &fakeConnector{typ: f.typ, category: f.category}, nil
}
func (f *fakeConnector) Connect(context.Context) error        { return nil }
func (f *fakeConnector) Ping(context.Context) error           { return nil }
func (f *fakeConnector) Close(context.Context) error          { return nil }
func (f *fakeConnector) TestConnection(context.Context) error { return nil }
func (f *fakeConnector) GetMetadata(context.Context) (map[string]any, error) {
	return map[string]any{}, nil
}

// entry is the shape an enterprise Setup registers. Kept here so a change to
// ExtraConnector breaks this file rather than only the ee repository.
//
// The capability is derived from the tier rather than fixed, because the
// assembly registry rejects one capability claiming two different minimum
// editions — a real constraint on how these entries are written: every
// connector under `connector_certified` has to sell at the same tier, and a
// connector that sells higher needs its own capability key.
func entry(typ string, min licverify.Edition) ExtraConnector {
	return ExtraConnector{
		Capability:  "connector_certified_" + string(min),
		MinEdition:  min,
		Type:        typ,
		Description: typ + " connector",
		New: func() interfaces.Connector {
			return &fakeConnector{typ: typ, category: interfaces.ConnectorCategoryTable}
		},
	}
}

// reset reopens the assembly window and clears the socket. Registration is only
// legal while assembling, which is the invariant every one of these tests needs
// to set up before it can register anything.
func reset(t *testing.T) {
	t.Helper()
	ResetForTest(entitlement.FixedGate(licverify.EditionCommunity))
}

func TestRegisterAndAll(t *testing.T) {
	reset(t)
	Register(entry("demo", licverify.EditionProfessional))

	all := All()
	if len(all) != 1 || all[0].Type != "demo" {
		t.Fatalf("All() = %+v, want one entry for demo", all)
	}
}

// TestAllIgnoresLicence pins the reason registration is unconditional: the
// implementation must be installed whatever certificate is in force, so that
// one arriving later needs no restart.
func TestAllIgnoresLicence(t *testing.T) {
	reset(t)
	Register(entry("demo", licverify.EditionEnterprise))
	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionCommunity))

	if len(All()) != 1 {
		t.Fatal("All() dropped an entry because of the licence; it must not consult it")
	}
}

func TestLicensedFollowsEdition(t *testing.T) {
	reset(t)
	Register(entry("pro", licverify.EditionProfessional))
	Register(entry("ent", licverify.EditionEnterprise))

	cases := []struct {
		edition licverify.Edition
		want    []string
	}{
		{licverify.EditionCommunity, nil},
		{licverify.EditionProfessional, []string{"pro"}},
		{licverify.EditionEnterprise, []string{"ent", "pro"}},
	}
	for _, tc := range cases {
		entitlement.SetGateForTest(entitlement.FixedGate(tc.edition))

		got := map[string]bool{}
		for _, c := range Licensed() {
			got[c.Type] = true
		}
		if len(got) != len(tc.want) {
			t.Errorf("edition %s: Licensed() = %v, want %v", tc.edition, got, tc.want)
			continue
		}
		for _, w := range tc.want {
			if !got[w] {
				t.Errorf("edition %s: Licensed() missing %s", tc.edition, w)
			}
		}
	}
}

// TestAllowedVersusRegistered separates the two questions callers confuse:
// "did this build carry the type" and "may it be used right now".
func TestAllowedVersusRegistered(t *testing.T) {
	reset(t)
	Register(entry("demo", licverify.EditionEnterprise))
	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionProfessional))

	if !Registered("demo") {
		t.Error("Registered(demo) = false; the build does carry it")
	}
	if Allowed("demo") {
		t.Error("Allowed(demo) = true under a professional licence; the entry needs enterprise")
	}
	if Registered("mariadb") || Allowed("mariadb") {
		t.Error("a built-in type must not be claimed by this socket")
	}
}

// TestTypeOfDerivesFromPrototype is the guard against the field config being
// written twice. The factory treats a mismatch between code and catalogue as
// fatal, so the catalogue record has to be derived from the implementation
// rather than restated beside it.
func TestTypeOfDerivesFromPrototype(t *testing.T) {
	reset(t)
	e := entry("demo", licverify.EditionProfessional)

	ct := TypeOf(e)
	if ct.Type != "demo" || ct.Mode != interfaces.ConnectorModeLocal ||
		ct.Category != interfaces.ConnectorCategoryTable {
		t.Fatalf("TypeOf() = %+v, want metadata read off the prototype", ct)
	}
	if _, ok := ct.FieldConfig["host"]; !ok {
		t.Error("TypeOf() dropped the prototype's field config")
	}
	if !ct.Enabled {
		t.Error("TypeOf() = disabled; a paid connector has no row to switch off, the licence decides")
	}
}

func TestRegisterRejectsTypeMismatch(t *testing.T) {
	reset(t)
	e := entry("demo", licverify.EditionProfessional)
	e.Type = "not-demo"

	defer func() {
		if recover() == nil {
			t.Error("Register accepted an entry whose Type disagrees with the prototype")
		}
	}()
	Register(e)
}

func TestRegisterRejectsMissingBuilder(t *testing.T) {
	reset(t)
	defer func() {
		if recover() == nil {
			t.Error("Register accepted an entry with no New func")
		}
	}()
	Register(ExtraConnector{Capability: "c", MinEdition: licverify.EditionProfessional, Type: "demo"})
}
