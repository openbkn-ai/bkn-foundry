package app

import (
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/config"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/federation"
	"testing"
)

func TestIdentityExtensionAssembly(t *testing.T) {
	a := &App{cfg: &config.Config{}}
	var runtime federation.Runtime
	extension := &federation.Extension{Mount: func(*gin.Engine) {}}
	if err := a.RegisterIdentityProvider(func(r federation.Runtime) (*federation.Extension, error) { runtime = r; return extension, nil }); err != nil {
		t.Fatal(err)
	}
	if runtime.Redirect == nil || a.deps.IdentityProvider != extension {
		t.Fatal("missing runtime or extension")
	}
	if err := a.RegisterIdentityProvider(func(federation.Runtime) (*federation.Extension, error) {
		t.Fatal("duplicate factory executed")
		return nil, nil
	}); err == nil {
		t.Fatal("duplicate registration accepted")
	}
	for _, started := range []bool{false, true} {
		a := &App{cfg: &config.Config{}, started: started}
		err := a.RegisterIdentityProvider(func(federation.Runtime) (*federation.Extension, error) { return nil, errors.New("factory error") })
		if err == nil {
			t.Fatal("failed/late registration accepted")
		}
		if a.deps.IdentityProvider != nil {
			t.Fatal("failed registration installed extension")
		}
	}
}
