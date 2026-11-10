// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	bmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
)

// revisionsStub answers DefinitionRevisions only; the list handler needs nothing else.
type revisionsStub struct {
	interfaces.KNVersionAccess
	revisions map[string]string
}

func (s revisionsStub) DefinitionRevisions(context.Context, string, string, string, []string) (map[string]string, error) {
	return s.revisions, nil
}

// A list response carries each entry's revision but no ETag, even when it has one entry:
// If-Match belongs to reads of one definition.
func TestListObjectTypesCarriesRevisionsButNoETag(t *testing.T) {
	restore := gin.Mode()
	gin.SetMode(gin.TestMode)
	defer gin.SetMode(restore)
	oldKNVA, oldBC := logics.KNVA, logics.BC
	logics.SetKNVersionAccess(revisionsStub{revisions: map[string]string{"ot1": "rev-1"}}, oldBC)
	defer logics.SetKNVersionAccess(oldKNVA, oldBC)

	ctrl := gomock.NewController(t)
	as := bmock.NewMockAuthService(ctrl)
	ots := bmock.NewMockObjectTypeService(ctrl)
	kns := bmock.NewMockKNService(ctrl)
	engine := gin.New()
	MockNewObjectTypeRestHandler(&common.AppSetting{}, as, ots, bmock.NewMockRelationTypeService(ctrl),
		bmock.NewMockActionTypeService(ctrl), kns).RegisterPublic(engine)

	as.EXPECT().VerifyToken(gomock.Any(), gomock.Any()).Return(hydra.Visitor{}, nil).AnyTimes()
	kns.EXPECT().CheckKNExistByID(gomock.Any(), "kn1", gomock.Any()).Return("kn1", true, nil)
	one := &interfaces.ObjectType{}
	one.OTID = "ot1"
	ots.EXPECT().ListObjectTypeSummaries(gomock.Any(), gomock.Any(), gomock.Any()).
		Return([]*interfaces.ObjectType{one}, 1, nil)

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/bkn-backend/v1/knowledge-networks/kn1/object-types", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
	}
	if etag := w.Header().Get("ETag"); etag != "" {
		t.Fatalf("list response ETag = %q, want none", etag)
	}
	if !strings.Contains(w.Body.String(), `"revision":"rev-1"`) {
		t.Fatalf("body %s does not carry the entry's revision", w.Body.String())
	}
}
