// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var draftEnUS = i18n.I18nMessages{
	Locale: i18n.AmericanEnglish,
	Messages: map[string]string{
		"BknProject.Draft.NoCatalogs":         "No Data Catalogs. Select the data catalogs that hold the project's structured data before drafting the ontology.",
		"BknProject.Draft.NotFound":           "Draft Not Found. The project has no ontology draft yet.",
		"BknProject.Draft.Drafting":           "Drafting. The project's ontology is being drafted; wait for it to finish before drafting again.",
		"BknProject.Draft.InternalError":      "Internal Error. Please contact the administrator.",
		"BknProject.Draft.InvalidRequestBody": "Invalid Request Body. The body must be valid JSON.",
		"BknProject.Draft.EmptyEdit":          "Nothing to Change. Give adopted, name or description to change.",
		"BknProject.Draft.InvalidName":        "Invalid Name. The name must not be empty and must be at most 128 characters.",
		"BknProject.Draft.InvalidDescription": "Invalid Description. The description must be at most 1000 characters.",
		"BknProject.Draft.EndpointNotAdopted": "Object Type Not Adopted. Adopt both object types a relation type connects before adopting the relation type.",
		"BknProject.Draft.ItemNotFound":       "Draft Item Not Found. The draft has no such item.",
		"BknProject.Draft.ProjectArchived":    "Project Archived. Unarchive the project before changing its draft.",
		"BknProject.Draft.NotReady":           "Draft Not Ready. Only a draft that finished drafting can be changed.",
		"BknProject.Draft.Stale":              "Draft Changed. Someone changed the draft after you loaded it; reload it and try again.",
		"BknProject.Draft.HasDecisions":       "Draft Has Decisions. Drafting again discards the decisions made on the draft; confirm discarding them to draft again.",
		"BknProject.Draft.InvalidIdentity":    "Invalid Identity. Give one or more distinct properties of the object type, each of type string, text, integer or unsigned integer.",
		"BknProject.Draft.NotRestorable":      "Field Not Restorable. The field was excluded because its type cannot be mapped to BKN.",
		"BknProject.Draft.FieldNotFound":      "Source Field Not Found. The field is no longer in its table, or you cannot read the table; draft again.",
		"BknProject.Draft.NothingAdopted":     "Nothing Adopted. Adopt at least one object type before confirming the draft.",
		"BknProject.Draft.InvalidItem":        "Draft Item Incomplete. An adopted item lacks what a definition needs, see error_details. Draft again.",
		"BknProject.Draft.NotOwner":           "Not the Project Owner. Only the project owner can confirm the draft.",
		"BknProject.Check.NotOwner":           "Not the Project Owner. Only the project owner can run the check again.",
		"BknProject.Check.NotFound":           "Not Checked Yet. The ontology is checked once it is confirmed.",
		"BknProject.Check.NothingConfirmed":   "Ontology Not Confirmed. Confirm the ontology draft first; it is checked once confirmed.",
		"BknProject.Check.ConfirmAgain":       "Confirm Again. The draft changed after it was confirmed, so what was confirmed can no longer be checked; confirm it again.",
		"BknProject.Check.Running":            "Checking. What was confirmed is being checked; wait for it to finish.",
		"BknProject.Draft.HasPending":         "Items Wait for You. Before confirming, choose the identity of adopted object types and settle relations that connect an object type not adopted, see error_details.",
		"BknProject.Draft.CatalogMissing":     "Data Catalog Not Found. A data catalog of the project no longer exists; change the project's data catalogs before drafting.",
		"BknProject.Draft.MissingUpdateTime":  "Missing Update Time. Give update_time, the update time of the draft the change was made on.",
		"BknProject.Draft.NeedsIdentity":      "Identity Not Chosen. Choose the object type's identity before adopting it.",
	},
}
