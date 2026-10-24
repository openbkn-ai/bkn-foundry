// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var datasetEnUS = i18n.I18nMessages{
	Locale: i18n.AmericanEnglish,
	Messages: map[string]string{
		"VegaBackend.Dataset.InvalidParameter.SchemaDefinition":  "Invalid dataset schema definition. schema_definition cannot be empty; at least one field is required",
		"VegaBackend.Dataset.InvalidParameter.FieldName":         "Invalid field name. Please check the field parameters",
		"VegaBackend.Dataset.LengthExceeded.FieldName":           "Field name length exceeded. Please shorten the name",
		"VegaBackend.Dataset.LengthExceeded.FieldDisplayName":    "Field display name length exceeded. Please shorten the display name",
		"VegaBackend.Dataset.LengthExceeded.FieldComment":        "Field comment length exceeded. Please shorten the description",
		"VegaBackend.Dataset.Duplicated.FieldName":               "Dataset field name already exists. Please use a different field name",
		"VegaBackend.Dataset.Duplicated.FieldDisplayName":        "Dataset field display name already exists. Please use a different display name",
		"VegaBackend.Dataset.InvalidParameter.FieldType":         "Invalid field type. Please use a supported VEGA field type",
		"VegaBackend.Dataset.InvalidParameter.FieldFeatureName":  "Invalid field feature name. Please check the feature name",
		"VegaBackend.Dataset.LengthExceeded.FieldFeatureName":    "Field feature name length exceeded. Please shorten the feature name",
		"VegaBackend.Dataset.Duplicated.FieldFeatureName":        "Field feature name already exists. Please use a different feature name",
		"VegaBackend.Dataset.LengthExceeded.FieldFeatureComment": "Field feature comment length exceeded. Please shorten the feature description",
		"VegaBackend.Dataset.InvalidParameter.FieldFeatureType":  "Invalid field feature type. Please use one of: keyword, fulltext, vector",
		"VegaBackend.Dataset.InvalidParameter.FieldFeatureRef":   "Field feature ref_property is invalid. Ensure ref_property is non-empty and refers to an existing field",
		"VegaBackend.Dataset.Unsupported.FieldFeatureRefType":    "Field feature ref_property type does not match feature type. Ensure ref field type is compatible with the feature type (fulltext requires text, keyword requires string, vector requires vector)",
		"VegaBackend.Dataset.Duplicated.DefaultFeaturePerType":   "Only one default feature is allowed per feature type. Please unset other defaults",
	},
}
