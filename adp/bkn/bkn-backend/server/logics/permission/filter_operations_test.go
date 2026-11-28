// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package permission

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	. "github.com/smartystreets/goconvey/convey"
	"go.opentelemetry.io/otel/attribute"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/global"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	padapter "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/drivenadapters/permission"
	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// Test_PermissionServiceImpl_FilterResources_FullOperationSet wires the service
// to the real bkn-safe adapter against a stub bkn-safe, and asserts the whole
// chain the knowledge-network detail responses depend on:
//
//   - no locally maintained operation set is sent, so bkn-safe uses its catalog;
//   - operation projection is explicitly enabled;
//   - view_detail, modify and delete come back together for an authorized
//     resource, which is what Studio reads to decide whether to show the
//     edit/delete entry points;
//   - the page costs one request, not one per resource per operation.
func Test_PermissionServiceImpl_FilterResources_FullOperationSet(t *testing.T) {
	Convey("Test FilterResources returns the full allowed operation set\n", t, func() {
		// Assertions stay out of the handler: goconvey's So panics when called
		// off the Convey goroutine, which would surface as a connection error
		// rather than a test failure.
		var requests []map[string]any
		var paths []string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			paths = append(paths, r.URL.Path)
			body, _ := io.ReadAll(r.Body)
			var req map[string]any
			_ = json.Unmarshal(body, &req)
			requests = append(requests, req)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"resources": []map[string]any{
					{
						"resource_type": interfaces.RESOURCE_TYPE_KN,
						"resource_id":   "kn1",
						"operations":    []string{"view_detail", "modify", "delete"},
					},
				},
			})
		}))
		defer srv.Close()

		svc := &PermissionServiceImpl{
			appSetting: &common.AppSetting{},
			mqClient:   &mockMQClient{},
			pa:         padapter.NewPermissionAccess(srv.URL),
		}

		ctx := withAccountInfo(context.Background(), "u1", "user")
		result, err := svc.FilterVisibleResourcesWithOperations(ctx, interfaces.RESOURCE_TYPE_KN, []string{"kn1", "kn2"},
			[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL})

		So(err, ShouldBeNil)
		So(paths, ShouldResemble, []string{"/api/safe/v1/authz/resource-filter"})
		So(len(requests), ShouldEqual, 1)

		So(requests[0]["include_operations"], ShouldEqual, true)
		visibility, _ := requests[0]["visibility_operations"].([]any)
		So(len(visibility), ShouldEqual, 1)

		// kn2 is absent from the reply: not visible, so it is filtered out.
		So(len(result), ShouldEqual, 1)
		So(result["kn1"].Operations, ShouldResemble, []string{"view_detail", "modify", "delete"})
	})
}

func TestFilterResourcesLogsSafeFailureCause(t *testing.T) {
	previous := global.GetLoggerProvider()
	capture := &filterLogExporter{}
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(capture)))
	global.SetLoggerProvider(provider)
	t.Cleanup(func() {
		global.SetLoggerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})

	const privateResponse = "private downstream response"
	const privateURL = "http://authz.example.invalid"
	for _, tc := range []struct {
		name      string
		transport error
		status    int
		truncated bool
		wantCause string
	}{
		{name: "HTTP failure", status: http.StatusServiceUnavailable, wantCause: "bkn-safe returned HTTP 503"},
		{name: "HTTP failure with truncated body", status: http.StatusServiceUnavailable, truncated: true, wantCause: "bkn-safe returned HTTP 503"},
		{name: "HTTP 200 with truncated body", status: http.StatusOK, truncated: true, wantCause: "bkn-safe response body truncated"},
		{name: "truncated response headers", transport: io.ErrUnexpectedEOF, wantCause: "bkn-safe transport failure: unavailable"},
		{name: "connection refused", transport: syscall.ECONNREFUSED, wantCause: "bkn-safe connection refused"},
		{name: "timeout", transport: context.DeadlineExceeded, wantCause: "bkn-safe transport failure: timeout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capture.records = nil
			svc, _, mock, _ := newTestPermissionImpl(t)
			if tc.transport == nil {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if tc.truncated {
						w.Header().Set("Content-Length", strconv.Itoa(len(privateResponse)+1))
					}
					w.WriteHeader(tc.status)
					_, _ = io.WriteString(w, privateResponse)
				}))
				t.Cleanup(server.Close)
				svc.pa = padapter.NewPermissionAccess(server.URL)
			} else {
				mock.EXPECT().FilterResources(gomock.Any(), gomock.Any()).Return(nil, &url.Error{
					Op: "Post", URL: privateURL, Err: tc.transport,
				})
			}

			ctx := withAccountInfo(t.Context(), "u1", "user")
			result, err := svc.FilterVisibleResources(ctx, interfaces.RESOURCE_TYPE_KN, []string{"kn1"},
				[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL})
			var httpErr *httperrors.HTTPError
			if result != nil || !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusInternalServerError ||
				httpErr.ErrorCode != berrors.BknBackend_InternalError_FilterResourcesFailed {
				t.Fatalf("unexpected filter result: %v, %v", result, err)
			}
			response := httptest.NewRecorder()
			ginContext, _ := gin.CreateTestContext(response)
			ginContext.Request = httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
			rest.ReplyError(ginContext, httpErr)
			var publicError struct {
				Code        string         `json:"error_code"`
				Description string         `json:"description"`
				Details     map[string]any `json:"error_details"`
			}
			decodeErr := json.Unmarshal(response.Body.Bytes(), &publicError)
			if decodeErr != nil || response.Code != http.StatusInternalServerError ||
				publicError.Code != httpErr.ErrorCode || publicError.Description == "" ||
				publicError.Details == nil || len(publicError.Details) != 0 {
				t.Errorf("unexpected public error response: %d %s (%v)", response.Code, response.Body, decodeErr)
			}
			if len(capture.records) != 1 || capture.records[0].Severity() != otellog.SeverityError {
				t.Fatalf("expected one error log, got %v", capture.records)
			}
			attrs := make(map[string]string)
			capture.records[0].WalkAttributes(func(attr attribute.KeyValue) bool {
				attrs[string(attr.Key)] = attr.Value.AsString()
				return true
			})
			if attrs["cause"] != tc.wantCause || !strings.Contains(attrs["error.message"], httpErr.ErrorCode) {
				t.Errorf("unexpected log attributes: %v", attrs)
			}
			for _, value := range attrs {
				if strings.Contains(value, privateURL) || strings.Contains(value, privateResponse) {
					t.Errorf("log exposed private dependency data: %q", value)
				}
			}
		})
	}
}

type filterLogExporter struct {
	records []sdklog.Record
}

func (e *filterLogExporter) Export(_ context.Context, records []sdklog.Record) error {
	for _, record := range records {
		e.records = append(e.records, record.Clone())
	}
	return nil
}

func (*filterLogExporter) Shutdown(context.Context) error   { return nil }
func (*filterLogExporter) ForceFlush(context.Context) error { return nil }
