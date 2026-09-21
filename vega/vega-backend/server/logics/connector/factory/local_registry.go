// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package factory

import (
	"fmt"
	"sort"
	"sync"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/licverify"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/local/fileset/anyshare"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/local/index/opensearch"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/local/table/mariadb"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/local/table/postgresql"
)

// LocalConnectorRegistration describes a local connector implementation
// composed into the Vega binary. New must return a fresh, unconfigured
// connector builder on every call.
//
// Aliases are additional connector-type keys served by the same implementation.
// They exist for compatibility with MySQL, which is implemented by the MariaDB
// connector. Each alias has the same minimum edition as Type.
type LocalConnectorRegistration struct {
	Type       string
	Aliases    []string
	MinEdition licverify.Edition
	New        func() interfaces.Connector
}

type localConnectorRegistry struct {
	mu sync.RWMutex

	registrations  map[string]LocalConnectorRegistration
	coreRegistered bool
	frozen         bool
}

var localConnectorRegistrations = &localConnectorRegistry{
	registrations: map[string]LocalConnectorRegistration{},
}

// RegisterCoreLocalConnectors registers the local implementations supplied by
// Foundry. App.Boot calls it during assembly, before an Enterprise entry point
// may add its own registrations.
func RegisterCoreLocalConnectors() {
	localConnectorRegistrations.registerCore([]LocalConnectorRegistration{
		{
			Type:       interfaces.ConnectorTypeMariaDB,
			Aliases:    []string{interfaces.ConnectorTypeMySQL},
			MinEdition: licverify.EditionCommunity,
			New: func() interfaces.Connector {
				return mariadb.NewMariaDBConnector()
			},
		},
		{
			Type:       interfaces.ConnectorTypeOpenSearch,
			MinEdition: licverify.EditionCommunity,
			New: func() interfaces.Connector {
				return opensearch.NewOpenSearchConnector()
			},
		},
		{
			Type:       interfaces.ConnectorTypePostgreSQL,
			MinEdition: licverify.EditionCommunity,
			New: func() interfaces.Connector {
				return postgresql.NewPostgresqlConnector()
			},
		},
		{
			Type:       interfaces.ConnectorTypeAnyShare,
			MinEdition: licverify.EditionCommunity,
			New: func() interfaces.Connector {
				return anyshare.NewAnyShareConnector()
			},
		},
	})
}

// RegisterLocalConnector registers one local connector during application
// assembly. It never checks the current license; availability is evaluated per
// request by the factory after the application has started.
func RegisterLocalConnector(registration LocalConnectorRegistration) {
	localConnectorRegistrations.register(registration)
}

func (r *localConnectorRegistry) registerCore(registrations []LocalConnectorRegistration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.coreRegistered {
		return
	}
	for _, registration := range registrations {
		r.registerLocked(registration)
	}
	r.coreRegistered = true
}

func (r *localConnectorRegistry) register(registration LocalConnectorRegistration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.registerLocked(registration)
}

func (r *localConnectorRegistry) registerLocked(registration LocalConnectorRegistration) {
	if r.frozen {
		panic(fmt.Sprintf("vega connector factory: %q registered after Freeze", registration.Type))
	}
	entitlement.MustBeAssembling("vega connector factory:" + registration.Type)
	if registration.Type == "" {
		panic("vega connector factory: RegisterLocalConnector with an empty type")
	}
	if registration.New == nil {
		panic(fmt.Sprintf("vega connector factory: %q registered without a constructor", registration.Type))
	}
	if registration.MinEdition == "" || !registration.MinEdition.Known() {
		panic(fmt.Sprintf("vega connector factory: %q registered with an unknown minimum edition %q", registration.Type, registration.MinEdition))
	}
	if _, exists := r.registrations[registration.Type]; exists {
		panic(fmt.Sprintf("vega connector factory: %q registered twice", registration.Type))
	}
	for _, alias := range registration.Aliases {
		if alias == "" || alias == registration.Type {
			panic(fmt.Sprintf("vega connector factory: %q has an invalid alias %q", registration.Type, alias))
		}
	}
	r.registrations[registration.Type] = registration
}

// LocalConnectorRegistrations returns all assembled registrations in stable
// type order. The connector factory consumes them when it is materialized.
func LocalConnectorRegistrations() []LocalConnectorRegistration {
	return localConnectorRegistrations.all()
}

func (r *localConnectorRegistry) all() []LocalConnectorRegistration {
	r.mu.RLock()
	defer r.mu.RUnlock()

	types := make([]string, 0, len(r.registrations))
	for connectorType := range r.registrations {
		types = append(types, connectorType)
	}
	sort.Strings(types)

	registrations := make([]LocalConnectorRegistration, 0, len(types))
	for _, connectorType := range types {
		registrations = append(registrations, r.registrations[connectorType])
	}
	return registrations
}

// FreezeLocalConnectorRegistrations closes connector registration assembly
// before factories, workers, and HTTP handlers are created.
func FreezeLocalConnectorRegistrations() {
	localConnectorRegistrations.freeze()
}

func (r *localConnectorRegistry) freeze() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		panic("vega connector factory: FreezeLocalConnectorRegistrations called twice")
	}
	r.frozen = true
	entitlement.Freeze()
}

// ResetLocalConnectorRegistrationsForTest clears the registry. Callers must
// also reset entitlement because it owns the shared assembly-window state.
func ResetLocalConnectorRegistrationsForTest() {
	if !testing.Testing() {
		panic("vega connector factory: ResetLocalConnectorRegistrationsForTest is test-only")
	}
	localConnectorRegistrations.mu.Lock()
	defer localConnectorRegistrations.mu.Unlock()
	localConnectorRegistrations.registrations = map[string]LocalConnectorRegistration{}
	localConnectorRegistrations.coreRegistered = false
	localConnectorRegistrations.frozen = false
}
