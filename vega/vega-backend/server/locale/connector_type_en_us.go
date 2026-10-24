// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var connectorTypeEnUS = i18n.I18nMessages{
	Locale: i18n.AmericanEnglish,
	Messages: map[string]string{
		"VegaBackend.ConnectorType.InvalidParameter":               "Invalid connector type parameter. Please check the parameter",
		"VegaBackend.ConnectorType.InvalidParameter.Type":          "Invalid connector type. Please check the parameter",
		"VegaBackend.ConnectorType.InvalidParameter.Name":          "Invalid connector type name. Please check the parameter",
		"VegaBackend.ConnectorType.InvalidParameter.Mode":          "Invalid connector type mode. Please check the parameter",
		"VegaBackend.ConnectorType.InvalidParameter.Category":      "Invalid connector type category. Please check the parameter",
		"VegaBackend.ConnectorType.InvalidParameter.Endpoint":      "Invalid connector type endpoint. Please check the parameter",
		"VegaBackend.ConnectorType.BadRequest":                     "Invalid connector type parameter. Please check the parameter",
		"VegaBackend.ConnectorType.NotFound":                       "Connector type not found. Please check the connector type ID",
		"VegaBackend.ConnectorType.TypeExists":                     "Connector type already exists. Please use a different type",
		"VegaBackend.ConnectorType.NameExists":                     "Connector type name already exists. Please use a different name",
		"VegaBackend.ConnectorType.TypeMismatch":                   "The connector type in the request body does not match the path. Omit the type field in the body, or make it match the path parameter",
		"VegaBackend.ConnectorType.InternalError":                  "Connector type internal error. Please contact the administrator",
		"VegaBackend.ConnectorType.FieldConfigUnavailable":         "Connector field configuration is unavailable. Ensure the connector implementation is available, then try again",
		"VegaBackend.ConnectorType.InternalError.RegisterFailed":   "Failed to reqister connector type. Please contact the administrator",
		"VegaBackend.ConnectorType.InternalError.GetFailed":        "Failed to get connector type. Please contact the administrator",
		"VegaBackend.ConnectorType.InternalError.UpdateFailed":     "Failed to update connector type. Please contact the administrator",
		"VegaBackend.ConnectorType.InternalError.DeleteFailed":     "Failed to delete connector type. Please contact the administrator",
		"VegaBackend.ConnectorType.PostgreSQL.Options.Name":        "Connection options",
		"VegaBackend.ConnectorType.PostgreSQL.Options.Description": "Optional; only sslmode, connect_timeout, application_name, fallback_application_name, statement_timeout, lock_timeout and idle_in_transaction_session_timeout are supported. connect_timeout is a non-negative integer in seconds; session timeouts accept milliseconds or strings with PostgreSQL time units, and 0 disables them. sslmode defaults to disable; SSL files are unsupported. The client encoding uses the driver default UTF8 and cannot be configured.",
	},
}
