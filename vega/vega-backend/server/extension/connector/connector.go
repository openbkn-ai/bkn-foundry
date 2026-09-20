// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package connector is the assembly socket for local Vega connectors supplied
// by the enterprise overlay. Community builds leave this registry empty.
//
// Registration is intentionally separate from connector-type persistence. A
// registration says that an implementation is present in this binary; Vega's
// existing connector-type records still decide its display name, category, and
// enabled state. The application freezes this socket before it materializes the
// factory, workers, and HTTP handlers.
package connector

import (
	"fmt"

	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement/socket"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/licverify"
)

// LocalConnector is a local implementation contributed by a composed binary.
// New must return a fresh, unconfigured connector builder on every call.
type LocalConnector struct {
	Capability string
	MinEdition licverify.Edition
	Type       string
	New        func() interfaces.Connector
}

var localConnectors = socket.New[LocalConnector]("vega connector")

// RegisterLocal registers one local connector during application assembly.
// It never checks the current license; availability is evaluated per request
// by the factory wrapper after the application has started.
func RegisterLocal(connector LocalConnector) {
	if connector.Type == "" {
		panic("vega connector: RegisterLocal with an empty type")
	}
	if connector.New == nil {
		panic(fmt.Sprintf("vega connector: %q registered without a constructor", connector.Type))
	}
	localConnectors.Add(connector.Type, connector.Capability, connector.MinEdition, connector)
}

// LocalConnectors returns the assembled local connectors in stable type order.
// It is consumed once by the factory after the application has frozen assembly.
func LocalConnectors() []LocalConnector { return localConnectors.All() }

// Freeze closes the complete extension assembly registry. It belongs to the
// application lifecycle rather than an individual connector because all Vega
// extension sockets must close before workers and HTTP handlers are created.
func Freeze() { entitlement.Freeze() }

// ResetForTest clears this socket. Callers must also reset entitlement because
// the shared assembly registry carries the frozen state and capability records.
func ResetForTest() { localConnectors.ResetForTest() }
