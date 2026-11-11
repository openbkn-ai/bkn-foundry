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

var refusalsEnUS = i18n.I18nMessages{
	Locale: i18n.AmericanEnglish,
	Messages: map[string]string{
		NotVersioned:          "Versioning is not enabled for this knowledge network.",
		InvalidVersion:        "Invalid knowledge network version. Use draft, running or a version number; a versioned network accepts only the main or draft branch.",
		VersionNotFound:       "The knowledge network version does not exist.",
		VersionPurged:         "The knowledge network version has been purged and its content is no longer available.",
		EditionRestricted:     "This version operation requires the Professional edition. The Community edition keeps only the running and the previous version.",
		VersioningDisabling:   "Versioning is being disabled for this knowledge network. Writes, version operations and reads of the draft or older versions are paused.",
		DraftRevisionMismatch: "The draft has changed. Reload the draft and retry.",
	},
}

var refusalsZhCN = i18n.I18nMessages{
	Locale: i18n.SimplifiedChinese,
	Messages: map[string]string{
		NotVersioned:          "该知识网络未开启版本管理。",
		InvalidVersion:        "版本取值不合法。请使用 draft、running 或版本号；版本化网络只接受 main 或 draft 分支。",
		VersionNotFound:       "知识网络版本不存在。",
		VersionPurged:         "该版本已清理，内容不再可用。",
		EditionRestricted:     "该版本操作需要专业版。社区版只保留运行版本与上一版。",
		VersioningDisabling:   "该知识网络正在关闭版本管理，写入、版本操作以及读取草稿与历史版本暂停。",
		DraftRevisionMismatch: "草稿已变化，请重新读取草稿后重试。",
	},
}

func init() {
	i18n.RegisterI18n(&refusalsEnUS)
	i18n.RegisterI18n(&refusalsZhCN)
	httperrors.Register(refusalCodes)
}

// RefusalCodes lists the version refusal codes.
func RefusalCodes() []string { return slices.Clone(refusalCodes) }

// IsRefusal reports whether code is a refusal about the requested version.
func IsRefusal(code string) bool { return slices.Contains(refusalCodes, code) }
