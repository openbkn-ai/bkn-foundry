// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package vega_backend

import (
	"context"
	"net/http"
	"strings"
	"testing"

	rmock "github.com/openbkn-ai/bkn-foundry/comm-go/rest/mock"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

func draftPin(t *testing.T) *interfaces.KNVersionPin {
	t.Helper()
	pin, err := interfaces.NewKNVersionPin("draft", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := pin.Observe("draft", "7"); err != nil {
		t.Fatal(err)
	}
	return pin
}

func draftContext(t *testing.T) context.Context {
	t.Helper()
	ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY,
		interfaces.AccountInfo{ID: "user-1", Type: "user"})
	return interfaces.WithKNVersionPin(ctx, draftPin(t))
}

func TestVegaBackendAccessDraftReadsDataAsTheCaller(t *testing.T) {
	httpClient := rmock.NewMockHTTPClient(gomock.NewController(t))
	httpClient.EXPECT().PostNoUnmarshal(gomock.Any(), "http://vega/resources/resource-1/data", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, headers map[string]string, _ any) (int, []byte, error) {
			if headers[interfaces.HTTP_HEADER_ACCOUNT_ID] != "user-1" || headers[interfaces.HTTP_HEADER_ACCOUNT_TYPE] != "user" ||
				headers[interfaces.HTTP_HEADER_METHOD_OVERRIDE] != http.MethodGet {
				t.Fatalf("unexpected caller headers: %#v", headers)
			}
			for name := range headers {
				if strings.HasPrefix(strings.ToLower(name), "x-bkn-") {
					t.Fatalf("a caller read must not carry proxy header %s", name)
				}
			}
			return http.StatusOK, []byte(`{"entries":[]}`), nil
		})
	access := &vegaBackendAccess{baseURL: "http://vega", httpClient: httpClient}
	if _, err := access.QueryResourceData(draftContext(t), "resource-1", nil); err != nil {
		t.Fatal(err)
	}
}

func TestVegaBackendAccessDraftReadsSchemaAsTheCaller(t *testing.T) {
	httpClient := rmock.NewMockHTTPClient(gomock.NewController(t))
	httpClient.EXPECT().GetNoUnmarshal(gomock.Any(), "http://vega/resources/resource-1/schema?operation=view_detail", nil, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _ any, headers map[string]string) (int, []byte, error) {
			if headers[interfaces.HTTP_HEADER_ACCOUNT_ID] != "user-1" {
				t.Fatalf("unexpected caller headers: %#v", headers)
			}
			return http.StatusOK, []byte(`{"schema_definition":[{"name":"id","type":"string"}]}`), nil
		})
	access := &vegaBackendAccess{baseURL: "http://vega", httpClient: httpClient}
	if got, err := access.GetResourceSchema(draftContext(t), "resource-1"); err != nil || len(got.SchemaDefinition) != 1 {
		t.Fatalf("GetResourceSchema() = %#v, %v", got, err)
	}
}

func TestVegaBackendAccessProxyContextWinsOverTheDraftPin(t *testing.T) {
	// A read that already carries a proxy context is never downgraded to the caller path.
	ctx := interfaces.WithKNVersionPin(trustedResourceProxyContext(context.Background(), "resource-1"), draftPin(t))
	if readsAsCaller(ctx) {
		t.Fatal("a proxy context must keep the read on the proxy path")
	}
}

func TestVegaBackendAccessRunningVersionStillNeedsTheProxy(t *testing.T) {
	access := &vegaBackendAccess{baseURL: "http://vega"}
	pin, _ := interfaces.NewKNVersionPin("", "")
	_ = pin.Observe("3", "")
	ctx := interfaces.WithKNVersionPin(context.Background(), pin)
	if _, err := access.QueryResourceData(ctx, "resource-1", nil); err == nil {
		t.Fatal("the running version must fail closed without a proxy context")
	}
}
