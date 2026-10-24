package category

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
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
	ctx.Request = request.WithContext(i18n.WithLanguage(request.Context(), i18n.AmericanEnglish))

	handler.CategoryList(ctx)

	if got := recorder.Header().Get(i18n.ContentLanguageHeader); got != string(i18n.AmericanEnglish) {
		t.Fatalf("Content-Language = %q, want %q", got, i18n.AmericanEnglish)
	}
	if got := recorder.Header().Get("Vary"); got != i18n.AcceptLanguageHeader {
		t.Fatalf("Vary = %q, want %q", got, i18n.AcceptLanguageHeader)
	}
}
