// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package knevent is the knowledge-network event module of bkn-backend. Events
// record what happened in the business: a rule found an object in an abnormal
// state, or a person reported something. Every write appends to the network's
// change feed, which applications read to handle the events in their own tasks.
package knevent

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/driveradapters"
	corelogics "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/vega_backend"
	libdb "github.com/openbkn-ai/bkn-foundry/comm-go/db"
)

// RouteExtension names the event routes among the application's route extensions.
const RouteExtension = "kn_events"

// Host is the bkn-backend application the module is assembled into.
type Host interface {
	Setting() *common.AppSetting
	RegisterRoutes(name string, install func(*gin.Engine)) error
	RegisterWorker(name string, run func(context.Context) error) error
}

// Setup assembles the event module into host: the routes, the cleanup that
// runs when a network is deleted, the purge of expired check refreshes, and
// the worker that keeps the event classes' datasets.
func Setup(host Host) error {
	setting := host.Setting()
	store := newStore(libdb.NewDB(&setting.DBSetting))
	corelogics.SetKnEventCleanupAccess(store)
	go purgeEvery(context.Background(), store, purgePeriod)
	classes := &classes{
		definitions: newCoreDefinitions(setting),
		datasets:    vega_backend.NewVegaBackendService(setting, corelogics.VBA),
	}
	service := newService(setting, store, classes)
	if err := host.RegisterWorker(ProjectionWorker, newProjector(store, classes).run); err != nil {
		return err
	}
	return host.RegisterRoutes(RouteExtension, func(engine *gin.Engine) {
		driveradapters.RegisterKnEventRoutes(engine, setting, service)
	})
}
