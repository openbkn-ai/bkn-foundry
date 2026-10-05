// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package app

import (
	"reflect"
	"testing"

	"github.com/gin-gonic/gin"
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
