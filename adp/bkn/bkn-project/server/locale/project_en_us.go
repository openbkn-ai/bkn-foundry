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
		"BknProject.Requirement.InvalidRequestBody":  "Invalid Request Body. The request body must be valid JSON.",
		"BknProject.Requirement.MissingUpdateTime":   "Update Time Missing. Send the update_time read with the requirement specification; reload it and try again.",
		"BknProject.Requirement.UnsupportedFormat":   "Unsupported File Format. Only Markdown (.md, .markdown) or plain-text (.txt) files can be uploaded.",
		"BknProject.Requirement.NotText":             "Not a Text File. The file must be UTF-8 text.",
		"BknProject.Requirement.DocumentTooLarge":    "File Too Large. The file exceeds the size limit of a requirement document.",
		"BknProject.Requirement.TooManyDocuments":    "Too Many Documents. The project has reached its limit of requirement documents; remove ones it no longer needs first.",
		"BknProject.Requirement.InvalidDocumentType": "Invalid Document Type. doc_type must be prd, policy, standard or other.",
		"BknProject.Requirement.InvalidItem":         "Invalid Item. The item misses required content, is too long, or refers to items that do not exist, see error_details.",
		"BknProject.Requirement.InvalidResolution":   "Invalid Resolution. A resolution needs a chosen document or a written conclusion, and a reason.",
		"BknProject.Requirement.InvalidOption":       "Option Not Found. The chosen option is not one of this choice's options.",
		"BknProject.Requirement.NoDocuments":         "No Requirement Documents. Upload at least one requirement document before drafting.",
		"BknProject.Requirement.NoCatalogs":          "No Data Catalogs. Choose data catalogs for the project before drafting.",
		"BknProject.Requirement.InputsTooLarge":      "Documents Too Long. The documents together exceed what one drafting can read; trim or remove some and try again.",
		"BknProject.Requirement.NotOwner":            "Not the Project Owner. Only the project owner can change the requirement specification and documents.",
		"BknProject.Requirement.NotFound":            "No Requirement Specification. The project has not drafted a requirement specification yet.",
		"BknProject.Requirement.DocumentNotFound":    "Document Not Found. The project has no such requirement document.",
		"BknProject.Requirement.ItemNotFound":        "Item Not Found. The requirement specification has no such item.",
		"BknProject.Requirement.Drafting":            "Drafting in Progress. The requirement specification is being drafted; wait for it to finish and try again.",
		"BknProject.Requirement.Stale":               "Specification Changed. Someone changed the requirement specification after you loaded it; reload it and try again.",
		"BknProject.Requirement.HasDecisions":        "Specification Has Decisions. Drafting again discards the conflict resolutions and modelling choices made; confirm discarding them to draft again.",
		"BknProject.Requirement.ProjectArchived":     "Project Archived. The requirement specification and documents of an archived project cannot be changed.",
		"BknProject.Requirement.InternalError":       "Internal Error. Processing the requirement specification failed; contact the administrator.",
		"BknProject.Requirement.InvalidTest":         "Invalid Test Question. A test question must not be empty or too long, and the concepts its values belong to must exist, see error_details.",
		"BknProject.Requirement.TestNotFound":        "Test Question Not Found. The scenario question has no such test question.",
		"BknProject.Requirement.Generating":          "Generation in Progress. Test questions and development guidance of this scenario question are being generated; wait for them to finish and try again.",
	},
}
