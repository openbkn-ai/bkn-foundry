// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package factory

import (
	"context"
	"testing"

	"github.com/openbkn-ai/licverify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	extconn "github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/extension/connector"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"

	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/interfaces"
)

// These tests walk the whole path a paid connector takes — register on the
// socket, get installed by the factory, then be usable or not depending on the
// certificate — because that is the part neither the socket's own tests nor the
// factory's own tests can see. They also double as the worked example for
// whoever adds the next one.

// demoConnector stands in for a connector the enterprise code line contributes.
// Only the metadata getters carry real values; nothing here talks to anything.
type demoConnector struct{ enabled bool }

func (d *demoConnector) GetType() string     { return "demodb" }
func (d *demoConnector) GetName() string     { return "demodb" }
func (d *demoConnector) GetMode() string     { return interfaces.ConnectorModeLocal }
func (d *demoConnector) GetCategory() string { return interfaces.ConnectorCategoryTable }
func (d *demoConnector) GetEnabled() bool    { return d.enabled }
func (d *demoConnector) SetEnabled(v bool)   { d.enabled = v }

func (d *demoConnector) GetSensitiveFields() []string { return []string{"password"} }
func (d *demoConnector) GetFieldConfig() map[string]interfaces.ConnectorFieldConfig {
	return map[string]interfaces.ConnectorFieldConfig{
		"host":     {Name: "主机地址", Type: "string", Required: true},
		"password": {Name: "密码", Type: "string", Required: true, Encrypted: true},
	}
}

func (d *demoConnector) New(interfaces.ConnectorConfig) (interfaces.Connector, error) {
	return &demoConnector{enabled: true}, nil
}
func (d *demoConnector) Connect(context.Context) error        { return nil }
func (d *demoConnector) Ping(context.Context) error           { return nil }
func (d *demoConnector) Close(context.Context) error          { return nil }
func (d *demoConnector) TestConnection(context.Context) error { return nil }
func (d *demoConnector) GetMetadata(context.Context) (map[string]any, error) {
	return map[string]any{}, nil
}

// setupDemoConnector is what an enterprise Setup() does, in miniature: reopen
// the assembly window with a given licence, register one paid connector, and
// hand back a factory with the built-ins plus whatever the socket holds.
func setupDemoConnector(t *testing.T, ed licverify.Edition) *ConnectorFactory {
	t.Helper()
	extconn.ResetForTest(entitlement.FixedGate(ed))
	t.Cleanup(func() { extconn.ResetForTest(entitlement.FixedGate(licverify.EditionCommunity)) })

	extconn.Register(extconn.ExtraConnector{
		Capability:  "connector_certified",
		MinEdition:  licverify.EditionProfessional,
		Type:        "demodb",
		Description: "demo connector",
		New:         func() interfaces.Connector { return &demoConnector{} },
	})

	cf := &ConnectorFactory{connectors: map[string]interfaces.Connector{}}
	cf.InitLocalConnectors()
	return cf
}

// TestExtensionConnectorInstalledRegardlessOfLicence is the invariant that
// makes "install a certificate, no restart" work. If installation were
// conditional, a process that booted unlicensed would have an empty slot
// forever, and the socket is frozen by then.
func TestExtensionConnectorInstalledRegardlessOfLicence(t *testing.T) {
	for _, ed := range []licverify.Edition{
		licverify.EditionCommunity,
		licverify.EditionProfessional,
	} {
		cf := setupDemoConnector(t, ed)

		assert.Contains(t, cf.connectors, "demodb",
			"edition %s: the implementation must be installed whatever the licence says", ed)
		assert.True(t, cf.connectors["demodb"].GetEnabled(),
			"edition %s: a paid connector has no catalogue row to enable it, so the factory must", ed)
	}
}

func TestExtensionConnectorUsableWhenLicensed(t *testing.T) {
	cf := setupDemoConnector(t, licverify.EditionProfessional)

	c, err := cf.CreateConnectorInstance(context.Background(), "demodb", interfaces.ConnectorConfig{})
	require.NoError(t, err)
	assert.Equal(t, "demodb", c.GetType())
}

// TestExtensionConnectorLooksAbsentWhenUnlicensed is the refusal shape. The
// error has to be the one an unknown type produces, word for word: an
// under-licensed enterprise image must not be distinguishable from a community
// one by what it says when refused.
func TestExtensionConnectorLooksAbsentWhenUnlicensed(t *testing.T) {
	cf := setupDemoConnector(t, licverify.EditionCommunity)
	ctx := context.Background()

	_, paidErr := cf.CreateConnectorInstance(ctx, "demodb", interfaces.ConnectorConfig{})
	require.Error(t, paidErr)

	_, unknownErr := cf.CreateConnectorInstance(ctx, "nosuchtype", interfaces.ConnectorConfig{})
	require.Error(t, unknownErr)

	assert.Equal(t, "connector nosuchtype not found", unknownErr.Error())
	assert.Equal(t, "connector demodb not found", paidErr.Error(),
		"the refusal must read like an unknown type, not like a licensing error")
}

// TestLicenceChangeTakesEffectWithoutReinstall pins the reason the check lives
// on the call rather than on installation: one factory, two answers, no
// restart in between.
func TestLicenceChangeTakesEffectWithoutReinstall(t *testing.T) {
	cf := setupDemoConnector(t, licverify.EditionCommunity)
	ctx := context.Background()

	_, err := cf.CreateConnectorInstance(ctx, "demodb", interfaces.ConnectorConfig{})
	require.Error(t, err, "unlicensed at this point")

	// The certificate arrives. Nothing is rebuilt, nothing re-registers.
	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionProfessional))

	_, err = cf.CreateConnectorInstance(ctx, "demodb", interfaces.ConnectorConfig{})
	assert.NoError(t, err, "a certificate installed later must take effect on the next call")

	// And it lapses again.
	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionCommunity))

	_, err = cf.CreateConnectorInstance(ctx, "demodb", interfaces.ConnectorConfig{})
	assert.Error(t, err, "a lapsed certificate must close the connector on the next call")
}

// TestBuiltInConnectorsUnaffected guards the direction that would be worst to
// get wrong: the socket must never make a built-in connector need permission.
func TestBuiltInConnectorsUnaffected(t *testing.T) {
	cf := setupDemoConnector(t, licverify.EditionCommunity)

	require.Contains(t, cf.connectors, interfaces.ConnectorTypeMariaDB)
	assert.False(t, extconn.Registered(interfaces.ConnectorTypeMariaDB),
		"a built-in type must not be claimed by the extension socket")
	assert.False(t, extconn.Allowed(interfaces.ConnectorTypeMariaDB),
		"Allowed answers only for socket types; built-ins go through their own path")
}
