// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var knVersionEnUS = i18n.I18nMessages{
	Locale: i18n.AmericanEnglish,
	Messages: map[string]string{
		"BknBackend.KNVersion.AlreadyVersioned":       "Versioning is already enabled for this knowledge network.",
		"BknBackend.KNVersion.ReservedBranchInUse":    "The knowledge network already has data on a reserved branch (draft or v<N>). Remove that data before enabling versioning.",
		"BknBackend.KNVersion.RunningVersionMismatch": "The running version has changed. Reload the version state and retry.",
		"BknBackend.KNVersion.DraftHasChanges":        "The draft has unpublished changes. Confirm overwriting them to continue.",
		"BknBackend.KNVersion.DraftBaseOutdated":      "The running version was switched after the draft was based on it. Review the difference and confirm publishing an outdated draft.",
		"BknBackend.KNVersion.DefinitionModified":     "The definition was modified by someone else. Reload it and retry.",
		"BknBackend.KNVersion.PreconditionRequired":   "Writes to a versioned knowledge network need a precondition: If-Match for one definition, or expected_draft_revision.",
		"BknBackend.KNVersion.ConceptIndexFailed":     "Building the concept index failed. Retry the operation.",
		"BknBackend.KNVersion.NothingToPublish":       "The draft has no unpublished changes.",
		"BknBackend.KNVersion.ValidationFailed":       "The draft did not pass the publish checks. Fix the listed problems and retry.",
		"BknBackend.KNVersion.BrokenReferences":       "The target version references resources that no longer exist. Fix the listed problems and retry.",
		"BknBackend.KNVersion.VersionNotActivatable":  "Only a history version can become the running version.",
		"BknBackend.KNVersion.VersionNotRestorable":   "Only a deprecated version can be restored.",
		"BknBackend.KNVersion.VersionNotDeprecatable": "Only a history version can be deprecated.",
		"BknBackend.KNVersion.VersionProtected":       "The running version and the version the draft is based on cannot be purged.",
		"BknBackend.KNVersion.IdempotencyKeyReused":   "The Idempotency-Key was already used for a different operation or version.",
		"BknBackend.KNVersion.InvalidParameter":       "Invalid version request parameter. Check the error details.",
		"BknBackend.KNVersion.InternalError":          "Internal error in knowledge network versioning. Try again; contact an administrator if the problem persists.",
	},
}
