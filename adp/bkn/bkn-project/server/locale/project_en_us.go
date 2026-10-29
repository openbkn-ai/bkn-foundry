// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var projectEnUS = i18n.I18nMessages{
	Locale: i18n.AmericanEnglish,
	Messages: map[string]string{
		"BknProject.Project.InvalidRequestBody":      "Invalid Request Body. The body must be valid JSON.",
		"BknProject.Project.InvalidName":             "Invalid Project Name. The name must not be empty and must be at most 128 characters.",
		"BknProject.Project.InvalidNetwork":          "Invalid Target Network. Give exactly one of kn_id (an existing network) and new_kn (a network to create, with a name).",
		"BknProject.Project.InvalidCatalogIDs":       "Invalid Data Catalogs. catalog_ids holds at most 100 ids, each non-empty and at most 40 characters.",
		"BknProject.Project.InvalidArchived":         "Invalid Archive Filter. archived must be false, true or all.",
		"BknProject.Project.InvalidNamingLanguage":   "Invalid Naming Language. naming_language must be zh-CN or en-US.",
		"BknProject.Project.NotOwner":                "Not the Project Owner. Only the project owner may change the project.",
		"BknProject.Project.NotFound":                "Project Not Found. The project does not exist, or you cannot see its knowledge network.",
		"BknProject.Project.BknBackendRequestFailed": "bkn-backend Request Failed. bkn-backend refused the request; error_details holds its reply.",
		"BknProject.Project.InternalError":           "Internal Error. Please contact the administrator.",
	},
}
