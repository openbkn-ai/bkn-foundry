// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package knowledgebase is the assembly seam for document sources and concept
// extraction. Core retains drafts, candidates, decisions and release handling.
package knowledgebase

import (
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement/socket"
	"github.com/openbkn-ai/licverify"
)

const Capability = "knowledge_base"

type Providers struct {
	RefreshSources RefreshSources
	RefreshTasks   RefreshTasks
	Publication    NetworkPublication
	Builds         BuildRuntime
	Datasets       Datasets
	Source         Source
	Concepts       Concepts
}

var registry = socket.New[Providers]("bkn-project knowledge base")

// Register is unconditional at assembly time, before app.Run freezes sockets.
// Licensing is evaluated per request, so importing a license needs no restart.
func Register(providers Providers) {
	if providers.Source == nil || providers.Concepts == nil {
		panic("knowledgebase: both source and concept providers are required")
	}
	registry.Add(Capability, Capability, licverify.EditionProfessional, providers)
}

func Lookup() (Providers, bool) {
	providers, installed := registry.Get(Capability)
	if !installed || !entitlement.AtLeast(licverify.EditionProfessional) {
		return Providers{Source: UnavailableSource{}, Concepts: UnavailableConcepts{}}, false
	}
	return providers, true
}

// WorkerAccess is what a background worker may do with journaled work.
type WorkerAccess int

const (
	// WorkerNotInstalled means this build has no knowledge-base providers.
	WorkerNotInstalled WorkerAccess = iota
	// WorkerRun means a licence is in force.
	WorkerRun
	// WorkerPause means no licence hub has answered. Work stays as it is, because
	// silence must not revoke a licensed deployment's builds.
	WorkerPause
	// WorkerClean means the answer is definitely no licence: none installed, past
	// grace or rejected. The worker may only revoke delegations and release
	// retention holds; normal operations keep using Lookup and its gate.
	WorkerClean
)

// ForWorker decides from a single licence reading, so a hub answering between
// two reads can never combine "not licensed yet" with "answered".
func ForWorker() (Providers, WorkerAccess) {
	providers, installed := registry.Get(Capability)
	if !installed {
		return Providers{}, WorkerNotInstalled
	}
	snapshot := entitlement.Snap()
	switch {
	case snapshot.Edition.AtLeast(licverify.EditionProfessional):
		return providers, WorkerRun
	case snapshot.Undetermined:
		return providers, WorkerPause
	}
	return providers, WorkerClean
}

// MissingProviders names what the assembly registered without, so a misconfigured
// deployment is reported at startup rather than on the first build. It is nil
// when this build has no knowledge-base extension at all.
func MissingProviders() []string {
	providers, installed := registry.Get(Capability)
	if !installed {
		return nil
	}
	missing := []string{}
	for _, provider := range []struct {
		name    string
		present bool
	}{
		{"builds", providers.Builds != nil},
		{"datasets", providers.Datasets != nil},
		{"publication", providers.Publication != nil},
		{"refresh sources", providers.RefreshSources != nil},
		{"refresh tasks", providers.RefreshTasks != nil},
	} {
		if !provider.present {
			missing = append(missing, provider.name)
		}
	}
	return missing
}

// Gate selects between complete routers before route matching, including method
// handling and canonical-path redirects. The extended router must contain the
// community routes too. An unlicensed build therefore leaks no extension route
// through a redirect, Allow header or a different not-found response.
// Authentication and project/source authorization remain required in handlers.
func Gate(extended, community http.Handler) http.Handler {
	if extended == nil || community == nil {
		panic("knowledgebase: both routers are required")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, available := Lookup(); available {
			extended.ServeHTTP(w, r)
			return
		}
		community.ServeHTTP(w, r)
	})
}

// ResetForTest is guarded by socket's test-binary check.
func ResetForTest() { registry.ResetForTest() }
