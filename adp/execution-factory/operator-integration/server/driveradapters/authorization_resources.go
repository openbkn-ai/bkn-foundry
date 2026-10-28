package driveradapters

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/dbaccess"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common/ormhelper"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
)

// AuthorizationResource is the minimal representation used by bkn-safe's
// object-permission picker. It intentionally omits resource details.
type AuthorizationResource struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type authorizationResourceList struct {
	Entries []AuthorizationResource `json:"entries"`
	Total   int64                   `json:"total"`
}

type authorizationResourceHandler struct {
	toolboxes model.IToolboxDB
	mcp       model.DBMCPServerConfig
	skills    model.ISkillRepository
}

func newAuthorizationResourceHandler() *authorizationResourceHandler {
	return &authorizationResourceHandler{
		toolboxes: dbaccess.NewToolboxDB(),
		mcp:       dbaccess.NewMCPServerConfigDBSingleton(),
		skills:    dbaccess.NewSkillRepositoryDB(),
	}
}

// ListAuthorizationResources handles GET /authorization-resources on the
// internal-v1 face. It deliberately does not apply per-user resource grants.
func (h *authorizationResourceHandler) ListAuthorizationResources(c *gin.Context) {
	resourceType := strings.TrimSpace(c.Query("resource_type"))
	name := strings.TrimSpace(c.Query("name"))
	direction := strings.ToLower(strings.TrimSpace(c.DefaultQuery("direction", "asc")))
	sort := strings.TrimSpace(c.DefaultQuery("sort", "name"))
	offset, limit, err := authorizationResourcePage(c)
	if err != nil || sort != "name" || (direction != "asc" && direction != "desc") {
		rest.ReplyError(c, errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, "invalid authorization resource query"))
		return
	}

	sortParams := authorizationResourceSort(resourceType, ormhelper.SortOrder(direction))
	var result authorizationResourceList
	switch resourceType {
	case "tool_box", "function":
		metadataType := "openapi"
		if resourceType == "function" {
			metadataType = "function"
		}
		filter := map[string]interface{}{"metadata_type": metadataType, "name": name, "limit": limit, "offset": offset}
		total, listErr := h.toolboxes.CountToolBox(c.Request.Context(), filter)
		if listErr == nil {
			var rows []*model.ToolboxDB
			rows, listErr = h.toolboxes.SelectToolBoxList(c.Request.Context(), filter, sortParams, nil)
			for _, row := range rows {
				result.Entries = append(result.Entries, AuthorizationResource{ID: row.BoxID, Name: row.Name})
			}
		}
		result.Total, err = total, listErr
	case "mcp":
		filter := map[string]interface{}{"name": name, "limit": limit, "offset": offset}
		total, listErr := h.mcp.CountByWhereClause(c.Request.Context(), nil, filter)
		if listErr == nil {
			var rows []*model.MCPServerConfigDB
			rows, listErr = h.mcp.SelectListPage(c.Request.Context(), nil, filter, sortParams, nil)
			for _, row := range rows {
				result.Entries = append(result.Entries, AuthorizationResource{ID: row.MCPID, Name: row.Name})
			}
		}
		result.Total, err = total, listErr
	case "skill":
		filter := map[string]interface{}{"name": name, "limit": limit, "offset": offset}
		total, listErr := h.skills.CountByWhereClause(c.Request.Context(), nil, filter)
		if listErr == nil {
			var rows []*model.SkillRepositoryDB
			rows, listErr = h.skills.SelectSkillListPage(c.Request.Context(), nil, filter, sortParams, nil)
			for _, row := range rows {
				result.Entries = append(result.Entries, AuthorizationResource{ID: row.SkillID, Name: row.Name})
			}
		}
		result.Total, err = total, listErr
	default:
		rest.ReplyError(c, errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, "unsupported authorization resource type"))
		return
	}
	if err != nil {
		rest.ReplyError(c, errors.DefaultHTTPError(c.Request.Context(), http.StatusInternalServerError, err.Error()))
		return
	}
	if result.Entries == nil {
		result.Entries = []AuthorizationResource{}
	}
	rest.ReplyOK(c, http.StatusOK, result)
}

// authorizationResourceSort makes offset pagination deterministic when several
// resources share a name. The resource ID is unique within each resource type.
func authorizationResourceSort(resourceType string, order ormhelper.SortOrder) *ormhelper.SortParams {
	idField := "f_skill_id"
	switch resourceType {
	case "tool_box", "function":
		idField = "f_box_id"
	case "mcp":
		idField = "f_mcp_id"
	}
	return &ormhelper.SortParams{Fields: []ormhelper.SortField{
		{Field: "f_name", Order: order},
		{Field: idField, Order: order},
	}}
}

func authorizationResourcePage(c *gin.Context) (int, int, error) {
	offset, err := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if err != nil || offset < 0 {
		return 0, 0, strconv.ErrSyntax
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if err != nil || limit < 1 || limit > 100 {
		return 0, 0, strconv.ErrSyntax
	}
	return offset, limit, nil
}
