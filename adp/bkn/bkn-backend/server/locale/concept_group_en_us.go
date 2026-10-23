// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var conceptGroupEnUS = i18n.I18nMessages{
	Locale: i18n.AmericanEnglish,
	Messages: map[string]string{
		"BknBackend.ConceptGroup.Duplicated.Name":                                    "Same Konwledge Network Name Existed. Please check whether the parameter is correct.",
		"BknBackend.ConceptGroup.InvalidParameter":                                   "Invalid Request Paramter. Please check whether the parameter is correct.",
		"BknBackend.ConceptGroup.InvalidParameter.ConceptCondition":                  "Invalid Concept Condition Paramter. Please check whether the parameter is correct.",
		"BknBackend.ConceptGroup.InvalidParameter.Direction":                         "Invalid Direction Paramter. Please check whether the parameter is correct.",
		"BknBackend.ConceptGroup.InvalidParameter.IncludeStatistics":                 "Invalid Include Statistics Paramter. Please check whether the parameter is correct.",
		"BknBackend.ConceptGroup.InvalidParameter.IncludeTypeInfo":                   "Invalid Include Type Info Paramter. Please check whether the parameter is correct.",
		"BknBackend.ConceptGroup.InvalidParameter.PathLength":                        "Invalid Path Length Paramter. Please check whether the parameter is correct.",
		"BknBackend.ConceptGroup.ConceptGroupIDExisted":                              "Same ID Existed. Please check whether the parameter is correct.",
		"BknBackend.ConceptGroup.ConceptGroupNameExisted":                            "Same Concept Group Name Existed. Please check whether the parameter is correct.",
		"BknBackend.ConceptGroup.ConceptGroupRelationExisted":                        "Same Concept Group Relation Existed. Please check whether the parameter is correct.",
		"BknBackend.ConceptGroup.ConceptGroupRelationNotExisted":                     "Same Concept Group Relation Not Existed. Please check whether the parameter is correct.",
		"BknBackend.ConceptGroup.LengthExceeded.Name":                                "The Length of Concept Group Name Out of Limit. Please check whether the parameter is correct.",
		"BknBackend.ConceptGroup.NullParameter.Direction":                            "Direction Is Empty. Please check whether the parameter is correct.",
		"BknBackend.ConceptGroup.NullParameter.Name":                                 "Concept Group Name Is Empty. Please check whether the parameter is correct.",
		"BknBackend.ConceptGroup.NullParameter.SourceObjectTypeId":                   "Source Object Type Id Is Empty. Please check whether the parameter is correct.",
		"BknBackend.ConceptGroup.ConceptGroupNotFound":                               "The Concept Group Does Not Exist. Please try again. If the error occurs again, please submit the work order or contact technical support.",
		"BknBackend.ConceptGroup.ObjectTypeNotFound":                                 "The Object Type Does Not Exist. Please try again. If the error occurs again, please submit the work order or contact technical support.",
		"BknBackend.ConceptGroup.InternalError":                                      "Internal Error. Please try again. If the error occurs again, please submit the work order or contact technical support.",
		"BknBackend.ConceptGroup.InternalError.AddObjectTypesToConceptGroupFailed":   "Add Object Types To Group Failed. Please try again. If the error occurs again, please submit the work order or contact technical support.",
		"BknBackend.ConceptGroup.InternalError.MissingTransaction":                   "Missing Transaction. Please try again. If the error occurs again, please submit the work order or contact technical support.",
		"BknBackend.ConceptGroup.InternalError.BeginTransactionFailed":               "Begin Transaction Failed. Please try again. If the error occurs again, please submit the work order or contact technical support.",
		"BknBackend.ConceptGroup.InternalError.CheckConceptGroupIfExistFailed":       "Check Concept Group Exist Failed. Please try again. If the error occurs again, please submit the work order or contact technical support.",
		"BknBackend.ConceptGroup.InternalError.GetConceptGroupByIDFailed":            "Get Concept Group By ID Failed. Please try again. If the error occurs again, please submit the work order or contact technical support.",
		"BknBackend.ConceptGroup.InternalError.UpdateConceptGroupFailed":             "Update Concept Group Failed. Please try again. If the error occurs again, please submit the work order or contact technical support.",
		"BknBackend.ConceptGroup.InternalError.CreateConceptGroupFailed":             "Create Concept Group Failed. Please try again. If the error occurs again, please submit the work order or contact technical support.",
		"BknBackend.ConceptGroup.InternalError.CreateConceptGroupRelationFailed":     "Create Concept Group Relation Failed. Please try again. If the error occurs again, please submit the work order or contact technical support.",
		"BknBackend.ConceptGroup.InternalError.CreateResourcesFailed":                "Create Concept Group Resources Failed. Please try again. If the error occurs again, please submit the work order or contact technical support.",
		"BknBackend.ConceptGroup.InternalError.GetActionTypesTotalFailed":            "Get Action Types Total Failed Failed. Please try again. If the error occurs again, please submit the work order or contact technical support.",
		"BknBackend.ConceptGroup.InternalError.GetConceptIDsByConceptGroupIDsFailed": "Get Concept IDs By Concept Group ID Failed. Please try again. If the error occurs again, please submit the work order or contact technical support.",
		"BknBackend.ConceptGroup.InternalError.GetRelationTypesTotalFailed":          "Get Relation Types Total In Concept Group Failed. Please try again. If the error occurs again, please submit the work order or contact technical support.",
		"BknBackend.ConceptGroup.InternalError.GetVectorFailed":                      "Get Concept Group Vector Failed. Please try again. If the error occurs again, please submit the work order or contact technical support.",
		"BknBackend.ConceptGroup.InternalError.InsertOpenSearchDataFailed":           "Insert Concept Group Data Into OpenSearch Failed. Please try again. If the error occurs again, please submit the work order or contact technical support.",
		"BknBackend.ConceptGroup.InternalError.CreateObjectTypesFailed":              "Failed to create object types. Please try again. If the error occurs again, please submit the work order or contact technical support.",
		"BknBackend.ConceptGroup.InternalError.CreateRelationTypesFailed":            "Failed to create relation types. Please try again. If the error occurs again, please submit the work order or contact technical support.",
		"BknBackend.ConceptGroup.InternalError.CreateActionTypesFailed":              "Failed to create action types. Please try again. If the error occurs again, please submit the work order or contact technical support.",
	},
}
