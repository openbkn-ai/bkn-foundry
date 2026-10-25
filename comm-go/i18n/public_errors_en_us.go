// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package i18n

var publicErrorsEnUS = I18nMessages{
	Locale: AmericanEnglish,
	Messages: map[string]string{
		"Public.BadRequest":          "Invalid request parameters. Check the request parameters and try again.",
		"Public.Unauthorized":        "Authentication failed. Provide valid authentication credentials.",
		"Public.Forbidden":           "You do not have permission to perform this operation. Contact an administrator to obtain the required permission.",
		"Public.NotFound":            "The requested resource was not found. Check that the resource identifier is correct.",
		"Public.MethodNotAllowed":    "The request method is not supported. Use a request method supported by this API.",
		"Public.Conflict":            "The request conflicts with the current resource state. Refresh the resource state and try again.",
		"Public.InternalServerError": "An internal server error occurred. Try again later.",
		"Public.NotImplemented":      "The server does not implement this request. Confirm the API capability and try again.",
		"Public.ServiceUnavailable":  "The service is temporarily unavailable. Try again later.",
	},
}
