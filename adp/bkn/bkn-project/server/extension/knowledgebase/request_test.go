// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledgebase

import (
	"context"
	"fmt"
	. "github.com/smartystreets/goconvey/convey"
	"testing"
)

func TestOperatorAuthorizationStaysRequestLocal(t *testing.T) {
	Convey("Interactive credentials are explicit, redacted, and not inherited by unrelated requests", t, func() {
		parent := context.Background()
		ctx := WithOperatorAuthorization(parent, "Bearer synthetic-test-token")
		So(OperatorAuthorization(ctx), ShouldEqual, "Bearer synthetic-test-token")
		So(OperatorAuthorization(parent), ShouldBeEmpty)
		So(fmt.Sprint(ctx), ShouldNotContainSubstring, "synthetic-test-token")
		So(fmt.Sprintf("%#v", ctx), ShouldNotContainSubstring, "synthetic-test-token")
		_, err := (UnavailableSource{}).SummarizeFiles(ctx, SourceScope{}, FileQuery{})
		So(err, ShouldEqual, ErrUnavailable)
	})
}
