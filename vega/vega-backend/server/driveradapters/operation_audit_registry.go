// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package driveradapters

import "strings"

// operationAuditRule describes a management request that is allowed to emit an
// operation audit fact. Runtime execution endpoints must not be added here.
type operationAuditRule struct {
	Action     string
	TargetType string
}

var operationAuditRoutes = map[string]operationAuditRule{
	"POST /catalogs":                          {Action: "create", TargetType: "catalog"},
	"PUT /catalogs/:id":                       {Action: "update", TargetType: "catalog"},
	"DELETE /catalogs/:id":                    {Action: "delete", TargetType: "catalog"},
	"POST /catalogs/:id/enable":               {Action: "enable", TargetType: "catalog"},
	"POST /catalogs/:id/disable":              {Action: "disable", TargetType: "catalog"},
	"PUT /catalogs/:id/health-check-schedule": {Action: "update", TargetType: "catalog_health_check_schedule"},
	"POST /resources":                         {Action: "create", TargetType: "resource"},
	"PUT /resources/:id":                      {Action: "update", TargetType: "resource"},
	"DELETE /resources/:id":                   {Action: "delete", TargetType: "resource"},
	"POST /discover-schedules":                {Action: "create", TargetType: "discover_schedule"},
	"PUT /discover-schedules/:id":             {Action: "update", TargetType: "discover_schedule"},
	"DELETE /discover-schedules/:id":          {Action: "delete", TargetType: "discover_schedule"},
	"POST /discover-schedules/:id/enable":     {Action: "enable", TargetType: "discover_schedule"},
	"POST /discover-schedules/:id/disable":    {Action: "disable", TargetType: "discover_schedule"},
	"POST /build-tasks":                       {Action: "create", TargetType: "index_task"},
	"DELETE /build-tasks/:ids":                {Action: "delete", TargetType: "index_task"},
	"POST /build-tasks/:id/start":             {Action: "start", TargetType: "index_task"},
	"POST /build-tasks/:id/stop":              {Action: "stop", TargetType: "index_task"},
}

func registeredOperationAudit(method, fullPath, methodOverride string) (operationAuditRule, bool) {
	if strings.EqualFold(strings.TrimSpace(methodOverride), "GET") {
		return operationAuditRule{}, false
	}
	path := strings.TrimSpace(fullPath)
	for _, prefix := range []string{
		"/api/vega-backend/v1", "/api/vega-backend/in/v1",
	} {
		if strings.HasPrefix(path, prefix) {
			path = strings.TrimPrefix(path, prefix)
			break
		}
	}
	rule, ok := operationAuditRoutes[strings.ToUpper(strings.TrimSpace(method))+" "+path]
	return rule, ok
}
