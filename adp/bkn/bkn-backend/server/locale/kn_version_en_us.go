// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var knVersionEnUS = i18n.I18nMessages{
	Locale: i18n.AmericanEnglish,
	Messages: map[string]string{
		"BknBackend.KNVersion.NotVersioned":           "Versioning is not enabled for this knowledge network.",
		"BknBackend.KNVersion.AlreadyVersioned":       "Versioning is already enabled for this knowledge network.",
		"BknBackend.KNVersion.ReservedBranchInUse":    "The knowledge network already has data on a reserved branch (draft or v<N>). Remove that data before enabling versioning.",
		"BknBackend.KNVersion.InvalidVersion":         "Invalid knowledge network version. Use draft, running or a version number; a versioned network accepts only the main or draft branch.",
		"BknBackend.KNVersion.VersionNotFound":        "The knowledge network version does not exist.",
		"BknBackend.KNVersion.VersionPurged":          "The knowledge network version has been purged and its content is no longer available.",
		"BknBackend.KNVersion.EditionRestricted":      "This version operation requires the Professional edition. The Community edition keeps only the running and the previous version.",
		"BknBackend.KNVersion.VersioningDisabling":    "Versioning is being disabled for this knowledge network. Writes, version operations and reads of the draft or older versions are paused.",
		"BknBackend.KNVersion.RunningVersionMismatch": "The running version has changed. Reload the version state and retry.",
		"BknBackend.KNVersion.DraftRevisionMismatch":  "The draft has changed. Reload the draft and retry.",
		"BknBackend.KNVersion.DraftHasChanges":        "The draft has unpublished changes. Confirm overwriting them to continue.",
		"BknBackend.KNVersion.DraftBaseOutdated":      "The running version was switched after the draft was based on it. Review the difference and confirm publishing an outdated draft.",
		"BknBackend.KNVersion.DefinitionModified":     "The definition was modified by someone else. Reload it and retry.",
		"BknBackend.KNVersion.PreconditionRequired":   "Writes to a versioned knowledge network need a precondition: If-Match for one definition, or expected_draft_revision.",
		"BknBackend.KNVersion.ConceptIndexFailed":     "Building the concept index failed. Retry the operation.",
		"BknBackend.KNVersion.ConceptIndexBuilding":   "The concept index of the draft is still being built. Retry later.",
		"BknBackend.KNVersion.ProxySyncRequired":      "The proxy permissions of the new version are not in effect yet. Retry the proxy synchronization, then retry the operation.",
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
