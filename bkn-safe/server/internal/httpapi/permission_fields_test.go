// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/gin-gonic/gin"
	row "github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/rowfilter"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"net/http"
	"testing"
)

type permissionFieldSchema struct{ fail bool }

func (s permissionFieldSchema) ResolvePublishedObjectType(_ context.Context, _ string, ref string) (row.PublishedObjectType, error) {
	if s.fail {
		return row.PublishedObjectType{}, errors.New("schema unavailable")
	}
	return row.PublishedObjectType{ObjectTypeRef: ref, Published: true, Properties: map[string]row.PublishedDataProperty{"id": {DisplayName: "订单编号"}, "phone": {DisplayName: "联系电话"}}}, nil
}
func TestEffectiveFieldPermissionsIncludesUnconfiguredFieldsAndBaseClamp(t *testing.T) {
	_, e, db, _ := newAdminServer(t)
	seedCatalogOps(t, db, "object_type", "view_detail", "query_data")
	if err := db.Create(&model.User{ID: "no-access", Account: "no-access", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	g := r.Group("/api/safe/v1/admin", func(c *gin.Context) { c.Set(ctxAccessorID, adminSub) })
	registerEffectiveFieldPermissions(g, e, db, permissionFieldSchema{})
	for _, subject := range []struct{ id, level string }{{adminSub, "full"}, {"no-access", "none"}} {
		w := adminReq(t, r, http.MethodGet, "/api/safe/v1/admin/permission-projection/fields?accessor_type=user&accessor_id="+subject.id+"&object_type_ref=kn/orders", nil)
		if w.Code != 200 {
			t.Fatalf("fields = %d %s", w.Code, w.Body.String())
		}
		var body struct {
			Decisions []struct {
				Name, Level, Source string
				DisplayName         string `json:"display_name"`
			} `json:"decisions"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Decisions) != 2 {
			t.Fatalf("missing unconfigured fields: %s", w.Body.String())
		}
		for _, field := range body.Decisions {
			if field.Level != subject.level || field.DisplayName == "" {
				t.Fatalf("field = %+v", field)
			}
		}
	}
	bad := gin.New()
	registerEffectiveFieldPermissions(bad.Group("/api/safe/v1/admin", func(c *gin.Context) { c.Set(ctxAccessorID, adminSub) }), e, db, permissionFieldSchema{fail: true})
	if w := adminReq(t, bad, http.MethodGet, "/api/safe/v1/admin/permission-projection/fields?accessor_type=user&accessor_id="+adminSub+"&object_type_ref=kn/orders", nil); w.Code == 200 {
		t.Fatal("schema failure returned successful empty fields")
	}
}
