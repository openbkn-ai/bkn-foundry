// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var objectMetricEnUS = i18n.I18nMessages{
	Locale: "en-US",
	Messages: map[string]string{
		"BknBackend.ObjectMetric.InvalidDefinition": "The object metric definition is invalid. Correct the field-level validation issues and retry.",
		"BknBackend.ObjectMetric.NotFound":          "Object metric not found. Verify the metric ID, version, and branch.",
		"BknBackend.ObjectMetric.DuplicateCode":     "Object metric code already exists. Use a unique stable code within the knowledge network.",
		"BknBackend.ObjectMetric.ImmutableVersion":  "The object metric version is immutable. Create a new draft version instead of editing a published version.",
		"BknBackend.ObjectMetric.DependencyInvalid": "The object metric dependency is invalid. Review pinned versions, object alignment, time windows, and dependency cycles.",
		"BknBackend.ObjectMetric.DeleteBlocked":     "The object metric version is still referenced. Remove or upgrade every metric and logical-property reference before deleting this version.",
		"BknBackend.ObjectMetric.InternalError":     "Object metric service error. Retry later or contact an administrator if the problem persists.",
	},
}
