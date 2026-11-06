// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledgebase

import (
	"context"
	"errors"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/licverify"
	. "github.com/smartystreets/goconvey/convey"
)

func reset(t *testing.T) {
	t.Helper()
	ResetForTest()
	entitlement.ResetForTest()
	t.Cleanup(func() { ResetForTest(); entitlement.ResetForTest() })
}

func TestCommunityDefaults(t *testing.T) {
	Convey("Community has no capability and its providers fail closed", t, func() {
		reset(t)
		providers, available := Lookup()
		So(available, ShouldBeFalse)
		So(entitlement.Assembled(), ShouldBeEmpty)
		ctx := context.Background()
		_, err := providers.Source.ListKnowledgeBases(ctx, SourceScope{}, PageQuery{})
		So(errors.Is(err, ErrUnavailable), ShouldBeTrue)
		_, err = providers.Source.ListCatalogs(ctx, SourceScope{}, CatalogQuery{})
		So(errors.Is(err, ErrUnavailable), ShouldBeTrue)
		_, err = providers.Source.ListFiles(ctx, SourceScope{}, FileQuery{})
		So(errors.Is(err, ErrUnavailable), ShouldBeTrue)
		_, err = providers.Source.ReadChunks(ctx, SourceScope{}, VersionRef{}, PageQuery{})
		So(errors.Is(err, ErrUnavailable), ShouldBeTrue)
		So(errors.Is(providers.Source.Hold(ctx, SourceScope{}, HoldRequest{}), ErrUnavailable), ShouldBeTrue)
		So(errors.Is(providers.Source.ReleaseHold(ctx, SourceScope{}, "hold"), ErrUnavailable), ShouldBeTrue)
		_, err = providers.Concepts.Extract(ctx, ConceptRequest{})
		So(errors.Is(err, ErrUnavailable), ShouldBeTrue)
	})
}

func TestRegistrationLifecycle(t *testing.T) {
	Convey("Registration uses knowledge_base and closes before serving", t, func() {
		reset(t)
		So(func() { Register(Providers{}) }, ShouldPanic)
		Register(Providers{Source: UnavailableSource{}, Concepts: UnavailableConcepts{}})
		caps := entitlement.Assembled()
		So(len(caps), ShouldEqual, 1)
		So(caps[0].Name, ShouldEqual, "knowledge_base")
		So(caps[0].MinEdition, ShouldEqual, licverify.EditionProfessional)
		So(func() { Register(Providers{Source: UnavailableSource{}, Concepts: UnavailableConcepts{}}) }, ShouldPanic)
		entitlement.Freeze()
		ResetForTest()
		So(func() { Register(Providers{Source: UnavailableSource{}, Concepts: UnavailableConcepts{}}) }, ShouldPanic)
	})
}
