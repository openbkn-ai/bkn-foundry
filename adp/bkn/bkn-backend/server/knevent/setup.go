// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package knevent is the knowledge-network event module of bkn-backend. Events
// record what happened in the business: a rule found an object in an abnormal
// state, or a person reported something. Every write appends to the network's
// change feed, which applications read to handle the events in their own tasks.
// Each kind of event is an event class, an object type of the network whose
// kinds include event; the module provides that kind. It serves from the
// professional edition up.
package knevent

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/licverify"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/drivenadapters/action_execution"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/drivenadapters/agent_operator"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/driveradapters"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	corelogics "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/action_schedule"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/relation_type"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/vega_backend"
	libdb "github.com/openbkn-ai/bkn-foundry/comm-go/db"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
)

// RouteExtension names the event routes among the application's route extensions. It is also
// the module's capability name.
const RouteExtension = "kn_events"

// minEdition is the lowest edition that may use events.
const minEdition = licverify.EditionProfessional

// Host is the bkn-backend application the module is assembled into.
type Host interface {
	Setting() *common.AppSetting
	RegisterRoutes(name string, install func(*gin.Engine)) error
	RegisterWorker(name string, run func(context.Context) error) error
}

// entitled reports whether the license allows events now. It is asked on every call, so a
// license installed later takes effect without a restart.
func entitled() bool { return entitlement.AtLeast(minEdition) }

// gate answers a call of the event API without the license as a path the server does not
// have: what an edition lacks is told by the capability snapshot, not by an error.
func gate(c *gin.Context) {
	if !entitled() {
		c.String(http.StatusNotFound, "404 page not found")
		c.Abort()
		return
	}
	c.Next()
}

// Setup assembles the event module into host: the event kind of object types, the routes, the
// cleanup that runs when a network is deleted, the purge of expired check refreshes, and the
// worker that keeps the event classes' datasets.
func Setup(host Host) error {
	entitlement.MarkAssembled(RouteExtension, minEdition)
	setting := host.Setting()
	store := newStore(libdb.NewDB(&setting.DBSetting))
	corelogics.SetKnEventCleanupAccess(store)
	go purgeEvery(context.Background(), store, purgePeriod)
	classes := &classes{datasets: vega_backend.NewVegaBackendService(setting, corelogics.VBA)}
	corelogics.RegisterObjectTypeKind(interfaces.ObjectTypeKindEvent, &eventKind{
		store: store, classes: classes,
		relations:    relation_type.NewRelationTypeService(setting),
		objects:      corelogics.OTA,
		declarations: corelogics.NewNetworkRelations(),
		available:    entitled,
	})
	corelogics.RegisterActionTypeOperation(interfaces.ActionOperationEvent, &eventAction{
		store:   store,
		objects: corelogics.OTA,
		actions: corelogics.ATA,
		tools:   agent_operator.NewCallerToolAccess(setting),
		trial:   action_execution.NewActionTrialAccess(setting),
		mainChanged: func(ctx context.Context, knID string) error {
			return action_schedule.SyncEventSchedules(ctx, corelogics.ASA, corelogics.ATA, knID, time.Now())
		},
		available: entitled,
	})
	service := newService(setting, store)
	if err := host.RegisterWorker(ProjectionWorker, newProjector(store, classes).run); err != nil {
		return err
	}
	handling := newHandler(store, action_execution.NewEventHandlingAccess(setting))
	if err := host.RegisterWorker(HandlingWorker, handling.run); err != nil {
		return err
	}
	return host.RegisterRoutes(RouteExtension, func(engine *gin.Engine) {
		driveradapters.RegisterKnEventRoutes(engine, setting, service, gate)
	})
}
