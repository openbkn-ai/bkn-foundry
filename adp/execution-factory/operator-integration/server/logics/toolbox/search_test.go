package toolbox

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
)

// TestSearchToolsFailClosed covers the security boundary of #1261: an empty whitelist means
// "nothing is in scope", never "do not filter". Both spellings are locked because the difference
// between them is exactly how a platform-wide leak would slip in.
func TestSearchToolsFailClosed(t *testing.T) {
	Convey("白名单为空时不查库、返回空", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		// No EXPECT on the tool store: the controller fails the test if a query is issued.
		service := &ToolServiceImpl{ToolDB: mocks.NewMockIToolDB(ctrl), Logger: logger.DefaultLogger()}

		Convey("tool_refs 为空数组", func() {
			resp, err := service.SearchTools(context.Background(), &interfaces.SearchToolsReq{
				Query: "sql", ToolRefs: []string{},
			})
			So(err, ShouldBeNil)
			So(resp.Entries, ShouldBeEmpty)
		})

		Convey("tool_refs 字段缺失", func() {
			resp, err := service.SearchTools(context.Background(), &interfaces.SearchToolsReq{Query: "sql"})
			So(err, ShouldBeNil)
			So(resp.Entries, ShouldBeEmpty)
		})

		Convey("tool_refs 全是格式非法的引用", func() {
			resp, err := service.SearchTools(context.Background(), &interfaces.SearchToolsReq{
				Query: "sql", ToolRefs: []string{"no-slash", "/", " / ", "box-only/"},
			})
			So(err, ShouldBeNil)
			So(resp.Entries, ShouldBeEmpty)
		})
	})
}

// TestSearchToolsPairScoping locks that a whitelist entry admits one (box, tool) pair rather than
// a bare tool id. Tool ids are scoped to their box, so filtering on the id alone would let a
// caller reach a same-id tool in a box it never bound.
func TestSearchToolsPairScoping(t *testing.T) {
	Convey("白名单按 (box, tool) 配对收窄", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		toolDB := mocks.NewMockIToolDB(ctrl)
		// "dangling/gone" is well formed, so its id still goes to the store; it simply matches
		// no row. Only malformed refs are dropped before the query.
		toolDB.EXPECT().SearchToolsByIDs(gomock.Any(), []string{"tool-1", "gone"}, "sql", 0).
			Return([]*model.ToolDB{
				{BoxID: "box-1", ToolID: "tool-1", Name: "run_sql", Description: "执行 SQL", Status: "published"},
				{BoxID: "box-other", ToolID: "tool-1", Name: "run_sql", Description: "别的箱里同 id 的工具"},
				{BoxID: "box-1", ToolID: "tool-1", Name: "已删除", IsDeleted: true},
			}, nil)

		service := &ToolServiceImpl{ToolDB: toolDB, Logger: logger.DefaultLogger()}

		resp, err := service.SearchTools(context.Background(), &interfaces.SearchToolsReq{
			Query: "sql", ToolRefs: []string{"box-1/tool-1", "box-1/tool-1", "dangling/gone"},
		})

		So(err, ShouldBeNil)
		So(len(resp.Entries), ShouldEqual, 1)
		So(resp.Entries[0].BoxID, ShouldEqual, "box-1")
		So(resp.Entries[0].ToolID, ShouldEqual, "tool-1")
		So(resp.Entries[0].MatchedBy, ShouldEqual, interfaces.ToolMatchedByLike)
	})
}

// TestSearchToolsTopK keeps the cap on the pair-filtered results. Applying it in SQL instead would
// let same-id tools from other boxes fill the page and crowd out the caller's own.
func TestSearchToolsTopK(t *testing.T) {
	Convey("top_k 作用在配对过滤之后", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		toolDB := mocks.NewMockIToolDB(ctrl)
		toolDB.EXPECT().SearchToolsByIDs(gomock.Any(), gomock.Any(), gomock.Any(), 0).
			Return([]*model.ToolDB{
				{BoxID: "other", ToolID: "tool-1", Name: "别的箱"},
				{BoxID: "other", ToolID: "tool-2", Name: "别的箱"},
				{BoxID: "box-1", ToolID: "tool-1", Name: "第一个"},
				{BoxID: "box-1", ToolID: "tool-2", Name: "第二个"},
			}, nil)

		service := &ToolServiceImpl{ToolDB: toolDB, Logger: logger.DefaultLogger()}

		resp, err := service.SearchTools(context.Background(), &interfaces.SearchToolsReq{
			ToolRefs: []string{"box-1/tool-1", "box-1/tool-2"}, TopK: 1,
		})

		So(err, ShouldBeNil)
		So(len(resp.Entries), ShouldEqual, 1)
		So(resp.Entries[0].Name, ShouldEqual, "第一个")
	})
}
