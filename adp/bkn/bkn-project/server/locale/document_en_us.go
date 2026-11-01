// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var documentEnUS = i18n.I18nMessages{
	Locale: i18n.AmericanEnglish,
	Messages: map[string]string{
		"BknProject.Draft.EventParticipantInUse":            "This relation is still an event participant. Update the event participant settings before deleting it.",
		"BknProject.Draft.DocumentRelation.ManualReason":    "Configured by the modeller; verify matches using the chosen fields during building.",
		"BknProject.Draft.DocumentDataNotReady":             "Document data is not ready. Complete data preparation before confirming the draft; reviewed candidates are preserved.",
		"BknProject.KnowledgeBase.NoDebugSamples":           "Select and save debug samples first.",
		"BknProject.KnowledgeBase.NoLocatedSamples":         "The selected samples have no locatable text. Choose parsed files with readable text.",
		"BknProject.KnowledgeBase.SampleLimit":              "The selected samples are too large. Select fewer or smaller debug files.",
		"BknProject.KnowledgeBase.InvalidProposal":          "The proposal failed source or model validation. Adjust the samples and retry.",
		"BknProject.KnowledgeBase.ModelUnavailable":         "Concept extraction is unavailable. Check the builtin assistant configuration and retry.",
		"BknProject.KnowledgeBase.ExtractionTimeout":        "Concept extraction timed out. Select fewer samples and retry.",
		"BknProject.Draft.DocumentField._id":                "Record identifier",
		"BknProject.Draft.DocumentField.entity_id":          "Entity identifier",
		"BknProject.Draft.DocumentField.document_id":        "Document",
		"BknProject.Draft.DocumentField.block_id":           "Source block",
		"BknProject.Draft.DocumentField.content_version_id": "Content version",
		"BknProject.Draft.DocumentField.source_quote":       "Source quote",
		"BknProject.Draft.DocumentField.source_start":       "Quote start",
		"BknProject.Draft.DocumentField.source_end":         "Quote end",
		"BknProject.Draft.DocumentField.extraction_version": "Extraction version",
		"BknProject.Draft.DocumentField.effective_from":     "Effective from",
		"BknProject.Draft.DocumentField.effective_to":       "Effective to",
		"BknProject.Draft.DocumentField.status":             "Status",
		"BknProject.Draft.DocumentField.recorded_at":        "Recorded at",
		"BknProject.Draft.DocumentField.knowledge_base_id":  "Knowledge base",
		"BknProject.Draft.DocumentField.file_id":            "File",
		"BknProject.Draft.DocumentField.name":               "File name",
		"BknProject.Draft.DocumentField.image_ref":          "Image reference",
		"BknProject.Draft.Evidence.document":                "Source document",
		"BknProject.Draft.Evidence.block":                   "Source block",
		"BknProject.Draft.Evidence.image":                   "Source image",
		"BknProject.Draft.Evidence.contains_blocks":         "Contains source blocks",
		"BknProject.Draft.Evidence.contains_images":         "Contains source images",
		"BknProject.Draft.Evidence.reason":                  "Locate the original evidence",
		"BknProject.Draft.Evidence.reference_document":      "{{.name}} source document",
		"BknProject.Draft.Evidence.reference_block":         "{{.name}} source block",
	},
}
