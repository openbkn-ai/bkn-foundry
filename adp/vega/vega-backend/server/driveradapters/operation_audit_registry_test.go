// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package driveradapters

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRegisteredOperationAudit(t *testing.T) {
	t.Run("includes only data resource management operations", func(t *testing.T) {
		for _, testCase := range []struct {
			method string
			path   string
			action string
			target string
		}{
			{"POST", "/api/vega-backend/v1/catalogs", "create", "catalog"},
			{"PUT", "/api/vega-backend/v1/catalogs/:id", "update", "catalog"},
			{"DELETE", "/api/vega-backend/v1/catalogs/:id", "delete", "catalog"},
			{"POST", "/api/vega-backend/v1/resources", "create", "resource"},
			{"PUT", "/api/vega-backend/v1/resources/:id", "update", "resource"},
			{"DELETE", "/api/vega-backend/v1/resources/:id", "delete", "resource"},
			{"POST", "/api/vega-backend/v1/discover-schedules", "create", "discover_schedule"},
			{"POST", "/api/vega-backend/v1/build-tasks", "create", "index_task"},
			{"POST", "/api/vega-backend/v1/build-tasks/:id/start", "start", "index_task"},
			{"POST", "/api/vega-backend/v1/build-tasks/:id/stop", "stop", "index_task"},
		} {
			rule, ok := registeredOperationAudit(testCase.method, testCase.path, "")
			assert.True(t, ok, testCase.path)
			assert.Equal(t, testCase.action, rule.Action, testCase.path)
			assert.Equal(t, testCase.target, rule.TargetType, testCase.path)
		}
	})

	t.Run("excludes runtime execution and data mutation endpoints", func(t *testing.T) {
		for _, testCase := range []struct {
			method string
			path   string
		}{
			{"POST", "/api/vega-backend/v1/catalogs/:id/test-connection"},
			{"POST", "/api/vega-backend/v1/catalogs/:id/discover"},
			{"POST", "/api/vega-backend/v1/resources/:id/data"},
			{"POST", "/api/vega-backend/v1/resources/query"},
		} {
			_, ok := registeredOperationAudit(testCase.method, testCase.path, "")
			assert.False(t, ok, testCase.path)
		}
	})
}
