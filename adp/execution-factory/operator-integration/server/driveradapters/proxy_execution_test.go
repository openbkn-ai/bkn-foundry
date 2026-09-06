// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

type recordingProxyExecutionAudit struct {
	events []interfaces.ProxyExecutionAuditEvent
}

func (r *recordingProxyExecutionAudit) RecordProxyExecution(
	_ context.Context, event interfaces.ProxyExecutionAuditEvent,
) {
	r.events = append(r.events, event)
}

func addValidProxyExecutionHeaders(request *http.Request, targetType, targetID, childType string) {
	request.Header.Set(string(interfaces.HeaderXAccountID), "proxy-1")
	request.Header.Set(string(interfaces.HeaderXAccountType), interfaces.ProxyAccountTypeApp)
	request.Header.Set(interfaces.HTTPHeaderBKNCallerID, "caller-1")
	request.Header.Set(interfaces.HTTPHeaderBKNCallerType, "user")
	request.Header.Set(interfaces.HTTPHeaderBKNKnowledgeID, "kn-1")
	request.Header.Set(interfaces.HTTPHeaderBKNChildType, childType)
	request.Header.Set(interfaces.HTTPHeaderBKNChildID, "action-1")
	request.Header.Set(interfaces.HTTPHeaderBKNProxyVersion, "3")
	request.Header.Set(interfaces.HTTPHeaderBKNTargetType, targetType)
	request.Header.Set(interfaces.HTTPHeaderBKNTargetID, targetID)
	request.Header.Set(interfaces.HTTPHeaderBKNOperation, interfaces.ProxyOperationExecute)
	request.Header.Set(interfaces.HTTPHeaderBKNExecutionID, "execution-1")
	request.Header.Set(common.HeaderBKNRequestID, "req_12345678")
}

func proxyExecutionTestEngine(
	recorder interfaces.ProxyExecutionAuditRecorder,
	handler gin.HandlerFunc,
) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(middlewareTraceContext, managedProxyExecutionBoundary(recorder))
	engine.POST("/api/agent-operator-integration/internal-v1/tool-box/:box_id/proxy/:tool_id", handler)
	engine.POST("/api/agent-operator-integration/internal-v1/mcp/proxy/:mcp_id/tool/call", handler)
	engine.POST("/api/agent-operator-integration/internal-v1/tool-box/:box_id/tool/:tool_id/debug", handler)
	return engine
}

func TestManagedProxyExecutionAllowsOnlyExactExecutionRoutes(t *testing.T) {
	t.Run("Tool execution", func(t *testing.T) {
		recorder := &recordingProxyExecutionAudit{}
		handlerCalls := 0
		engine := proxyExecutionTestEngine(recorder, func(c *gin.Context) {
			handlerCalls++
			if c.GetHeader("Authorization") != "" || c.GetHeader("X-Authorization") != "" ||
				c.GetHeader("X-Api-Key") != "" {
				t.Error("platform credentials reached the execution handler")
			}
			proxy, ok := interfaces.ProxyExecutionContextFromContext(c.Request.Context())
			if !ok || proxy.ExecutionID != "execution-1" || proxy.TargetID != "box-1" {
				t.Errorf("proxy context = %+v, %v", proxy, ok)
			}
			c.Status(http.StatusOK)
		})
		request := httptest.NewRequest(http.MethodPost,
			"/api/agent-operator-integration/internal-v1/tool-box/box-1/proxy/tool-1", nil)
		addValidProxyExecutionHeaders(request, interfaces.ProxyTargetTypeToolBox, "box-1", interfaces.ProxyChildTypeAction)
		request.Header.Set("Authorization", "Bearer caller-oauth")
		request.Header.Set("X-Authorization", "caller-oauth")
		request.Header.Set("X-Api-Key", "caller-app-key")
		response := httptest.NewRecorder()

		engine.ServeHTTP(response, request)

		if response.Code != http.StatusOK || handlerCalls != 1 {
			t.Fatalf("status=%d handler=%d", response.Code, handlerCalls)
		}
		if len(recorder.events) != 0 {
			t.Fatalf("route validation emitted final audit events: %+v", recorder.events)
		}
	})

	t.Run("MCP execution", func(t *testing.T) {
		handlerCalls := 0
		engine := proxyExecutionTestEngine(nil, func(c *gin.Context) {
			handlerCalls++
			c.Status(http.StatusOK)
		})
		request := httptest.NewRequest(http.MethodPost,
			"/api/agent-operator-integration/internal-v1/mcp/proxy/mcp-1/tool/call", nil)
		addValidProxyExecutionHeaders(request, interfaces.ProxyTargetTypeMCP, "mcp-1", interfaces.ProxyChildTypeAction)
		response := httptest.NewRecorder()

		engine.ServeHTTP(response, request)

		if response.Code != http.StatusOK || handlerCalls != 1 {
			t.Fatalf("status=%d handler=%d", response.Code, handlerCalls)
		}
	})

	t.Run("logical property Tool execution", func(t *testing.T) {
		handlerCalls := 0
		engine := proxyExecutionTestEngine(nil, func(c *gin.Context) {
			handlerCalls++
			c.Status(http.StatusOK)
		})
		request := httptest.NewRequest(http.MethodPost,
			"/api/agent-operator-integration/internal-v1/tool-box/box-1/proxy/tool-1", nil)
		addValidProxyExecutionHeaders(request, interfaces.ProxyTargetTypeToolBox, "box-1", interfaces.ProxyChildTypeLogic)
		request.Header.Del(interfaces.HTTPHeaderBKNExecutionID)
		response := httptest.NewRecorder()

		engine.ServeHTTP(response, request)

		if response.Code != http.StatusOK || handlerCalls != 1 {
			t.Fatalf("status=%d handler=%d", response.Code, handlerCalls)
		}
	})
}

func TestManagedProxyExecutionRejectsInvalidContextBeforeHandler(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		configure func(*http.Request)
	}{
		{
			name: "target tamper",
			path: "/api/agent-operator-integration/internal-v1/tool-box/box-2/proxy/tool-1",
			configure: func(request *http.Request) {
				addValidProxyExecutionHeaders(request, interfaces.ProxyTargetTypeToolBox, "box-1", interfaces.ProxyChildTypeAction)
			},
		},
		{
			name: "debug route",
			path: "/api/agent-operator-integration/internal-v1/tool-box/box-1/tool/tool-1/debug",
			configure: func(request *http.Request) {
				addValidProxyExecutionHeaders(request, interfaces.ProxyTargetTypeToolBox, "box-1", interfaces.ProxyChildTypeAction)
			},
		},
		{
			name: "missing execution id",
			path: "/api/agent-operator-integration/internal-v1/tool-box/box-1/proxy/tool-1",
			configure: func(request *http.Request) {
				addValidProxyExecutionHeaders(request, interfaces.ProxyTargetTypeToolBox, "box-1", interfaces.ProxyChildTypeAction)
				request.Header.Del(interfaces.HTTPHeaderBKNExecutionID)
			},
		},
		{
			name: "invalid mapping version",
			path: "/api/agent-operator-integration/internal-v1/mcp/proxy/mcp-1/tool/call",
			configure: func(request *http.Request) {
				addValidProxyExecutionHeaders(request, interfaces.ProxyTargetTypeMCP, "mcp-1", interfaces.ProxyChildTypeAction)
				request.Header.Set(interfaces.HTTPHeaderBKNProxyVersion, "0")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := &recordingProxyExecutionAudit{}
			handlerCalls := 0
			engine := proxyExecutionTestEngine(recorder, func(c *gin.Context) {
				handlerCalls++
				c.Status(http.StatusOK)
			})
			request := httptest.NewRequest(http.MethodPost, test.path, nil)
			test.configure(request)
			response := httptest.NewRecorder()

			engine.ServeHTTP(response, request)

			if response.Code != http.StatusForbidden || handlerCalls != 0 {
				t.Fatalf("status=%d handler=%d", response.Code, handlerCalls)
			}
			if len(recorder.events) != 1 || recorder.events[0].Decision != "deny" ||
				recorder.events[0].Reason != "invalid_trusted_context" {
				t.Fatalf("audit events = %+v", recorder.events)
			}
		})
	}
}

func TestDirectExecutionAndPublicRoutesRemainUnchanged(t *testing.T) {
	t.Run("internal direct execution without proxy context", func(t *testing.T) {
		engine := proxyExecutionTestEngine(nil, func(c *gin.Context) {
			if c.GetHeader("Authorization") != "Bearer direct-caller" {
				t.Errorf("Authorization = %q", c.GetHeader("Authorization"))
			}
			if _, ok := interfaces.ProxyExecutionContextFromContext(c.Request.Context()); ok {
				t.Error("direct call received a proxy execution context")
			}
			c.Status(http.StatusOK)
		})
		request := httptest.NewRequest(http.MethodPost,
			"/api/agent-operator-integration/internal-v1/tool-box/box-1/proxy/tool-1", nil)
		request.Header.Set("Authorization", "Bearer direct-caller")
		response := httptest.NewRecorder()

		engine.ServeHTTP(response, request)

		if response.Code != http.StatusOK {
			t.Fatalf("status=%d", response.Code)
		}
	})

	t.Run("public routes strip forged proxy context", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		engine := gin.New()
		engine.Use(stripProxyExecutionHeaders())
		engine.POST("/api/agent-operator-integration/v1/tool-box/:box_id/proxy/:tool_id", func(c *gin.Context) {
			for _, header := range trustedProxyExecutionHeaders {
				if c.GetHeader(header) != "" {
					t.Errorf("forged header %s reached public handler", header)
				}
			}
			if c.GetHeader("Authorization") != "Bearer direct-caller" {
				t.Errorf("Authorization = %q", c.GetHeader("Authorization"))
			}
			c.Status(http.StatusOK)
		})
		request := httptest.NewRequest(http.MethodPost,
			"/api/agent-operator-integration/v1/tool-box/box-1/proxy/tool-1", nil)
		addValidProxyExecutionHeaders(request, interfaces.ProxyTargetTypeToolBox, "box-1", interfaces.ProxyChildTypeAction)
		request.Header.Set("Authorization", "Bearer direct-caller")
		response := httptest.NewRecorder()

		engine.ServeHTTP(response, request)

		if response.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", response.Code)
		}
	})
}
