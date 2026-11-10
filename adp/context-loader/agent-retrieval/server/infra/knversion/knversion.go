// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package knversion carries the knowledge-network version one tool call reads.
//
// A Selector is created per tool call from the tool arguments and the MCP
// connection headers. The HTTP clients of bkn-backend and ontology-query add
// its parameters to every request and feed it every response, so that a tool
// which reads several times sees one consistent version: the first response
// pins the version (and, for a draft, the draft revision) for the later calls.
package knversion

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

const (
	// HeaderVersion and HeaderDraftRevision are the MCP connection headers that
	// supply defaults, and the response headers bkn-backend answers with.
	HeaderVersion       = "X-Kn-Version"
	HeaderDraftRevision = "X-Kn-Draft-Revision"

	// QueryVersion and QueryDraftRevision are the downstream query parameters.
	QueryVersion       = "kn_version"
	QueryDraftRevision = "draft_revision"

	Running = "running"
	Draft   = "draft"

	// CodeDraftRevisionMismatch is the bkn-backend error code that says the
	// draft moved on since the revision this call is pinned to.
	CodeDraftRevisionMismatch = "BknBackend.KNVersion.DraftRevisionMismatch"
)

// Selector is the requested version of one tool call plus what the first
// response pinned. It is safe for concurrent use.
type Selector struct {
	version       string // requested: "", running, draft or a positive integer
	draftRevision string // requested; only valid with draft
	fixedRevision bool   // the caller named the draft revision itself

	mu               sync.Mutex
	pinnedVersion    string
	pinnedRevision   string
	revisionMismatch bool
}

// ParamError is a caller mistake in the version parameters.
type ParamError struct{ Msg string }

func (e *ParamError) Error() string { return e.Msg }

// Resolve validates a requested version. An empty version means "not
// requested". draft_revision is only valid together with kn_version=draft.
func Resolve(version, draftRevision string) (*Selector, error) {
	version = strings.ToLower(strings.TrimSpace(version))
	draftRevision = strings.TrimSpace(draftRevision)
	if version != "" && version != Running && version != Draft {
		if n, err := strconv.Atoi(version); err != nil || n < 1 {
			return nil, &ParamError{"kn_version must be running, draft or a positive version number"}
		}
	}
	if draftRevision != "" {
		if version != Draft {
			return nil, &ParamError{"draft_revision is only valid with kn_version=draft"}
		}
		if n, err := strconv.Atoi(draftRevision); err != nil || n < 1 {
			return nil, &ParamError{"draft_revision must be a positive integer"}
		}
	}
	return &Selector{version: version, draftRevision: draftRevision, fixedRevision: draftRevision != ""}, nil
}

// Version is the requested version as the caller gave it ("" when not requested).
func (s *Selector) Version() string {
	if s == nil {
		return ""
	}
	return s.version
}

// ReadsRunning reports whether the call reads the running version, which is
// what no version and kn_version=running both mean.
func (s *Selector) ReadsRunning() bool {
	return s == nil || s.version == "" || s.version == Running
}

// RevisionFixed reports whether the caller pinned the draft revision itself.
func (s *Selector) RevisionFixed() bool { return s != nil && s.fixedRevision }

// Renew returns a fresh selector with the same request and nothing pinned.
func (s *Selector) Renew() *Selector {
	return &Selector{version: s.version, draftRevision: s.draftRevision, fixedRevision: s.fixedRevision}
}

// Params returns the query values to send: the pinned ones once a response has
// pinned them, the requested ones before that. Both empty means send nothing.
func (s *Selector) Params() (version, draftRevision string) {
	if s == nil {
		return "", ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pinnedVersion != "" {
		return s.pinnedVersion, s.pinnedRevision
	}
	return s.version, s.draftRevision
}

// Observe records a downstream response: it pins the version from the first
// response that names one, and notes a draft-revision mismatch.
func (s *Selector) Observe(status int, header http.Header, body []byte) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if status == http.StatusConflict && strings.Contains(string(body), CodeDraftRevisionMismatch) {
		s.revisionMismatch = true
		return
	}
	if status < 200 || status >= 300 || s.pinnedVersion != "" {
		return
	}
	v := strings.TrimSpace(header.Get(HeaderVersion))
	if v == "" {
		return
	}
	s.pinnedVersion = v
	if v == Draft {
		s.pinnedRevision = strings.TrimSpace(header.Get(HeaderDraftRevision))
		if s.pinnedRevision == "" {
			s.pinnedRevision = s.draftRevision
		}
	}
}

// RevisionMismatch reports whether a downstream call answered DraftRevisionMismatch.
func (s *Selector) RevisionMismatch() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.revisionMismatch
}

// Label is the version actually read, for trace records: "7", "draft@12",
// "draft", or "" when no response named a version (unversioned network).
func (s *Selector) Label() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pinnedVersion == Draft && s.pinnedRevision != "" {
		return Draft + "@" + s.pinnedRevision
	}
	return s.pinnedVersion
}

type ctxKey struct{}

// WithSelector stores the selector on ctx.
func WithSelector(ctx context.Context, s *Selector) context.Context {
	return context.WithValue(ctx, ctxKey{}, s)
}

// FromContext returns the selector of the current tool call, or nil.
func FromContext(ctx context.Context) *Selector {
	s, _ := ctx.Value(ctxKey{}).(*Selector)
	return s
}
