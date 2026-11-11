// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package knversion holds the error codes bkn-backend answers with when it
// refuses a request because of the knowledge network version it names.
//
// A service that reads through bkn-backend returns such a refusal under the
// same code, so the caller sees one code whichever service answered. An error
// code and its messages can be registered only once in a process, so the
// services that answer with these codes import them from here instead of each
// declaring them: declaring them twice stops a program that links two of the
// services from starting.
package knversion

import (
	"slices"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/knversion/locale"
)

// Refusals about the requested version. The codes keep bkn-backend's prefix:
// they are part of its published API.
const (
	NotVersioned          = "BknBackend.KNVersion.NotVersioned"
	InvalidVersion        = "BknBackend.KNVersion.InvalidVersion"
	VersionNotFound       = "BknBackend.KNVersion.VersionNotFound"
	VersionPurged         = "BknBackend.KNVersion.VersionPurged"
	EditionRestricted     = "BknBackend.KNVersion.EditionRestricted"
	VersioningDisabling   = "BknBackend.KNVersion.VersioningDisabling"
	DraftRevisionMismatch = "BknBackend.KNVersion.DraftRevisionMismatch"
)

var refusalCodes = []string{
	NotVersioned,
	InvalidVersion,
	VersionNotFound,
	VersionPurged,
	EditionRestricted,
	VersioningDisabling,
	DraftRevisionMismatch,
}

func init() {
	i18n.RegisterI18n(&locale.EnUS)
	i18n.RegisterI18n(&locale.ZhCN)
	httperrors.Register(refusalCodes)
}

// RefusalCodes lists the version refusal codes.
func RefusalCodes() []string { return slices.Clone(refusalCodes) }

// IsRefusal reports whether code is a refusal about the requested version.
func IsRefusal(code string) bool { return slices.Contains(refusalCodes, code) }
