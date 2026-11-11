// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package knevent assembles knowledge-network events into the open-core
// bkn-backend process. Events record what happened in the business: a rule
// found an object in an abnormal state, or a person reported something. Every
// write appends to the network's change feed, which applications read to
// handle the events in their own tasks.
package knevent

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	coreapp "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/app"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/driveradapters"
	corelogics "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/vega_backend"
	libdb "github.com/openbkn-ai/bkn-foundry/comm-go/db"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/licverify"
)

const Capability = "kn_events"

var MinEdition = licverify.EditionProfessional

// Setup registers the implementation unconditionally. The middleware reads
// the live licence on every request, so importing a Professional licence after
// process start takes effect without restarting the service. Cleanup is not
// gated: networks with events stay deletable after a licence downgrade, and
// expired check refreshes keep being purged.
func Setup(application *coreapp.Application) error {
	setting := application.Setting()
	store := newStore(libdb.NewDB(&setting.DBSetting))
	corelogics.SetKnEventCleanupAccess(store)
	go purgeEvery(context.Background(), store, purgePeriod)
	classes := &classes{
		definitions: newCoreDefinitions(setting),
		datasets:    vega_backend.NewVegaBackendService(setting, corelogics.VBA),
	}
	service := newService(setting, store, classes)
	// The datasets behind the event classes are written while the licence
	// covers events, which is read each round as the routes read it.
	projector := newProjector(store, classes, func() bool { return entitlement.AtLeast(MinEdition) })
	if err := application.RegisterWorker(ProjectionWorker, projector.run); err != nil {
		return err
	}
	entitlement.MarkAssembled(Capability, MinEdition)
	return application.RegisterRoutes(Capability, func(engine *gin.Engine) {
		driveradapters.RegisterKnEventRoutes(engine, setting, service, requireEntitlement())
	})
}

func requireEntitlement() gin.HandlerFunc {
	return func(c *gin.Context) {
		if entitlement.AtLeast(MinEdition) {
			c.Next()
			return
		}
		logger.Infof("knevent: route hidden because licence is below the required tier: min_edition=%s edition=%s method=%s path=%s",
			MinEdition, entitlement.Current(), c.Request.Method, c.Request.URL.Path)
		// Match gin's unmounted-route response on the wire, as objectmetric does.
		c.Writer.Header().Del("Cache-Control")
		c.Abort()
		c.Data(http.StatusNotFound, gin.MIMEPlain, []byte("404 page not found"))
	}
}
