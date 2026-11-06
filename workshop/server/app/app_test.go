// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/workshop/server/common"
)

func TestRouteExtensionsFreezeInStableOrder(t *testing.T) {
	application := &Application{routeExtensions: make(map[string]func(*gin.Engine))}
	installed := make([]string, 0, 2)
	if err := application.RegisterRoutes("zeta", func(*gin.Engine) { installed = append(installed, "zeta") }); err != nil {
		t.Fatal(err)
	}
	if err := application.RegisterRoutes("alpha", func(*gin.Engine) { installed = append(installed, "alpha") }); err != nil {
		t.Fatal(err)
	}
	for _, install := range application.freezeRouteExtensions() {
		install(nil)
	}
	if want := []string{"alpha", "zeta"}; !reflect.DeepEqual(installed, want) {
		t.Fatalf("install order = %v, want %v", installed, want)
	}
	if err := application.RegisterRoutes("late", func(*gin.Engine) {}); err == nil {
		t.Fatal("RegisterRoutes after freeze succeeded")
	}
}

func TestRouteExtensionsRejectInvalidAndDuplicateRegistrations(t *testing.T) {
	application := &Application{routeExtensions: make(map[string]func(*gin.Engine))}
	if err := application.RegisterRoutes("", func(*gin.Engine) {}); err == nil {
		t.Fatal("RegisterRoutes accepted an empty name")
	}
	if err := application.RegisterRoutes("apps", nil); err == nil {
		t.Fatal("RegisterRoutes accepted a nil installer")
	}
	if err := application.RegisterRoutes("apps", func(*gin.Engine) {}); err != nil {
		t.Fatal(err)
	}
	if err := application.RegisterRoutes("apps", func(*gin.Engine) {}); err == nil {
		t.Fatal("RegisterRoutes accepted a duplicate name")
	}
}

type okPinger struct{}

func (okPinger) PingContext(context.Context) error { return nil }

// The community binary has no business routes: they answer gin's plain 404,
// which is what RequireEdition reproduces for an unlicensed paid binary.
func TestCommunityHandlerServesProbesAndHidesBusinessRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	application := &Application{appSetting: &common.AppSetting{}, routeExtensions: make(map[string]func(*gin.Engine))}
	application.db = nil
	handler := application.handlerWith(okPinger{})

	for path, want := range map[string]int{
		"/api/workshop/v1/health":    http.StatusOK,
		"/api/workshop/v1/ready":     http.StatusOK,
		"/api/workshop/in/v1/health": http.StatusOK,
		"/api/workshop/v1/apps":      http.StatusNotFound,
	} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != want {
			t.Errorf("GET %s = %d, want %d", path, w.Code, want)
		}
	}
}

func TestWorkersRunUntilStoppedAndRejectBadRegistrations(t *testing.T) {
	application := &Application{workers: make(map[string]func(context.Context))}
	if err := application.RegisterWorker("", func(context.Context) {}); err == nil {
		t.Fatal("RegisterWorker accepted an empty name")
	}
	if err := application.RegisterWorker("feed", nil); err == nil {
		t.Fatal("RegisterWorker accepted a nil function")
	}
	started := make(chan struct{})
	stopped := false
	if err := application.RegisterWorker("feed", func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		stopped = true
	}); err != nil {
		t.Fatal(err)
	}
	if err := application.RegisterWorker("feed", func(context.Context) {}); err == nil {
		t.Fatal("RegisterWorker accepted a duplicate name")
	}
	stop := application.startWorkers()
	<-started
	stop()
	if !stopped {
		t.Fatal("stop returned before the worker did")
	}
	application.extensionsFrozen = true
	if err := application.RegisterWorker("late", func(context.Context) {}); err == nil {
		t.Fatal("RegisterWorker after Run succeeded")
	}
}
