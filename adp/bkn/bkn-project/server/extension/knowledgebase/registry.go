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
	Source   Source
	Concepts Concepts
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
