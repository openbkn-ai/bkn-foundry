// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
)

const (
	// KNVersionRunning and KNVersionDraft are the symbolic values of kn_version.
	KNVersionRunning = "running"
	KNVersionDraft   = "draft"

	// HTTPHeaderKNVersion and HTTPHeaderKNDraftRevision are the bkn-backend response headers
	// that report the version a read was served from.
	HTTPHeaderKNVersion       = "X-Kn-Version"
	HTTPHeaderKNDraftRevision = "X-Kn-Draft-Revision"
)

type knVersionPinKey struct{}

// KNVersionPin fixes the knowledge network version for the lifetime of one query.
//
// A request names a version with kn_version (running, draft or a number) and, for the
// draft, optionally a draft_revision. The first bkn-backend read that answers with
// X-Kn-Version fixes the version actually read; every later bkn-backend call of the same
// query asks for exactly that version, so a publish, a switch or a draft edit in the
// middle of a query cannot mix two versions. A network without versioning never answers
// with the header, so its pin stays unresolved and its requests are sent as they always were.
type KNVersionPin struct {
	mu sync.Mutex
	// requested is the kn_version of the request ("" when absent) and requestedRevision its draft_revision.
	requested         string
	requestedRevision string
	// version and revision are what bkn-backend reported: "<N>" or "draft", and the draft revision.
	version  string
	revision string
}

// NewKNVersionPin builds the pin of a request from its kn_version and draft_revision values.
// It rejects values bkn-backend would not accept, so the caller can answer 400 right away.
func NewKNVersionPin(knVersion, draftRevision string) (*KNVersionPin, error) {
	knVersion = strings.TrimSpace(knVersion)
	draftRevision = strings.TrimSpace(draftRevision)
	switch knVersion {
	case "", KNVersionRunning, KNVersionDraft:
	default:
		if n, err := strconv.Atoi(knVersion); err != nil || n <= 0 {
			return nil, fmt.Errorf("kn_version must be draft, running or a version number")
		}
	}
	if draftRevision != "" {
		if knVersion != KNVersionDraft {
			return nil, fmt.Errorf("draft_revision is only accepted together with kn_version=draft")
		}
		if n, err := strconv.ParseInt(draftRevision, 10, 64); err != nil || n < 0 {
			return nil, fmt.Errorf("draft_revision must be a non-negative integer")
		}
	}
	return &KNVersionPin{requested: knVersion, requestedRevision: draftRevision}, nil
}

// WithKNVersionPin attaches the pin of one query to its context.
func WithKNVersionPin(ctx context.Context, pin *KNVersionPin) context.Context {
	return context.WithValue(ctx, knVersionPinKey{}, pin)
}

// KNVersionPinFromContext returns the pin of the query, or nil when the context has none.
func KNVersionPinFromContext(ctx context.Context) *KNVersionPin {
	if ctx == nil {
		return nil
	}
	pin, _ := ctx.Value(knVersionPinKey{}).(*KNVersionPin)
	return pin
}

// QueryString returns the version parameters to append to a bkn-backend URL that already
// has a query string: the pinned version once known, the requested one before that.
func (p *KNVersionPin) QueryString() string {
	if p == nil {
		return ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	version, revision := p.requested, p.requestedRevision
	if p.version != "" {
		version, revision = p.version, ""
		if p.version == KNVersionDraft {
			revision = p.revision
		}
	}
	if version == "" {
		return ""
	}
	query := "&kn_version=" + url.QueryEscape(version)
	if revision != "" {
		query += "&draft_revision=" + url.QueryEscape(revision)
	}
	return query
}

// Observe records the version bkn-backend served a read from. The first answer fixes the
// pin; a later answer for another version means the query would mix two versions.
// An empty version (a network without versioning, or never published) fixes nothing.
func (p *KNVersionPin) Observe(version, revision string) error {
	if p == nil || version == "" {
		return nil
	}
	if version != KNVersionDraft {
		revision = ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.version == "" {
		p.version, p.revision = version, revision
		return nil
	}
	if p.version != version || p.revision != revision {
		return &KNVersionChangedError{Pinned: p.label(), Read: labelOf(version, revision)}
	}
	return nil
}

// ReadsRunningVersion reports whether the query asked for whatever version is running.
func (p *KNVersionPin) ReadsRunningVersion() bool {
	return p == nil || p.requested == "" || p.requested == KNVersionRunning
}

// UsesCallerIdentity reports whether data has to be read with the caller's own identity.
// Only the running version, as it was when the query started, is read through the
// knowledge network proxy: the proxy's grants cover that version and no other. The draft and
// history versions are read as the caller, so Vega checks the caller's own permissions.
// A request that asked for the running version reads through the proxy whether or not the
// network has versions (an unversioned network is just main); any other request that is
// not resolved to a running version fails safe to the caller identity.
func (p *KNVersionPin) UsesCallerIdentity() bool {
	if p == nil {
		return false
	}
	return !p.ReadsRunningVersion()
}

// ProxyBranch is the branch to resolve the proxy for: "v<N>" for a pinned running version
// and "" (the main branch) for a network without versioning or one never published.
func (p *KNVersionPin) ProxyBranch() string {
	if p == nil {
		return ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.version == "" || p.version == KNVersionDraft {
		return ""
	}
	return "v" + p.version
}

// Token names the version that was read, for cursors and cached results; "" for a network
// without versioning, whose proxy model version stays the token.
func (p *KNVersionPin) Token() string {
	if p == nil {
		return ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return labelOf(p.version, p.revision)
}

func (p *KNVersionPin) label() string { return labelOf(p.version, p.revision) }

func labelOf(version, revision string) string {
	switch version {
	case "":
		return ""
	case KNVersionDraft:
		return KNVersionDraft + "@" + revision
	}
	return "v" + version
}

// CallerIdentityModelPrefix marks an action execution that runs with the caller's own
// identity (the draft or a history version) instead of the knowledge network proxy. It
// prefixes the version token stored in the execution's proxy model version, so no schema
// change is needed and an execution without both a proxy and this mark still fails closed.
const CallerIdentityModelPrefix = "caller:"

type callerIdentityExecutionKey struct{}

// WithCallerIdentityExecution marks the context of an action execution that runs as the caller.
func WithCallerIdentityExecution(ctx context.Context) context.Context {
	return context.WithValue(ctx, callerIdentityExecutionKey{}, true)
}

// CallerIdentityExecutionFromContext reports whether the action runs as the caller.
func CallerIdentityExecutionFromContext(ctx context.Context) bool {
	marked, _ := ctx.Value(callerIdentityExecutionKey{}).(bool)
	return marked
}

// KNVersionChangedError reports that one query saw two different versions of a network.
type KNVersionChangedError struct {
	Pinned string
	Read   string
}

func (e *KNVersionChangedError) Error() string {
	return fmt.Sprintf("knowledge network version changed during the query: pinned %s, read %s", e.Pinned, e.Read)
}
