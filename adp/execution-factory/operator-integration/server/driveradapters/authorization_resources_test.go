package driveradapters

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common/ormhelper"
)

func TestAuthorizationResourcePage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name       string
		url        string
		wantOffset int
		wantLimit  int
		wantErr    bool
	}{
		{name: "defaults", url: "/", wantOffset: 0, wantLimit: 20},
		{name: "explicit page", url: "/?offset=20&limit=50", wantOffset: 20, wantLimit: 50},
		{name: "negative offset", url: "/?offset=-1", wantErr: true},
		{name: "limit exceeds maximum", url: "/?limit=101", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest("GET", test.url, nil)
			offset, limit, err := authorizationResourcePage(ctx)
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, want error %v", err, test.wantErr)
			}
			if !test.wantErr && (offset != test.wantOffset || limit != test.wantLimit) {
				t.Fatalf("page = (%d, %d), want (%d, %d)", offset, limit, test.wantOffset, test.wantLimit)
			}
		})
	}
}

func TestAuthorizationResourceSortUsesResourceIDAsTieBreaker(t *testing.T) {
	for _, test := range []struct {
		resourceType string
		direction    ormhelper.SortOrder
		idField      string
	}{
		{resourceType: "tool_box", direction: ormhelper.SortOrderAsc, idField: "f_box_id"},
		{resourceType: "function", direction: ormhelper.SortOrderDesc, idField: "f_box_id"},
		{resourceType: "mcp", direction: ormhelper.SortOrderAsc, idField: "f_mcp_id"},
		{resourceType: "skill", direction: ormhelper.SortOrderDesc, idField: "f_skill_id"},
	} {
		t.Run(test.resourceType, func(t *testing.T) {
			sort := authorizationResourceSort(test.resourceType, test.direction)
			if len(sort.Fields) != 2 {
				t.Fatalf("sort fields = %d, want 2", len(sort.Fields))
			}
			if sort.Fields[0].Field != "f_name" || sort.Fields[0].Order != test.direction {
				t.Fatalf("primary sort = %+v", sort.Fields[0])
			}
			if sort.Fields[1].Field != test.idField || sort.Fields[1].Order != test.direction {
				t.Fatalf("tie-breaker sort = %+v", sort.Fields[1])
			}
		})
	}
}
