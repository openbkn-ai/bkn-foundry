// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"testing"

	"github.com/gin-gonic/gin"
)

func TestProjectionGrantVerifierEnabled(t *testing.T) {
	for _, test := range []struct {
		name    string
		enabled string
		issuer  string
		aud     string
		keys    string
		want    bool
	}{
		{name: "all unset", want: false},
		{name: "unrelated projection config", issuer: "trace-core-projection", aud: "bkn-projection-read", keys: "key-1=base64", want: false},
		{name: "explicitly enabled", enabled: "true", want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("BKN_TRACE_PROJECTION_GRANT_ENABLED", test.enabled)
			t.Setenv("BKN_TRACE_PROJECTION_GRANT_ISSUER", test.issuer)
			t.Setenv("BKN_TRACE_PROJECTION_GRANT_AUDIENCE", test.aud)
			t.Setenv("BKN_TRACE_PROJECTION_GRANT_PUBLIC_KEYS", test.keys)
			if got := projectionGrantVerifierEnabled(); got != test.want {
				t.Fatalf("projectionGrantVerifierEnabled() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestProjectionReadRouteHasNoOntologyManagerAlias(t *testing.T) {
	test := setGinMode()
	defer test()
	engine := gin.New()
	(&restHandler{}).RegisterPublic(engine)
	for _, route := range engine.Routes() {
		if route.Path == "/api/ontology-manager/in/v1/trace/projection/knowledge-networks/:kn_id" {
			t.Fatal("projection reader must not have an ontology-manager alias")
		}
	}
}
