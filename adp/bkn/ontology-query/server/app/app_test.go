// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package app

import (
	"context"
	"reflect"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

type testActionGate struct{}

func (testActionGate) Available() error { return nil }

func (testActionGate) ClaimActionPermit(context.Context, interfaces.ActionGovernanceClaim) error {
	return nil
}

func TestActionGovernanceGateInstallsBeforeRoutesFreeze(t *testing.T) {
	application := &Application{appSetting: &common.AppSetting{}, routeExtensions: make(map[string]func(*gin.Engine))}
	if err := application.InstallActionGovernanceGate(nil); err == nil {
		t.Fatal("nil governance gate accepted")
	}
	if err := application.InstallActionGovernanceGate(testActionGate{}); err != nil || application.restHandler == nil {
		t.Fatalf("gate installation failed: %v", err)
	}
	if err := application.InstallActionGovernanceGate(testActionGate{}); err == nil {
		t.Fatal("duplicate governance gate accepted")
	}
	application.freezeRouteExtensions()
	if err := application.InstallActionGovernanceGate(testActionGate{}); err == nil {
		t.Fatal("late governance gate accepted")
	}
}

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
	if err := application.RegisterRoutes("metrics", nil); err == nil {
		t.Fatal("RegisterRoutes accepted a nil installer")
	}
	if err := application.RegisterRoutes("metrics", func(*gin.Engine) {}); err != nil {
		t.Fatal(err)
	}
	if err := application.RegisterRoutes("metrics", func(*gin.Engine) {}); err == nil {
		t.Fatal("RegisterRoutes accepted a duplicate name")
	}
}
