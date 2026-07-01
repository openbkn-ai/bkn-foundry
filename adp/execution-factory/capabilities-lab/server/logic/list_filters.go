// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logic

import (
	"strings"

	"github.com/openbkn-ai/adp/execution-factory/capabilities-lab/server/model"
)

func filterCapabilitiesByStatus(items []model.Capability, status string) []model.Capability {
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" || status == "all" {
		return items
	}

	filtered := make([]model.Capability, 0, len(items))
	for _, item := range items {
		if capabilityStatusMatches(item.Status, status) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func capabilityStatusMatches(actual, expected string) bool {
	actual = normalizeCapabilityStatus(actual)
	expected = normalizeCapabilityStatus(expected)
	return actual == expected
}

func normalizeCapabilityStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "published", "publish", "enabled":
		return "published"
	case "offline", "disabled", "unpublish", "unpublished", "draft":
		return "draft"
	default:
		return strings.ToLower(strings.TrimSpace(status))
	}
}

func applyStatusFilterResponse(resp *model.CapabilityListResponse, status string) *model.CapabilityListResponse {
	if resp == nil || strings.TrimSpace(status) == "" || strings.EqualFold(status, "all") {
		return resp
	}

	filtered := filterCapabilitiesByStatus(resp.Data, status)
	return &model.CapabilityListResponse{
		Data:     filtered,
		Total:    len(filtered),
		Page:     resp.Page,
		PageSize: resp.PageSize,
	}
}
