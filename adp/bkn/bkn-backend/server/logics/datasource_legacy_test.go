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

	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
	"bkn-backend/logics"
)

func TestLegacyDataViewBinding(t *testing.T) {
	Convey("legacy data_view bindings are flagged and rejected", t, func() {
		ctx := context.Background()

		ds := &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_DATA_VIEW, ID: "view-1"}
		logics.EnrichDataSourceBindingStatus(ds)
		So(ds.BindingAvailable, ShouldNotBeNil)
		So(*ds.BindingAvailable, ShouldBeFalse)
		So(ds.BindingIssue, ShouldEqual, logics.LegacyDataViewBindingIssue)

		err := logics.LegacyDataViewBindingError(ctx, berrors.BknBackend_ObjectType_InvalidParameter)
		So(err, ShouldNotBeNil)
		So(err.HTTPCode, ShouldEqual, http.StatusBadRequest)
		So(err.BaseError.ErrorCode, ShouldEqual, berrors.BknBackend_ObjectType_InvalidParameter)

		resourceDS := &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "res-1"}
		logics.MarkResourceBindingAvailable(resourceDS)
		So(resourceDS.BindingAvailable, ShouldNotBeNil)
		So(*resourceDS.BindingAvailable, ShouldBeTrue)
		So(resourceDS.BindingIssue, ShouldBeEmpty)

		logics.MarkResourceBindingUnavailable(resourceDS)
		So(*resourceDS.BindingAvailable, ShouldBeFalse)
		So(resourceDS.BindingIssue, ShouldEqual, logics.ResourceBindingNotFoundIssue)
	})
}
