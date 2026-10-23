// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var capabilityBindingEnUS = i18n.I18nMessages{
	Locale: "en-US",
	Messages: map[string]string{
		"BknBackend.CapabilityBinding.InvalidParameter":                     "Invalid capability binding parameter. Check the capability type and identifiers in the request body.",
		"BknBackend.CapabilityBinding.NullParameter.CapabilityID":           "Missing capability identifier. Provide skill_id for a skill and tool_id for a function.",
		"BknBackend.CapabilityBinding.NullParameter.OwnerID":                "Missing owning tool box identifier. A function binding requires owner_id (the tool box id); tool ids are scoped to their box in the execution factory.",
		"BknBackend.CapabilityBinding.InvalidCapabilityType":                "Unsupported capability type. Only skill and function are supported.",
		"BknBackend.CapabilityBinding.InternalError":                        "Failed to process the capability binding. Please retry or contact support.",
		"BknBackend.CapabilityBinding.InternalError.BeginTransactionFailed": "Failed to begin the transaction. Please retry or contact support.",
		"BknBackend.CapabilityBinding.InternalError.CreateBindingsFailed":   "Failed to write capability bindings. Please retry or contact support.",
		"BknBackend.CapabilityBinding.InternalError.DeleteBindingsFailed":   "Failed to release capability bindings. Please retry or contact support.",
		"BknBackend.CapabilityBinding.InternalError.ListBindingsFailed":     "Failed to list capability bindings. Please retry or contact support.",
		"BknBackend.CapabilityBinding.InternalError.GetBindingsTotalFailed": "Failed to count capability bindings. Please retry or contact support.",
		"BknBackend.CapabilityBinding.TargetNotFound":                       "Capability not found. Check the skill_id, or the box_id and tool_id of the function.",
		"BknBackend.CapabilityBinding.TargetNotAvailable":                   "The capability exists but is not usable. Publish the tool box or skill in the execution factory, or enable the tool, then mount it.",
		"BknBackend.CapabilityBinding.EmptyToolBox":                         "The tool box has no tool to mount. Add and enable at least one tool in that box first.",
		"BknBackend.CapabilityBinding.ExecutionFactoryUnavailable":          "The execution factory is unreachable; no binding was written. Retry later. This request produced no partial write.",
		"BknBackend.CapabilityBinding.ExportMetadataUnavailable":            "Capability names are unavailable, so the export was refused. Retry once the execution factory is reachable. Nothing was exported and nothing was changed.",
	},
}
