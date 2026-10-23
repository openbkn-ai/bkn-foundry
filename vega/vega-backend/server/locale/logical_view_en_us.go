// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var logicalViewEnUS = i18n.I18nMessages{
	Locale: i18n.AmericanEnglish,
	Messages: map[string]string{
		"VegaBackend.LogicalView.InvalidParameter.JoinType":          "Invalid logic view join type. Please check the join type of the logic view definition",
		"VegaBackend.LogicalView.InvalidParameter.LogicDefinition":   "Invalid logic view definition. Please check the logic view definition",
		"VegaBackend.LogicalView.InvalidParameter.FieldName":         "Invalid field name. Please check the field name",
		"VegaBackend.LogicalView.LengthExceeded.FieldName":           "Field name length exceeded. Please shorten the field name",
		"VegaBackend.LogicalView.LengthExceeded.FieldDisplayName":    "Field display name length exceeded. Please shorten the display name",
		"VegaBackend.LogicalView.LengthExceeded.FieldComment":        "Field comment length exceeded. Please shorten the field comment",
		"VegaBackend.LogicalView.Duplicated.NodeID":                  "Logic view logic definition node id duplicated. Please use different logic definition node id",
		"VegaBackend.LogicalView.Duplicated.FieldName":               "Logic view field name already exists. Please use a different field name",
		"VegaBackend.LogicalView.Duplicated.FieldDisplayName":        "Logic view field display name already exists. Please use a different display name",
		"VegaBackend.LogicalView.InvalidParameter.FieldFeatureName":  "Invalid field feature name. Please check the feature name",
		"VegaBackend.LogicalView.LengthExceeded.FieldFeatureName":    "Field feature name length exceeded. Please shorten the feature name",
		"VegaBackend.LogicalView.Duplicated.FieldFeatureName":        "Field feature name already exists. Please use a different feature name",
		"VegaBackend.LogicalView.LengthExceeded.FieldFeatureComment": "Field feature comment length exceeded. Please shorten the feature comment",
		"VegaBackend.LogicalView.DefinitionInvalid":                  "The derived logical view definition is no longer valid{{if .Fields}} for source fields: {{.Fields}}{{else}}; check its fixed filter condition{{end}}. Edit the view to reconfigure its output fields or filter condition. Update the view output fields or fixed filter condition to match the current source schema, then query again",
	},
}
