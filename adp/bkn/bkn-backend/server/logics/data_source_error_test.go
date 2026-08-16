// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package logics

import (
	"context"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	. "github.com/smartystreets/goconvey/convey"
)

func TestUnsupportedDataSourceErrorsUseLocalizedDetails(t *testing.T) {
	Convey("Unsupported data source errors use the request locale\n", t, func() {
		Convey("Object type details use the Chinese catalog\n", func() {
			ctx := rest.WithLanguage(context.Background(), rest.SimplifiedChinese)
			httpErr := UnsupportedObjectTypeDataSourceError(ctx, "order", "table")
			So(httpErr.BaseError.ErrorDetails, ShouldEqual, "对象类 order 的数据来源类型 table 不支持，仅支持 resource。")
		})

		Convey("Relation type details use the English catalog\n", func() {
			ctx := rest.WithLanguage(context.Background(), rest.AmericanEnglish)
			httpErr := UnsupportedRelationBackingDataSourceError(ctx, "order_item", "table")
			So(httpErr.BaseError.ErrorDetails, ShouldEqual, "backing_data_source.type must be resource; received table.")
		})
	})
}
