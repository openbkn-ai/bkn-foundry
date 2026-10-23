// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var resourceEnUS = i18n.I18nMessages{
	Locale: i18n.AmericanEnglish,
	Messages: map[string]string{
		"VegaBackend.Resource.InvalidParameter":                    "Invalid resource parameter. Please check the parameter",
		"VegaBackend.Resource.Duplicated.FieldFeatureType":         "Field {{.FieldName}} already has a {{.FieldFeatureType}} feature. Please keep only one feature of this type for the field",
		"VegaBackend.Resource.CategoryNotCreatable":                "Resource category cannot be created via API. Only 'dataset' and 'logical_view' resources can be created via this API. Other categories must be created via a discover task.",
		"VegaBackend.Resource.InvalidParameter.Type":               "Invalid resource type. Please check the parameter",
		"VegaBackend.Resource.InvalidParameter.Name":               "Invalid resource name. Please check the parameter",
		"VegaBackend.Resource.InvalidParameter.CatalogID":          "Catalog ID is required. Please provide a valid catalog ID",
		"VegaBackend.Resource.InvalidParameter.Analyzer":           "Configured full-text analyzer is unavailable. Choose an analyzer supported by the current index capability snapshot",
		"VegaBackend.Resource.InvalidParameter.PrimaryKeyFields":   "Invalid primary key fields. Primary keys must be unique schema fields of a supported type",
		"VegaBackend.Resource.InvalidParameter.IncrementalFields":  "Invalid incremental fields. Incremental fields must be unique schema fields of a supported type",
		"VegaBackend.Resource.LengthExceeded.Name":                 "Resource name length exceeded. Please shorten the name",
		"VegaBackend.Resource.LengthExceeded.Description":          "Resource description length exceeded. Please shorten the description",
		"VegaBackend.Resource.NotFound":                            "Resource not found. Please check the resource ID",
		"VegaBackend.Resource.SourceReadForbidden":                 "The data source account cannot read this resource. Check the connection's schema scope or grant SELECT on the source table",
		"VegaBackend.Resource.NameExists":                          "Resource name already exists in catalog. Please use a different name",
		"VegaBackend.Resource.IDExists":                            "Resource ID already exists. Please use a different ID",
		"VegaBackend.Resource.CatalogNotFound":                     "Catalog not found for resource. Please check the catalog ID",
		"VegaBackend.Resource.IsDisabled":                          "Resource is disabled. Please enable the resource first",
		"VegaBackend.Resource.AlreadyEnabled":                      "Resource is already enabled. No action needed",
		"VegaBackend.Resource.AlreadyDisabled":                     "Resource is already disabled. No action needed",
		"VegaBackend.Resource.EnabledFieldNotAllowed":              "Resource enabled state cannot be modified by PUT. Use the resource enable or disable action",
		"VegaBackend.Resource.NotQueryable":                        "Resource is not queryable in its current status. Only resources in active or deprecated status are queryable; enable the resource or refresh its schema",
		"VegaBackend.Resource.MetadataUnavailable":                 "Resource metadata is unavailable. Refresh the resource schema, or restore a missing source resource, before querying",
		"VegaBackend.Resource.InternalError":                       "Resource internal error. Please contact the administrator",
		"VegaBackend.Resource.InternalError.CreateFailed":          "Failed to create resource. Please contact the administrator",
		"VegaBackend.Resource.InternalError.GetFailed":             "Failed to get resource. Please contact the administrator",
		"VegaBackend.Resource.InternalError.UpdateFailed":          "Failed to update resource. Please contact the administrator",
		"VegaBackend.Resource.InternalError.DeleteFailed":          "Failed to delete resource. Please contact the administrator",
		"VegaBackend.Resource.InternalError.SyncFailed":            "Failed to sync resource. Please contact the administrator",
		"VegaBackend.Resource.InternalError.InvalidCategory":       "Invalid resource category. Please check the parameter",
		"VegaBackend.Resource.InternalError.GetAccountNamesFailed": "Failed to get account names. Please contact the administrator",
		"VegaBackend.Resource.UpdateConflict":                      "The resource was updated after it was read. Reload the resource and retry the update",
		"VegaBackend.Resource.DeleteConflict":                      "The resource is no longer stale and missing. Reload the resource before deleting it",
		"VegaBackend.Resource.QueryValueExceedsKeywordLimit":       "The query value exceeds the maximum indexed keyword length. Use a shorter value or explicitly query the original data when available.",
	},
}
