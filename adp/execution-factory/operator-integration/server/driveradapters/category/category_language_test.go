package category

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"go.uber.org/mock/gomock"
)

func TestCategoryListMarksLocalizedCacheableResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	manager := mocks.NewMockCategoryManager(ctrl)
	manager.EXPECT().GetCategoryList(gomock.Any()).Return([]*interfaces.CategoryInfo{}, nil)
	handler := &categoryHandler{CategoryManager: manager}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	request := httptest.NewRequest(http.MethodGet, "/operator/category", nil)
	ctx.Request = request.WithContext(sharedrest.WithLanguage(request.Context(), sharedrest.AmericanEnglish))

	handler.CategoryList(ctx)

	if got := recorder.Header().Get(sharedrest.ContentLanguageHeader); got != sharedrest.AmericanEnglish {
		t.Fatalf("Content-Language = %q, want %q", got, sharedrest.AmericanEnglish)
	}
	if got := recorder.Header().Get("Vary"); got != sharedrest.AcceptLanguageHeader {
		t.Fatalf("Vary = %q, want %q", got, sharedrest.AcceptLanguageHeader)
	}
}
