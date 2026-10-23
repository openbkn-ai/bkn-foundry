// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var knowledgeNetworkEnUS = i18n.I18nMessages{
	Locale: i18n.AmericanEnglish,
	Messages: map[string]string{
		"OntologyQuery.KnowledgeNetwork.NullParameter.Direction":                      "Direction Is Empty. Please check whether the parameter is correct.",
		"OntologyQuery.KnowledgeNetwork.NullParameter.SourceObjectTypeId":             "Source Object Type Id Is Empty. Please check whether the parameter is correct.",
		"OntologyQuery.KnowledgeNetwork.NullParameter.TypePathObjectTypes":            "Object Types In The Type Path Is Empty. Please check whether the parameter is correct.",
		"OntologyQuery.KnowledgeNetwork.NullParameter.TypePathRelationTypes":          "Relation Types In The Type Path Is Empty. Please check whether the parameter is correct.",
		"OntologyQuery.KnowledgeNetwork.InvalidParameter":                             "Invalid Parameter. Please check whether the parameter is correct.",
		"OntologyQuery.KnowledgeNetwork.InvalidParameter.Direction":                   "Invalid Direction. Please check whether the parameter is correct.",
		"OntologyQuery.KnowledgeNetwork.InvalidParameter.IncludeTypeInfo":             "Invalid Include Type Info. Please check whether the parameter is correct.",
		"OntologyQuery.KnowledgeNetwork.InvalidParameter.PathLength":                  "Invalid Path Length. Please check whether the parameter is correct.",
		"OntologyQuery.KnowledgeNetwork.InvalidParameter.TypePath":                    "Invalid Include Type Path. Please check whether the parameter is correct.",
		"OntologyQuery.KnowledgeNetwork.KnowledgeNetworkNotFound":                     "Knowledge Network Not Found. Please check whether the parameter is correct.",
		"OntologyQuery.KnowledgeNetwork.RelationTypeNotFound":                         "Relation Type Not Found. Please check whether the parameter is correct.",
		"OntologyQuery.KnowledgeNetwork.InternalError.GetViewDataByIDFailed":          "Get View Data By ID Failed. Please try again. If the error occurs again, please submit the work order or contact technical support.",
		"OntologyQuery.KnowledgeNetwork.InternalError.GetKnowledgeNetworksByIDFailed": "Get Knowledge Networks By ID Failed. Please try again. If the error occurs again, please submit the work order or contact technical support.",
		"OntologyQuery.KnowledgeNetwork.InternalError.GetRelationTypeFailed":          "Get Relation Type Failed. Please try again. If the error occurs again, please submit the work order or contact technical support.",
	},
}
