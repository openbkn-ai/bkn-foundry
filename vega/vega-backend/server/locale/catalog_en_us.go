// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var catalogEnUS = i18n.I18nMessages{
	Locale: i18n.AmericanEnglish,
	Messages: map[string]string{
		"VegaBackend.Catalog.InvalidParameter":                            "Invalid catalog parameter. Please check the parameter",
		"VegaBackend.Catalog.InvalidParameter.Type":                       "Invalid catalog type. Please check the parameter",
		"VegaBackend.Catalog.InvalidParameter.Name":                       "Invalid catalog name. Please check the parameter",
		"VegaBackend.Catalog.InvalidParameter.ID":                         "Request body is missing the 'id' field. Include the 'id' field in the body and make it equal to the path parameter",
		"VegaBackend.Catalog.InvalidParameter.ConnectorType":              "Invalid connector type. Please check the parameter",
		"VegaBackend.Catalog.InvalidParameter.ConnectorConfig":            "Invalid connector config. Please check the parameter",
		"VegaBackend.Catalog.LengthExceeded.Name":                         "Catalog name length exceeded. Please shorten the name",
		"VegaBackend.Catalog.LengthExceeded.Description":                  "Catalog description length exceeded. Please shorten the description",
		"VegaBackend.Catalog.EnabledFieldNotAllowed":                      "Catalog enabled state cannot be modified via PUT /catalogs/{id}. Use POST /catalogs/{id}/enable or /disable to change enabled state",
		"VegaBackend.Catalog.InternalError.GetAccountNamesFailed":         "Failed to get account names. Please contact the administrator",
		"VegaBackend.Catalog.NotFound":                                    "Catalog not found. Please check the catalog ID",
		"VegaBackend.Catalog.NameExists":                                  "Catalog name already exists. Please use a different name",
		"VegaBackend.Catalog.IDMismatch":                                  "Catalog id in the request body does not match the path. Make the 'id' field in the body equal to the path parameter",
		"VegaBackend.Catalog.IDExists":                                    "Catalog ID already exists. Please use a different ID",
		"VegaBackend.Catalog.HasAssets":                                   "Catalog has assets and cannot be deleted. Please delete all assets in the catalog first",
		"VegaBackend.Catalog.IsDisabled":                                  "Catalog is disabled. Please enable the catalog first",
		"VegaBackend.Catalog.InternalError":                               "Catalog internal error. Please contact the administrator",
		"VegaBackend.Catalog.InternalError.CreateFailed":                  "Failed to create catalog. Please contact administrator",
		"VegaBackend.Catalog.InternalError.CreateResourcesFailed":         "Failed to create catalog resource permissions. Please contact administrator",
		"VegaBackend.Catalog.InternalError.GetFailed":                     "Failed to get catalog. Please contact administrator",
		"VegaBackend.Catalog.InternalError.UpdateFailed":                  "Failed to update catalog. Please contact the administrator",
		"VegaBackend.Catalog.InternalError.DeleteFailed":                  "Failed to delete catalog. Please contact the administrator",
		"VegaBackend.Catalog.InternalError.TestConnectionFailed":          "Failed to test catalog connection. Please contact the administrator",
		"VegaBackend.Catalog.InternalError.EncryptFailed":                 "Failed to entrypt config. Please contact the administrator",
		"VegaBackend.Catalog.InvalidParameter.SensitiveFieldNotEncrypted": "Sensitive field is not properly encrypted. Please encrypt the sensitive field with the public key before submitting",
		"VegaBackend.Catalog.UpdateConflict":                              "The catalog configuration was updated after it was read. Reload the catalog configuration and retry the update",
	},
}
