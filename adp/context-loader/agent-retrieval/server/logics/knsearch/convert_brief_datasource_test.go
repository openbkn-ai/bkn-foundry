// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knsearch

import (
	"testing"

	"github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/interfaces"
)

func TestConvertObjectTypesToLocal_BriefKeepsDataSource(t *testing.T) {
	convey.Convey("brief keeps data_source (run_sql needs id) but drops primary_keys/tags/comment", t, func() {
		s := &localSearchImpl{}
		obj := &interfaces.ObjectType{
			ID:          "ot_order",
			Name:        "order",
			Tags:        []string{"t1"},
			Comment:     "订单",
			DataSource:  &interfaces.ResourceInfo{Type: "data_view", ID: "RID1", Name: "v"},
			PrimaryKeys: []string{"id"},
			DataProperties: []*interfaces.DataProperty{{
				Name:    "amount",
				Type:    "double",
				Comment: "金额",
			}},
		}

		brief := s.convertObjectTypesToLocal([]*interfaces.ObjectType{obj}, true, false)
		convey.So(len(brief), convey.ShouldEqual, 1)
		convey.So(brief[0].DataSource, convey.ShouldNotBeNil)
		convey.So(brief[0].DataSource.ID, convey.ShouldEqual, "RID1")
		convey.So(brief[0].PrimaryKeys, convey.ShouldBeEmpty)
		convey.So(brief[0].Tags, convey.ShouldBeEmpty)
		convey.So(brief[0].ConceptType, convey.ShouldEqual, "")
		convey.So(brief[0].DataProperties[0].Name, convey.ShouldEqual, "amount")
		convey.So(brief[0].DataProperties[0].Comment, convey.ShouldEqual, "") // brief 砍属性备注

		full := s.convertObjectTypesToLocal([]*interfaces.ObjectType{obj}, false, false)
		convey.So(full[0].DataSource, convey.ShouldNotBeNil)
		convey.So(full[0].DataSource.ID, convey.ShouldEqual, "RID1")
		convey.So(full[0].PrimaryKeys, convey.ShouldResemble, []string{"id"})
		convey.So(full[0].ConceptType, convey.ShouldEqual, "object_type")
		convey.So(full[0].DataProperties[0].Comment, convey.ShouldEqual, "金额")
	})
}
