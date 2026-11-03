// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
package driveradapters

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbrefresh"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"io"
	"net/http"
	"slices"
)

func (r *restHandler) registerKnowledgeBaseRefresh(engine *gin.Engine) {
	group := engine.Group("/api/bkn-project/v1/lineages/:lineage_id/kb-refresh")
	group.Use(rest.PrivateNoCacheMiddleware())
	group.GET("", func(c *gin.Context) { r.refreshRequest(c, false) })
	group.POST("", func(c *gin.Context) { r.refreshRequest(c, true) })
}
func (r *restHandler) refreshRequest(c *gin.Context, mutation bool) {
	r.withExVisitor(c, func(c *gin.Context, visitor hydra.Visitor) {
		ctx := i18n.GetLanguageCtx(c)
		if visitor.Type != hydra.VisitorType_User && visitor.Type != hydra.VisitorType_RealName {
			replyBuildError(c, ctx, &kb.SourceError{StatusCode: 403})
			return
		}
		if _, enabled := kb.Lookup(); !enabled {
			replyNotFound(c)
			return
		}
		service := kbrefresh.NewService(nil)
		if r.refreshFactory != nil {
			service = r.refreshFactory()
		}
		if service == nil || service.Journal == nil || logics.BBA == nil {
			replyBuildError(c, ctx, kb.ErrUnavailable)
			return
		}
		lineage := c.Param("lineage_id")
		publication, err := kbrefresh.Resolve(ctx, lineage, service.Releases, service.Publications)
		if err != nil {
			replyRefreshError(c, ctx, err)
			return
		}
		network, err := logics.BBA.GetKN(ctx, visitor.TokenID, publication.NetworkID)
		if err != nil {
			replyRefreshError(c, ctx, err)
			return
		}
		if network == nil || network.ID != publication.NetworkID {
			replyBuildError(c, ctx, &kb.SourceError{StatusCode: 403})
			return
		}
		canManage := slices.Contains(network.Operations, "modify") || slices.Contains(network.Operations, "full_business_access") || slices.Contains(network.Operations, "*")
		service.ExpectedNetwork = publication.NetworkID
		status := http.StatusOK
		if mutation {
			if !canManage {
				replyBuildError(c, ctx, &kb.SourceError{StatusCode: 403})
				return
			}
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
			decoder := json.NewDecoder(c.Request.Body)
			decoder.DisallowUnknownFields()
			var request struct {
				Action    string `json:"action"`
				NetworkID string `json:"network_id"`
				Revision  int64  `json:"revision"`
			}
			var extra any
			if decoder.Decode(&request) != nil || decoder.Decode(&extra) != io.EOF {
				replyBuildError(c, ctx, &kb.SourceError{StatusCode: 400})
				return
			}
			if request.NetworkID == "" {
				replyBuildError(c, ctx, &kb.SourceError{StatusCode: 400})
				return
			}
			if request.NetworkID != publication.NetworkID {
				replyRefreshError(c, ctx, kbrefresh.ErrNoPublishedTarget)
				return
			}
			switch request.Action {
			case "check":
				err = service.Check(ctx, lineage, uuid.NewString(), kbrefresh.ProcessLease)
			case "discard":
				if request.Revision < 1 {
					replyBuildError(c, ctx, &kb.SourceError{StatusCode: 400})
					return
				}
				err = service.DiscardBatch(ctx, lineage, kbrefresh.ProcessLease, request.Revision, interfaces.AccountInfo{ID: visitor.ID, Type: "user"})
			case "release":
				if request.Revision < 1 {
					replyBuildError(c, ctx, &kb.SourceError{StatusCode: 400})
					return
				}
				err = service.ReleaseBatch(ctx, lineage, kbrefresh.ProcessLease, request.Revision, interfaces.AccountInfo{ID: visitor.ID, Type: "user"})
			default:
				replyBuildError(c, ctx, &kb.SourceError{StatusCode: 400})
				return
			}
			if err != nil {
				replyRefreshError(c, ctx, err)
				return
			}
			status = http.StatusAccepted
		}
		result, err := service.Status(ctx, lineage, canManage)
		if err != nil {
			replyRefreshError(c, ctx, err)
			return
		}
		rest.ReplyOK(c, status, result)
	})
}
