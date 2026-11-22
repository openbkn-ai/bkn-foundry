// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

func observationTestForwarder() *forwarder {
	return &forwarder{
		pool: &clientPool{
			logger:      logger.DefaultLogger(),
			clients:     make(map[clientKey]*ProxyClient),
			config:      PoolConfig{MaxClients: 4, MaxTimeout: 5 * time.Second, DefaultTimeout: 5 * time.Second},
			stopCleanup: make(chan struct{}),
		},
		logger: logger.DefaultLogger(),
	}
}

func TestForwardReturnsSanitizedRequestObservation(t *testing.T) {
	Convey("Forward returns transport-level request facts without sensitive values", t, func() {
		captured := struct {
			path          string
			queryKeys     []string
			contentLength int64
		}{}
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			captured.path = r.URL.EscapedPath()
			for key := range r.URL.Query() {
				captured.queryKeys = append(captured.queryKeys, key)
			}
			captured.contentLength = r.ContentLength
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))
		defer target.Close()

		forwarder := observationTestForwarder()
		resp, err := forwarder.Forward(context.Background(), &interfaces.HTTPRequest{
			Timeout: 5 * time.Second,
			HTTPRouter: interfaces.HTTPRouter{
				Method: http.MethodGet,
				URL:    target.URL + "/echo/{qa_path}",
			},
			HTTPRequestParams: interfaces.HTTPRequestParams{
				PathParams: map[string]string{"qa_path": "A+B space"},
				QueryParams: map[string]any{
					"qa_probe":  0,
					"zb_second": "SECRET-QUERY-VALUE",
				},
				Headers: map[string]any{"X-Secret-Token": "secret-token-value"},
				Body:    map[string]any{"qa_count": 0, "qa_payload": "SECRET-BODY-VALUE"},
			},
		})

		So(err, ShouldBeNil)
		So(resp, ShouldNotBeNil)
		So(resp.RequestObservation, ShouldNotBeNil)
		observation := resp.RequestObservation
		So(observation.Method, ShouldEqual, http.MethodGet)
		So(observation.Path, ShouldEqual, captured.path)
		So(observation.QueryKeys, ShouldResemble, []string{"qa_probe", "zb_second"})
		So(observation.ContentType, ShouldEqual, "application/json")
		So(observation.ContentLength, ShouldEqual, captured.contentLength)
		So(observation.ContentLength, ShouldBeGreaterThan, 0)
		So(observation.Redirected, ShouldBeFalse)

		// The observation must never leak header values, query values or the body.
		encoded, err := json.Marshal(observation)
		So(err, ShouldBeNil)
		for _, secret := range []string{"secret-token-value", "SECRET-QUERY-VALUE", "SECRET-BODY-VALUE", "qa_payload", "X-Secret-Token"} {
			So(string(encoded), ShouldNotContainSubstring, secret)
		}
	})
}

func TestForwardObservationFollowsRedirects(t *testing.T) {
	Convey("buildRequestObservation records the final request when redirects are followed", t, func() {
		original, err := http.NewRequest(http.MethodPost, "http://upstream.example/start?keep=1", nil)
		So(err, ShouldBeNil)
		final, err := http.NewRequest(http.MethodGet, "http://upstream.example/final?keep=1", nil)
		So(err, ShouldBeNil)

		observation := buildRequestObservation(original, final)
		So(observation, ShouldNotBeNil)
		So(observation.Method, ShouldEqual, http.MethodGet)
		So(observation.Path, ShouldEqual, "/final")
		So(observation.QueryKeys, ShouldResemble, []string{"keep"})
		So(observation.Redirected, ShouldBeTrue)
	})
}

func TestForwardObservationDoesNotClaimRedirectWhenPolicyReturnsRedirectResponse(t *testing.T) {
	Convey("The raw client returns redirect responses without following them", t, func() {
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/start":
				http.Redirect(w, r, "/final", http.StatusFound)
			default:
				_, _ = w.Write([]byte(`{"ok":true}`))
			}
		}))
		defer target.Close()

		forwarder := observationTestForwarder()
		resp, err := forwarder.Forward(context.Background(), &interfaces.HTTPRequest{
			Timeout: 5 * time.Second,
			HTTPRouter: interfaces.HTTPRouter{
				Method: http.MethodGet,
				URL:    target.URL + "/start",
			},
		})

		So(err, ShouldBeNil)
		So(resp, ShouldNotBeNil)
		So(resp.RequestObservation, ShouldNotBeNil)
		So(resp.StatusCode, ShouldEqual, http.StatusFound)
		So(resp.RequestObservation.Method, ShouldEqual, http.MethodGet)
		So(resp.RequestObservation.Path, ShouldEqual, "/start")
		So(resp.RequestObservation.Redirected, ShouldBeFalse)
	})
}

func TestForwardAttachesObservationOnTransportFailure(t *testing.T) {
	Convey("Forward still describes the attempted request when the upstream is unreachable", t, func() {
		forwarder := observationTestForwarder()
		resp, err := forwarder.Forward(context.Background(), &interfaces.HTTPRequest{
			Timeout: 2 * time.Second,
			HTTPRouter: interfaces.HTTPRouter{
				Method: http.MethodPost,
				URL:    "http://127.0.0.1:1/unreachable",
			},
		})

		So(err, ShouldBeNil)
		So(resp, ShouldNotBeNil)
		So(resp.StatusCode, ShouldBeIn, []int{http.StatusInternalServerError, http.StatusGatewayTimeout})
		So(resp.RequestObservation, ShouldNotBeNil)
		So(resp.RequestObservation.Method, ShouldEqual, http.MethodPost)
		So(resp.RequestObservation.Path, ShouldEqual, "/unreachable")
		So(resp.RequestObservation.Redirected, ShouldBeFalse)
	})
}
