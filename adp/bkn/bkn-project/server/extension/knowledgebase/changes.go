// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledgebase

import (
	"context"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// ChangeSource is the published-proxy read surface used by refresh. CurrentCursor
// reads the change feed's latest_cursor without enumerating its history. Empty
// means that the source has no recorded changes. Errors never mean an empty KB.
// Construction readers must not fall back to this independently authorized path.
type ChangeSource interface {
	Source
	CurrentCursor(context.Context, SourceScope) (string, error)
}

// PublishedSource uses a persistent mounted network proxy, never a disposable
// build delegation. Every operation checks the publication's current publisher.
type PublishedSource interface {
	ChangeSource
	EvidenceSource
	Check(context.Context) error
}

// RefreshTasks runs the same fixed, tool-free document Agent as construction.
// The trusted coordinator supplies the recorded publisher after live checks;
// neither method accepts or retains an operator token.
type RefreshTasks interface {
	Start(context.Context, interfaces.AccountInfo, *interfaces.KBBuild, int, *interfaces.Draft, *FileInput) (*interfaces.BknAgentTask, error)
	Poll(context.Context, interfaces.AccountInfo, string) (*interfaces.BknAgentTask, error)
}
type PublishedProxy struct {
	NetworkID string
	AccountID string
	Version   int64
}
type NetworkProxy interface {
	PublishedProxy(context.Context, string) (PublishedProxy, error)
}
type RefreshSources interface {
	OpenPublished(context.Context, *interfaces.KBPublication, func(context.Context) error) (PublishedSource, error)
}

// PublishedHoldCleanup releases only protected, journaled retention IDs. It has
// no read surface and remains usable when a publisher loses source query rights.
type PublishedHoldCleanup interface {
	ReleasePublishedHold(context.Context, *interfaces.KBPublication, interfaces.KBHoldGroup, func(context.Context) error) error
}
