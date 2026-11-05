// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbinstance"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

func (r *restHandler) registerKBBuild(group *gin.RouterGroup) {
	for _, route := range []struct{ method, path string }{{"GET", "/acceptance/references/status"}, {"GET", "/acceptance/references"}, {"POST", "/acceptance/references"}, {"GET", "/build-settings"}, {"GET", "/builds"}, {"GET", "/builds/:build_id/results"}, {"GET", "/builds/:build_id/system-links"}, {"POST", "/builds/:build_id/system-links"}, {"POST", "/builds"}, {"POST", "/builds/cancel"}, {"POST", "/builds/:build_id/cancel"}, {"POST", "/builds/:build_id/accept-files"}} {
		method, path := route.method, route.path
		group.Handle(method, path, func(c *gin.Context) {
			r.withExVisitor(c, func(c *gin.Context, visitor hydra.Visitor) {
				ctx := i18n.GetLanguageCtx(c)
				if visitor.Type != hydra.VisitorType_User && visitor.Type != hydra.VisitorType_RealName {
					replySourceError(c, ctx, &kb.SourceError{StatusCode: 403})
					return
				}
				providers, enabled := kb.Lookup()
				if !enabled {
					replyNotFound(c)
					return
				}
				if logics.KBBA == nil || ((path == "/build-settings" || (path == "/builds" && method == "POST")) && providers.Builds == nil) {
					replyBuildError(c, ctx, kb.ErrUnavailable)
					return
				}
				service := &kbinstance.BuildService{Projects: r.ps, Selections: r.kbra, Drafts: r.da, Store: logics.KBBA, Source: providers.Source, Runtime: providers.Builds}
				var references *kbinstance.ReferenceService
				if datasets, ok := providers.Datasets.(interface {
					kb.Datasets
					kb.DatasetReferenceRecords
				}); ok && logics.KBRefs != nil {
					references = &kbinstance.ReferenceService{Builds: service, Store: logics.KBRefs, Datasets: datasets}
					service.ReferenceReady = references.Ready
					service.FullReady = references.FullReady
				}
				var result any
				var err error
				status := http.StatusOK
				switch {
				case path == "/builds/:build_id/system-links":
					datasets, available := providers.Datasets.(interface {
						kb.DatasetFileSnapshot
						kb.DatasetFileRecords
					})
					if !available {
						err = kb.ErrUnavailable
						break
					}
					links := &kbinstance.SystemLinkService{Builds: service, Datasets: datasets, Vega: logics.VA, References: references}
					if method == http.MethodGet {
						result, err = links.Preview(ctx, visitor.TokenID, c.Param("project_id"), c.Param("build_id"), visitorAccount(visitor))
					} else {
						c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
						decoder := json.NewDecoder(c.Request.Body)
						decoder.DisallowUnknownFields()
						var request kbinstance.SystemLinkRequest
						var trailing any
						if decoder.Decode(&request) != nil || decoder.Decode(&trailing) != io.EOF {
							err = &kb.SourceError{StatusCode: 400}
							break
						}
						result, err = links.Apply(ctx, visitor.TokenID, c.Param("project_id"), c.Param("build_id"), visitorAccount(visitor), request)
					}
				case path == "/acceptance/references/status":
					if references == nil {
						err = kb.ErrUnavailable
					} else {
						result, err = references.Status(ctx, visitor.TokenID, c.Param("project_id"), visitorAccount(visitor))
					}
				case path == "/acceptance/references" && method == "GET":
					if references == nil {
						err = kb.ErrUnavailable
					} else {
						result, err = references.View(ctx, visitor.TokenID, c.Param("project_id"), c.Query("file"), visitorAccount(visitor))
					}
				case path == "/acceptance/references":
					if references == nil {
						err = kb.ErrUnavailable
						break
					}
					c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
					var request kbinstance.ReferenceRequest
					decoder := json.NewDecoder(c.Request.Body)
					decoder.DisallowUnknownFields()
					decoder.UseNumber()
					var trailing any
					if decoder.Decode(&request) != nil || decoder.Decode(&trailing) != io.EOF {
						err = &kb.SourceError{StatusCode: 400}
						break
					}
					result, err = references.Save(ctx, visitor.TokenID, c.Param("project_id"), visitorAccount(visitor), request)

				case path == "/builds/:build_id/results":
					offset := 0
					if raw := c.Query("offset"); raw != "" {
						var parseErr error
						offset, parseErr = strconv.Atoi(raw)
						if parseErr != nil {
							replySourceError(c, ctx, &kb.SourceError{StatusCode: 400})
							return
						}
					}
					result, err = service.Results(ctx, visitor.TokenID, c.Param("project_id"), c.Param("build_id"), c.Query("group"), c.Query("file"), offset, visitorAccount(visitor), logics.VA)
				case path == "/builds/cancel":
					var count int
					count, err = service.CancelAll(ctx, visitor.TokenID, c.Param("project_id"), visitorAccount(visitor))
					result = gin.H{"accepted": count}
				case path == "/build-settings":
					result, err = service.Settings(ctx, visitor.TokenID, c.Param("project_id"), visitorAccount(visitor))
				case method == "GET":
					result, err = service.List(ctx, visitor.TokenID, c.Param("project_id"), visitorAccount(visitor))
				case path == "/builds/:build_id/accept-files":
					c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
					var request struct {
						Revision int64 `json:"revision"`
					}
					if c.ShouldBindJSON(&request) != nil {
						replySourceError(c, ctx, &kb.SourceError{StatusCode: 400})
						return
					}
					var build *interfaces.KBBuild
					build, err = service.AcceptFiles(ctx, visitor.TokenID, c.Param("project_id"), c.Param("build_id"), request.Revision, visitorAccount(visitor))
					if err == nil && build == nil {
						err = kbinstance.ErrBuildChanged
					}
					result = build
				case c.Param("build_id") != "":
					err = service.Cancel(ctx, visitor.TokenID, c.Param("project_id"), c.Param("build_id"), visitorAccount(visitor))
					result = gin.H{"accepted": true}
				default:
					c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
					var req kbinstance.BuildRequest
					if c.ShouldBindJSON(&req) != nil {
						replySourceError(c, ctx, &kb.SourceError{StatusCode: 400})
						return
					}
					result, err = service.Start(ctx, visitor.TokenID, c.Param("project_id"), visitorAccount(visitor), req)
					status = http.StatusAccepted
				}
				if err != nil {
					replyBuildError(c, ctx, err)
					return
				}
				rest.ReplyOK(c, status, result)
			})
		})
	}
}

func replyBuildError(c *gin.Context, ctx context.Context, err error) {
	status, key := http.StatusConflict, ""
	values := map[string]any{}
	var identity *kbinstance.FileIdentityError
	var agent *interfaces.BknAgentError
	switch {
	case errors.Is(err, kbinstance.ErrAcceptanceHidden):
		status, key = http.StatusForbidden, "BuildAcceptanceHidden"
	case errors.As(err, &identity):
		key = "BuildFileIdentity"
		values["name"] = identity.Name
	case errors.Is(err, kbinstance.ErrAcceptanceRequired):
		key = "BuildReviewFirst"
	case errors.Is(err, kbinstance.ErrNothingToAccept):
		key = "BuildNothingToAccept"
	case errors.Is(err, kbinstance.ErrInvalidBuild), errors.Is(err, kbinstance.ErrBuildChanged):
		key = "BuildChanged"
	case errors.Is(err, kb.ErrUnavailable), errors.As(err, &agent):
		status, key = http.StatusServiceUnavailable, "BuildUnavailable"
	default:
		replyConceptError(c, ctx, err)
		return
	}
	message, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknProject.KnowledgeBase."+key, values)
	code := httperrors.PublicError_BadRequest
	if status >= 500 {
		code = httperrors.PublicError_ServiceUnavailable
	}
	result := httperrors.NewHTTPError(ctx, status, code)
	if renderErr == nil {
		_ = result.WithErrorDetails(message)
	}
	rest.ReplyError(c, result)
}
