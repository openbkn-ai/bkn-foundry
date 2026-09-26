// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/permissionrequest"
)

// registerPermissionRequests mounts the self-service approval inbox.
func registerPermissionRequests(g *gin.RouterGroup, service *permissionrequest.Service) {
	group := g.Group("/permission-requests")
	group.GET("/mine", func(c *gin.Context) {
		page, err := service.ListRequestedPage(c.Request.Context(), c.GetString(ctxAccessorID), permissionRequestPageOptions(c))
		if writePermissionRequestError(c, err) {
			return
		}
		writePermissionRequestPage(c, page)
	})
	group.GET("/todo", func(c *gin.Context) {
		page, err := service.ListTodoPage(c.Request.Context(), c.GetString(ctxAccessorID), permissionRequestPageOptions(c))
		if writePermissionRequestError(c, err) {
			return
		}
		writePermissionRequestPage(c, page)
	})
	group.GET("/reviewed", func(c *gin.Context) {
		page, err := service.ListReviewedPage(c.Request.Context(), c.GetString(ctxAccessorID), permissionRequestPageOptions(c))
		if writePermissionRequestError(c, err) {
			return
		}
		writePermissionRequestPage(c, page)
	})
	group.GET("/todo/summary", func(c *gin.Context) {
		summary, err := service.GetTodoSummary(c.Request.Context(), c.GetString(ctxAccessorID))
		if writePermissionRequestError(c, err) {
			return
		}
		c.JSON(http.StatusOK, summary)
	})
	group.GET("/:id", func(c *gin.Context) {
		result, err := service.Get(c.Request.Context(), c.Param("id"))
		if writePermissionRequestError(c, err) {
			return
		}
		caller := c.GetString(ctxAccessorID)
		if result.RequesterID != caller {
			allowed, checkErr := service.CanView(c.Request.Context(), caller, result)
			if checkErr != nil {
				serverError(c, checkErr)
				return
			}
			if !allowed {
				replyPublicError(c, http.StatusForbidden)
				return
			}
		}
		c.JSON(http.StatusOK, result)
	})
	group.GET("/:id/reviews", func(c *gin.Context) {
		result, err := service.Get(c.Request.Context(), c.Param("id"))
		if writePermissionRequestError(c, err) {
			return
		}
		caller := c.GetString(ctxAccessorID)
		if result.RequesterID != caller {
			allowed, checkErr := service.CanView(c.Request.Context(), caller, result)
			if checkErr != nil {
				serverError(c, checkErr)
				return
			}
			if !allowed {
				replyPublicError(c, http.StatusForbidden)
				return
			}
		}
		reviews, err := service.ListDecisions(c.Request.Context(), result.ID)
		if writePermissionRequestError(c, err) {
			return
		}
		c.JSON(http.StatusOK, gin.H{"entries": reviews})
	})
	group.POST("/:id/cancel", func(c *gin.Context) {
		result, err := service.Cancel(c.Request.Context(), c.Param("id"), c.GetString(ctxAccessorID))
		if writePermissionRequestError(c, err) {
			return
		}
		c.JSON(http.StatusOK, result)
	})
	group.POST("/:id/decision", func(c *gin.Context) {
		var body struct {
			Decision string `json:"decision" binding:"required"`
			Comment  string `json:"comment"`
		}
		if !bind(c, &body) {
			return
		}
		requestID := strings.TrimSpace(c.Param("id"))
		result, err := service.Decide(c.Request.Context(), requestID, permissionrequest.DecisionInput{
			ReviewerID: c.GetString(ctxAccessorID), Decision: body.Decision, Comment: body.Comment,
		})
		if writePermissionRequestError(c, err) {
			return
		}
		c.JSON(http.StatusOK, result)
	})
}

func permissionRequestPageOptions(c *gin.Context) permissionrequest.PageOptions {
	limit, offset := 20, 0
	if value, err := strconv.Atoi(c.Query("limit")); err == nil && value > 0 && value <= 200 {
		limit = value
	}
	if value, err := strconv.Atoi(c.Query("offset")); err == nil && value >= 0 {
		offset = value
	}
	return permissionrequest.PageOptions{
		Limit:        limit,
		Offset:       offset,
		Sort:         c.Query("sort"),
		Direction:    c.Query("direction"),
		ResourceType: c.Query("resource_type"),
		ResourceID:   c.Query("resource_id"),
		Status:       c.Query("status"),
		ResourceName: c.Query("resource_name"),
		Requester:    c.Query("requester"),
	}
}

func writePermissionRequestPage(c *gin.Context, page permissionrequest.RequestPage) {
	c.JSON(http.StatusOK, gin.H{"entries": page.Entries, "total_count": page.TotalCount})
}

// registerPublicPermissionRequests implements the user-facing creation API.
// The authenticated subject is the only applicant and authorization target;
// a browser cannot nominate another account or a managed proxy.
func registerPublicPermissionRequests(g *gin.RouterGroup, service *permissionrequest.Service) {
	g.POST("/permission-requests", func(c *gin.Context) {
		var body struct {
			Resource struct {
				Type string `json:"type" binding:"required"`
				ID   string `json:"id" binding:"required"`
				Name string `json:"name"`
			} `json:"resource" binding:"required"`
			Operation  string   `json:"operation"`
			Operations []string `json:"operations"`
			Reason     string   `json:"reason"`
		}
		if !bind(c, &body) {
			return
		}
		applicant := c.GetString(ctxAccessorID)
		result, created, err := service.Create(c.Request.Context(), permissionrequest.CreateInput{
			RequesterID:  applicant,
			ResourceType: body.Resource.Type, ResourceID: body.Resource.ID, ResourceName: body.Resource.Name, Operation: body.Operation, Operations: body.Operations,
			Reason: body.Reason,
		})
		if writePermissionRequestError(c, err) {
			return
		}
		status := http.StatusOK
		if created {
			status = http.StatusCreated
		}
		c.JSON(status, result)
	})
}

func registerAdminPermissionRequests(g *gin.RouterGroup, service *permissionrequest.Service, e *authz.Enforcer) {
	g.GET("/permission-requests/pending", RequirePermission(e, "admin-authz", "grant"), func(c *gin.Context) {
		rows, err := service.ListPending(c.Request.Context())
		if writePermissionRequestError(c, err) {
			return
		}
		c.JSON(http.StatusOK, gin.H{"entries": rows})
	})
	g.GET("/permission-request-reviews", RequirePermission(e, "admin-authz", "grant"), func(c *gin.Context) {
		rows, err := service.ListAllDecisions(c.Request.Context())
		if writePermissionRequestError(c, err) {
			return
		}
		c.JSON(http.StatusOK, gin.H{"entries": rows})
	})
}

func writePermissionRequestError(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, permissionrequest.ErrInvalidRequest):
		replyPublicError(c, http.StatusBadRequest)
	case errors.Is(err, permissionrequest.ErrNotFound):
		replyPublicError(c, http.StatusNotFound)
	case errors.Is(err, permissionrequest.ErrForbidden):
		replyPublicError(c, http.StatusForbidden)
	case errors.Is(err, permissionrequest.ErrClosed):
		replyPublicError(c, http.StatusConflict)
	case errors.Is(err, permissionrequest.ErrPermissionAlreadyGranted):
		replyPublicErrorDetails(c, http.StatusConflict, gin.H{"reason": "permission_already_granted"})
	case errors.Is(err, permissionrequest.ErrPrerequisiteMissing):
		replyPublicErrorDetails(c, http.StatusConflict, gin.H{"reason": "missing_prerequisite"})
	case errors.Is(err, permissionrequest.ErrResourceDeleted):
		replyPublicErrorDetails(c, http.StatusConflict, gin.H{"reason": "resource_deleted"})
	case errors.Is(err, permissionrequest.ErrResourceUnavailable):
		replyPublicError(c, http.StatusServiceUnavailable)
	case errors.Is(err, permissionrequest.ErrUnsupportedResourceType):
		replyPublicError(c, http.StatusBadRequest)
	default:
		serverError(c, err)
	}
	return true
}
