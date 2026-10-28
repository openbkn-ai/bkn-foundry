package driveradapters

import (
	"sort"
	"testing"

	"github.com/gin-gonic/gin"
)

type stubCategoryHandler struct{}

func (stubCategoryHandler) CategoryList(*gin.Context)   {}
func (stubCategoryHandler) CategoryUpdate(*gin.Context) {}
func (stubCategoryHandler) CategoryCreate(*gin.Context) {}
func (stubCategoryHandler) CategoryDelete(*gin.Context) {}

func categoryRoutes(register func(*categoryRestHandler, *gin.RouterGroup)) []string {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	register(&categoryRestHandler{CategoryHandler: stubCategoryHandler{}}, engine.Group(""))
	var routes []string
	for _, route := range engine.Routes() {
		routes = append(routes, route.Method+" "+route.Path)
	}
	sort.Strings(routes)
	return routes
}

// Categories are shared by toolboxes, MCP servers and skills, so they live at /category
// rather than under the retired /operator prefix.
func TestCategoryRoutes(t *testing.T) {
	private := categoryRoutes((*categoryRestHandler).RegisterPrivate)
	wantPrivate := []string{"DELETE /category/:category_type", "GET /category", "POST /category", "PUT /category/:category_type"}
	if len(private) != len(wantPrivate) {
		t.Fatalf("private routes = %v, want %v", private, wantPrivate)
	}
	for i := range wantPrivate {
		if private[i] != wantPrivate[i] {
			t.Fatalf("private routes = %v, want %v", private, wantPrivate)
		}
	}
	public := categoryRoutes((*categoryRestHandler).RegisterPublic)
	if len(public) != 1 || public[0] != "GET /category" {
		t.Fatalf("public routes = %v, want [GET /category]", public)
	}
}
