// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// vegaRawQueryAnswering returns a Vega client whose raw query endpoint answers
// with status and body.
func vegaRawQueryAnswering(status int, body string) *vegaAccess {
	return &vegaAccess{
		logger:  &mockLogger{},
		baseURL: "http://vega-backend:13014/api/vega-backend",
		httpClient: &mockHTTPClient{
			postNoUnmarshalFunc: func(_ context.Context, _ string, _ map[string]string, _ interface{}) (int, []byte, error) {
				return status, []byte(body), nil
			},
		},
	}
}

// vegaViewDetailDenied is Vega's answer when the caller holds no view_detail on
// a resource the statement references, as observed in #1543.
const vegaViewDetailDenied = `{"error_code":"Public.Forbidden","description":"没有执行此操作的权限。",` +
	`"solution":"请联系管理员授予所需权限。","error_link":"",` +
	`"error_details":"Access denied: insufficient permissions for[view_detail]"}`

// A caller authorized only through object types reaches run_sql's Vega
// view_detail check and is refused. The refusal must say that run_sql needs the
// resource grant itself and point at the object-query tools, instead of echoing
// Vega's body. See #1543.
func TestVegaRawQuery_ResourceDenialExplainsDirectPermission(t *testing.T) {
	for _, tc := range []struct {
		locale string
		want   []string
	}{
		{"en-US", []string{"view_detail", "knowledge-network authorization", "object types",
			"query_object_instance", "run_cypher"}},
		{"zh-CN", []string{"view_detail", "知识网络授权", "对象类", "query_object_instance", "run_cypher"}},
	} {
		t.Run(tc.locale, func(t *testing.T) {
			ctx := common.SetLanguageToCtx(context.Background(), tc.locale)
			client := vegaRawQueryAnswering(http.StatusForbidden, vegaViewDetailDenied)

			_, err := client.RawQuery(ctx, &interfaces.VegaRawQueryReq{
				Query: "SELECT * FROM {{.d9m3f2k2u8d3u773mgo0}} LIMIT 10", QueryFormat: "sql",
			})

			he := asHTTPError(t, err)
			if he.HTTPCode != http.StatusForbidden || he.Code != "Public.Forbidden" {
				t.Fatalf("error = %d %s, want 403 Public.Forbidden", he.HTTPCode, he.Code)
			}
			details, _ := he.ErrorDetails.(string)
			for _, want := range tc.want {
				if !strings.Contains(details, want) {
					t.Errorf("details = %q, want it to mention %q", details, want)
				}
			}
			for _, leak := range []string{"vega raw query failed", "Access denied", "error_code", "没有执行此操作的权限",
				"SELECT", "d9m3f2k2u8d3u773mgo0", "vega-backend"} {
				if strings.Contains(he.Error(), leak) {
					t.Errorf("error echoes downstream or request internals %q: %s", leak, he.Error())
				}
			}
		})
	}
}

// Only Vega's permission denial is reworded. Every other failure keeps the status
// and detail it had before #1543.
func TestVegaRawQuery_OtherFailuresKeepDownstreamDetail(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"sql rejected", http.StatusBadRequest,
			`{"error_code":"VegaBackend.Query.InvalidParameter","description":"参数错误",` +
				`"error_details":"raw query rejected by read-only policy"}`},
		{"resource not found", http.StatusNotFound,
			`{"error_code":"VegaBackend.Query.ResourceNotFound","error_details":"resource res_orders not found"}`},
		{"403 without the permission code", http.StatusForbidden,
			`{"error_code":"VegaBackend.Query.InvalidParameter","error_details":"cursor does not belong to the current account"}`},
		{"403 non-envelope body", http.StatusForbidden, `<html>403 Forbidden</html>`},
		{"server error", http.StatusInternalServerError,
			`{"error_code":"VegaBackend.Query.ExecuteFailed","error_details":"connection reset"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := vegaRawQueryAnswering(tc.status, tc.body)

			_, err := client.RawQuery(context.Background(), &interfaces.VegaRawQueryReq{})

			he := asHTTPError(t, err)
			if he.HTTPCode != tc.status {
				t.Fatalf("status = %d, want %d", he.HTTPCode, tc.status)
			}
			if want := "vega raw query failed: " + tc.body; he.ErrorDetails != want {
				t.Errorf("details = %#v, want %q", he.ErrorDetails, want)
			}
		})
	}
}

func TestVegaRawQueryPreservesLargeIntegersAsJSONNumber(t *testing.T) {
	client := &vegaAccess{
		logger:  &mockLogger{},
		baseURL: "http://vega.example.com",
		httpClient: &mockHTTPClient{
			postNoUnmarshalFunc: func(_ context.Context, _ string, _ map[string]string, _ interface{}) (int, []byte, error) {
				return 200, []byte(`{"entries":[{"id_card":110101199001152345,"small":42}]}`), nil
			},
		},
	}

	resp, err := client.RawQuery(context.Background(), &interfaces.VegaRawQueryReq{})
	if err != nil {
		t.Fatalf("RawQuery() error = %v", err)
	}

	large, ok := resp.Entries[0]["id_card"].(json.Number)
	if !ok {
		t.Fatalf("id_card type = %T, want json.Number", resp.Entries[0]["id_card"])
	}
	if got := large.String(); got != "110101199001152345" {
		t.Errorf("id_card = %s, want 110101199001152345", got)
	}
	if _, ok := resp.Entries[0]["small"].(json.Number); !ok {
		t.Errorf("small type = %T, want json.Number", resp.Entries[0]["small"])
	}
}
