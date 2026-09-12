// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0. See the LICENSE file in the
// project root for details.

package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
)

type probeRouter struct{}

func (probeRouter) RegisterRouter(group *gin.RouterGroup) {
	group.GET("/probe", func(c *gin.Context) { c.Status(http.StatusNoContent) })
}

func TestValidatePortsRejectsUnsafeSandboxListener(t *testing.T) {
	tests := []struct {
		name        string
		sandboxPort int
		wantError   bool
	}{
		{name: "dedicated port", sandboxPort: 30780},
		{name: "same as trusted port", sandboxPort: 30779, wantError: true},
		{name: "disabled port", sandboxPort: 0, wantError: true},
		{name: "port above range", sandboxPort: 65536, wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validatePorts(config.Project{Port: 30779, SandboxPort: test.sandboxPort})
			if (err != nil) != test.wantError {
				t.Fatalf("validatePorts() error = %v, wantError = %v", err, test.wantError)
			}
		})
	}
}

func TestSandboxEngineOmitsTrustedInternalRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	server := &Server{
		httpHealthHandler:  probeRouter{},
		restPublicHandler:  probeRouter{},
		restPrivateHandler: probeRouter{},
	}

	tests := []struct {
		name       string
		engine     *gin.Engine
		path       string
		wantStatus int
	}{
		{
			name:       "platform exposes trusted internal route",
			engine:     server.platformEngine(),
			path:       "/api/agent-retrieval/in/v1/probe",
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "sandbox exposes authenticated public route",
			engine:     server.sandboxEngine(),
			path:       "/api/agent-retrieval/v1/probe",
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "sandbox rejects trusted internal route",
			engine:     server.sandboxEngine(),
			path:       "/api/agent-retrieval/in/v1/probe",
			wantStatus: http.StatusNotFound,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			test.engine.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
		})
	}
}
