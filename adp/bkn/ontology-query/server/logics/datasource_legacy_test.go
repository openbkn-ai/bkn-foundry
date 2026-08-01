// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package logics_test

import (
	"context"
	"net/http"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	oerrors "ontology-query/errors"
	"ontology-query/interfaces"
	"ontology-query/logics"
)

func TestLegacyDataViewBinding(t *testing.T) {
	Convey("legacy data_view bindings are rejected with a clear 400", t, func() {
		ctx := context.Background()

		So(logics.IsLegacyDataViewBinding(""), ShouldBeTrue)
		So(logics.IsLegacyDataViewBinding(interfaces.DATA_SOURCE_TYPE_DATA_VIEW), ShouldBeTrue)
		So(logics.IsLegacyDataViewBinding(interfaces.DATA_SOURCE_TYPE_RESOURCE), ShouldBeFalse)

		err := logics.LegacyDataViewBindingError(ctx, "object_type", "sales_order")
		So(err, ShouldNotBeNil)
		So(err.HTTPCode, ShouldEqual, http.StatusBadRequest)
		So(err.BaseError.ErrorCode, ShouldEqual, oerrors.OntologyQuery_UnsupportedLegacyDataSourceBinding)
	})
}
