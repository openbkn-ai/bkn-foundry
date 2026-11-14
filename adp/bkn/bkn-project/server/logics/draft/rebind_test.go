// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

// vegaWorld is what vega has: catalogs by id with their names, and tables with their structure.
type vegaWorld struct {
	catalogs map[string]string
	tables   []*interfaces.VegaTable
	// lists counts the catalogs listed.
	lists int
	// failLists makes that many listings fail as a passing vega error would.
	failLists int
}

func (w *vegaWorld) table(id string) *interfaces.VegaTable {
	for _, t := range w.tables {
		if t.ID == id {
			return t
		}
	}
	return nil
}

// mock answers as vega would for the world: anything not in it is gone.
func (w *vegaWorld) mock(ctrl *gomock.Controller) *pmock.MockVegaAccess {
	va := pmock.NewMockVegaAccess(ctrl)
	va.EXPECT().MissingCatalogs(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ interfaces.VegaAccount, ids []string) ([]string, error) {
			return slices.DeleteFunc(slices.Clone(ids), func(id string) bool { _, ok := w.catalogs[id]; return ok }), nil
		}).AnyTimes()
	va.EXPECT().CatalogNames(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ interfaces.VegaAccount, ids []string) (map[string]string, error) {
			names := map[string]string{}
			for _, id := range ids {
				if name, ok := w.catalogs[id]; ok {
					names[id] = name
				}
			}
			return names, nil
		}).AnyTimes()
	va.EXPECT().MissingTables(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ interfaces.VegaAccount, ids []string) ([]string, error) {
			return slices.DeleteFunc(slices.Clone(ids), func(id string) bool { return w.table(id) != nil }), nil
		}).AnyTimes()
	va.EXPECT().ListTables(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ interfaces.VegaAccount, catalogID string) ([]*interfaces.VegaTable, error) {
			w.lists++
			if w.failLists > 0 {
				w.failLists--
				return nil, errors.New("vega-backend returned HTTP 503")
			}
			if _, ok := w.catalogs[catalogID]; !ok {
				return nil, errors.New("vega-backend returned HTTP 404")
			}
			listed := []*interfaces.VegaTable{}
			for _, t := range w.tables {
				if t.CatalogID == catalogID {
					// Listing leaves the structure out.
					listed = append(listed, &interfaces.VegaTable{ID: t.ID, CatalogID: t.CatalogID, Name: t.Name})
				}
			}
			return listed, nil
		}).AnyTimes()
	va.EXPECT().GetTables(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ interfaces.VegaAccount, ids []string) ([]*interfaces.VegaTable, error) {
			read := []*interfaces.VegaTable{}
			for _, id := range ids {
				if t := w.table(id); t != nil {
					read = append(read, t)
				}
			}
			return read, nil
		}).AnyTimes()
	return va
}

// vegaTableOf is a table of the catalog with columns of strings.
func vegaTableOf(id, catalogID, name, sourceName string, columns ...string) *interfaces.VegaTable {
	t := &interfaces.VegaTable{ID: id, CatalogID: catalogID, Name: name, SourceName: sourceName}
	for _, c := range columns {
		t.Columns = append(t.Columns, interfaces.VegaColumn{Name: c, Type: "string"})
	}
	return t
}

// reinstalledWorld is the sample catalog deleted and created again: the same names, new ids.
func reinstalledWorld() *vegaWorld {
	return &vegaWorld{
		catalogs: map[string]string{"c_new": "sample", "c_en": "sample_en"},
		tables: []*interfaces.VegaTable{
			vegaTableOf("new_orders", "c_new", "Orders", "orders", "order_id", "customer_id", "status", "channel"),
			vegaTableOf("new_customers", "c_new", "Customers", "customers", "customer_id"),
			vegaTableOf("new_products", "c_new", "Products", "products", "product_id"),
			vegaTableOf("new_audit_log", "c_new", "Audit log", "audit_log", "log_id"),
			vegaTableOf("new_order_lines", "c_new", "Order lines", "order_lines", "order_id", "product_id"),
			// Another catalog has tables of the same physical names.
			vegaTableOf("en_customers", "c_en", "Customers", "customers", "customer_id"),
		},
	}
}

// recordedDraft is a draft whose tables, in the catalog "sample", recorded their names.
func recordedDraft() *interfaces.Draft {
	d := confirmableDraft()
	for _, ref := range draftTableRefs(&d.Items) {
		ref.table.CatalogID, ref.table.CatalogName, ref.table.SourceName = "cat", "sample", ref.table.Name
	}
	d.Items.Inputs = &interfaces.DraftInputs{CatalogIDs: []string{"cat"}, Tables: []interfaces.DraftInputTable{
		{ResourceID: "res_customers", Fingerprint: "f_customers"}, {ResourceID: "res_kept", Fingerprint: "f_kept"},
	}}
	return d
}

func Test_RebindMatches(t *testing.T) {
	Convey("Test finding a gone table again by catalog name and table name\n", t, func() {
		w := reinstalledWorld()
		listed := map[string][]*interfaces.VegaTable{}
		for _, t := range w.tables {
			listed[t.CatalogID] = append(listed[t.CatalogID], t)
		}
		catalogs := []string{"c_new", "c_en"}

		Convey("The catalog name picks the catalog; the physical name the table\n", func() {
			gone := &interfaces.DraftTable{ResourceID: "res_customers", Name: "客户", CatalogID: "cat",
				CatalogName: "sample", SourceName: "customers"}
			m := rebindMatches([]*interfaces.DraftTable{gone}, catalogs, w.catalogs, listed, nil)
			So(m["res_customers"].ID, ShouldEqual, "new_customers")
		})

		Convey("No table of the name in the catalog of the name: none\n", func() {
			gone := &interfaces.DraftTable{ResourceID: "res_x", Name: "x", CatalogID: "cat", CatalogName: "sample", SourceName: "suppliers"}
			So(rebindMatches([]*interfaces.DraftTable{gone}, catalogs, w.catalogs, listed, nil), ShouldBeEmpty)
			gone = &interfaces.DraftTable{ResourceID: "res_x", Name: "x", CatalogID: "cat", CatalogName: "gone", SourceName: "customers"}
			So(rebindMatches([]*interfaces.DraftTable{gone}, catalogs, w.catalogs, listed, nil), ShouldBeEmpty)
		})

		Convey("A draft that did not record the catalog name matches only a table of one catalog\n", func() {
			gone := &interfaces.DraftTable{ResourceID: "res_customers", Name: "customers", CatalogID: "cat"}
			So(rebindMatches([]*interfaces.DraftTable{gone}, catalogs, w.catalogs, listed, nil), ShouldBeEmpty)
			gone = &interfaces.DraftTable{ResourceID: "res_products", Name: "products", CatalogID: "cat"}
			So(rebindMatches([]*interfaces.DraftTable{gone}, catalogs, w.catalogs, listed, nil)["res_products"].ID,
				ShouldEqual, "new_products")
		})

		Convey("A table created again in its own catalog, which is still there, is looked for there only\n", func() {
			gone := &interfaces.DraftTable{ResourceID: "res_customers", Name: "Customers", CatalogID: "c_en"}
			So(rebindMatches([]*interfaces.DraftTable{gone}, catalogs, w.catalogs, listed, nil)["res_customers"].ID,
				ShouldEqual, "en_customers")
		})
	})
}

func Test_RebindOnStatus(t *testing.T) {
	Convey("Test binding gone tables again by name when the owner reads the status\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		da := pmock.NewMockDraftAccess(ctrl)
		ca := pmock.NewMockCheckAccess(ctrl)
		ps := pmock.NewMockProjectService(ctrl)
		ls := pmock.NewMockLineageService(ctrl)
		rqa := pmock.NewMockRequirementAccess(ctrl)
		rqa.EXPECT().GetRequirement(gomock.Any(), "p1").Return(nil, false, nil).AnyTimes()
		ca.EXPECT().GetCheck(gomock.Any(), "p1").Return(nil, false, nil).AnyTimes()
		ls.EXPECT().PreflightDefinitions(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(&interfaces.DefinitionsPreflight{}, nil).AnyTimes()
		project := &interfaces.ProjectEntry{Project: &interfaces.Project{
			ID: "p1", KNID: "supply", Branch: "project/p1", Owner: caller, CatalogIDs: []string{"c_new", "c_en"},
		}}
		ps.EXPECT().GetProjectForStatus(gomock.Any(), token, "p1", gomock.Any()).Return(project, false, nil).AnyTimes()
		w := reinstalledWorld()
		svc := &draftService{setting: testSetting, da: da, ca: ca, ps: ps, ls: ls, rqa: rqa, va: w.mock(ctrl)}
		ctx := context.Background()

		// The draft as the database holds it: written drafts are read back through JSON.
		var stored *interfaces.Draft
		store := func(d *interfaces.Draft) {
			data, err := json.Marshal(d)
			So(err, ShouldBeNil)
			stored = &interfaces.Draft{}
			So(json.Unmarshal(data, stored), ShouldBeNil)
		}
		da.EXPECT().GetDraft(gomock.Any(), "p1").DoAndReturn(func(context.Context, string) (*interfaces.Draft, bool, error) {
			copied := *stored
			store(&copied)
			return stored, true, nil
		}).AnyTimes()
		var writes int
		da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, d *interfaces.Draft, read int64) (bool, error) {
				writes++
				store(d)
				return true, nil
			}).AnyTimes()

		Convey("A reinstalled catalog: every table is bound again, decisions and mappings kept\n", func() {
			store(recordedDraft())

			s, err := svc.GetStatus(ctx, token, "p1", caller)

			So(err, ShouldBeNil)
			So(writes, ShouldEqual, 1)
			So(s.DataSources.Tables.State, ShouldEqual, interfaces.DATA_SOURCE_OK)
			So(s.DataSources.Reconnected, ShouldEqual, 5)
			So(s.Blockers, ShouldBeEmpty)
			So(action(s, interfaces.ACTION_CONFIRM_DRAFT).Allowed, ShouldBeTrue)
			customers := findObjectType(&stored.Items, "customers")
			So(customers.Table, ShouldResemble, interfaces.DraftTable{ResourceID: "new_customers", Name: "Customers",
				CatalogID: "c_new", CatalogName: "sample", SourceName: "customers"})
			So(customers.PrimaryKeys, ShouldResemble, []string{"customer_id"})
			So(customers.Properties[0].MappedField, ShouldEqual, "customer_id")
			So(findRelationType(&stored.Items, "orders_to_products").LinkTable.ResourceID, ShouldEqual, "new_order_lines")
			// The decision made before stays, and each rebinding is one decision more.
			So(stored.Items.Decisions[0].Action, ShouldEqual, interfaces.DRAFT_DECISION_UNADOPT)
			So(stored.Items.Decisions, ShouldHaveLength, 6)
			So(stored.Items.Inputs.CatalogIDs, ShouldResemble, []string{"c_new"})
			So(stored.Items.Inputs.Tables[0], ShouldResemble,
				interfaces.DraftInputTable{ResourceID: "new_customers", Fingerprint: "f_customers"})

			Convey("Reading again binds nothing more, and still tells what was bound again\n", func() {
				s, err := svc.GetStatus(ctx, token, "p1", caller)

				So(err, ShouldBeNil)
				So(writes, ShouldEqual, 1)
				So(s.DataSources.Reconnected, ShouldEqual, 5)
			})
		})

		Convey("A column the item maps that the table lost is a source change of the item bound again\n", func() {
			w.tables[0] = vegaTableOf("new_orders", "c_new", "Orders", "orders", "order_id", "customer_id", "status")
			store(recordedDraft())

			s, err := svc.GetStatus(ctx, token, "p1", caller)

			So(err, ShouldBeNil)
			So(writes, ShouldEqual, 1)
			So(s.DataSources.Tables.State, ShouldEqual, interfaces.DATA_SOURCE_OK)
			So(s.Stale, ShouldContain, interfaces.STALE_SOURCE_CHANGED)
			changes, err := svc.va.GetTables(ctx, caller, []string{"new_orders"})
			So(err, ShouldBeNil)
			removed := draftObjectTypeChanges(&stored.Items, findObjectType(&stored.Items, "orders"), changes[0], nil, "")
			So(removed, ShouldHaveLength, 1)
			So(removed[0].Change, ShouldEqual, interfaces.SOURCE_CHANGE_REMOVED)
			So(removed[0].Field, ShouldEqual, "channel")
		})

		Convey("A table with no same-name table stays gone, with its physical name, and nothing is written\n", func() {
			w.tables = slices.DeleteFunc(w.tables, func(t *interfaces.VegaTable) bool { return t.ID == "new_customers" })
			store(recordedDraft())

			s, err := svc.GetStatus(ctx, token, "p1", caller)

			So(err, ShouldBeNil)
			// The tables that were found are bound again; the one not found is still named.
			So(writes, ShouldEqual, 1)
			So(s.DataSources.Tables.State, ShouldEqual, interfaces.DATA_SOURCE_MISSING)
			So(s.DataSources.Tables.Affected, ShouldResemble, []interfaces.ProjectStatusAffected{{
				Kind: interfaces.DRAFT_KIND_OBJECT_TYPES, ID: "customers", Name: "Customers", Table: "customers",
				SourceTable: "customers",
			}})
			So(findObjectType(&stored.Items, "customers").Table.ResourceID, ShouldEqual, "res_customers")
			So(action(s, interfaces.ACTION_GENERATE_DRAFT).Allowed, ShouldBeTrue)

			Convey("Reading again writes nothing\n", func() {
				_, err := svc.GetStatus(ctx, token, "p1", caller)
				So(err, ShouldBeNil)
				So(writes, ShouldEqual, 1)
			})
		})

		Convey("A listing that fails is not remembered as no table found: the next read binds again\n", func() {
			w.failLists = 2
			store(recordedDraft())

			s, err := svc.GetStatus(ctx, token, "p1", caller)

			So(err, ShouldBeNil)
			So(writes, ShouldEqual, 0)
			So(s.DataSources.Tables.State, ShouldEqual, interfaces.DATA_SOURCE_MISSING)

			s, err = svc.GetStatus(ctx, token, "p1", caller)

			So(err, ShouldBeNil)
			So(writes, ShouldEqual, 1)
			So(s.DataSources.Tables.State, ShouldEqual, interfaces.DATA_SOURCE_OK)
		})

		Convey("A deleted catalog the project still lists does not stop the others being looked in\n", func() {
			project.CatalogIDs = []string{"cat", "c_new", "c_en"}
			defer func() { project.CatalogIDs = []string{"c_new", "c_en"} }()
			store(recordedDraft())

			s, err := svc.GetStatus(ctx, token, "p1", caller)

			So(err, ShouldBeNil)
			So(writes, ShouldEqual, 1)
			So(s.DataSources.Tables.State, ShouldEqual, interfaces.DATA_SOURCE_OK)
		})

		Convey("A table another item binds already is not bound a second time\n", func() {
			d := recordedDraft()
			findObjectType(&d.Items, "audit_log").Table.ResourceID = "new_customers"
			store(d)

			s, err := svc.GetStatus(ctx, token, "p1", caller)

			So(err, ShouldBeNil)
			So(writes, ShouldEqual, 1)
			So(findObjectType(&stored.Items, "customers").Table.ResourceID, ShouldEqual, "res_customers")
			So(findObjectType(&stored.Items, "orders").Table.ResourceID, ShouldEqual, "new_orders")
			So(s.DataSources.Tables.Missing, ShouldEqual, 1)
		})

		Convey("A table looked for in vain is not looked for again on every read\n", func() {
			w.tables = w.tables[5:]
			store(recordedDraft())

			_, err := svc.GetStatus(ctx, token, "p1", caller)
			So(err, ShouldBeNil)
			first := w.lists
			_, err = svc.GetStatus(ctx, token, "p1", caller)

			So(err, ShouldBeNil)
			So(writes, ShouldEqual, 0)
			// The second read lists only what the catalogs hint lists, once per catalog.
			So(w.lists-first, ShouldEqual, len(project.CatalogIDs))
			So(first, ShouldBeGreaterThan, len(project.CatalogIDs))
		})

		Convey("Someone not the owner reads the tables gone, and nothing is written\n", func() {
			store(recordedDraft())
			other := interfaces.AccountInfo{ID: "u2", Type: "user"}

			s, err := svc.GetStatus(ctx, token, "p1", other)

			So(err, ShouldBeNil)
			So(writes, ShouldEqual, 0)
			So(s.DataSources.Tables.Missing, ShouldEqual, 4)
		})

		Convey("A draft whose tables are there is not written\n", func() {
			d := recordedDraft()
			for _, ref := range draftTableRefs(&d.Items) {
				ref.table.ResourceID = "new_" + ref.table.Name
			}
			store(d)

			s, err := svc.GetStatus(ctx, token, "p1", caller)

			So(err, ShouldBeNil)
			So(writes, ShouldEqual, 0)
			So(s.DataSources.Tables.State, ShouldEqual, interfaces.DATA_SOURCE_OK)
			So(s.DataSources.Reconnected, ShouldEqual, 0)
		})
	})
}

// action is the status's next action of the name.
func action(s *interfaces.ProjectStatus, name string) *interfaces.ProjectStatusNextAction {
	for i := range s.NextActions {
		if s.NextActions[i].Action == name {
			return &s.NextActions[i]
		}
	}
	return nil
}

func Test_DraftingRecordsNames(t *testing.T) {
	Convey("Test drafting records the catalog's name and the physical table's with each table\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		svc := &draftService{setting: testSetting, va: reinstalledWorld().mock(ctrl)}

		tables, _, err := svc.readCatalogs(context.Background(), []string{"c_new"}, caller)

		So(err, ShouldBeNil)
		So(tables, ShouldNotBeEmpty)
		So(draftTable(tables[0]), ShouldResemble, interfaces.DraftTable{ResourceID: "new_orders", Name: "Orders",
			CatalogID: "c_new", CatalogName: "sample", SourceName: "orders"})
	})
}

func Test_DraftAfterCatalogRecreated(t *testing.T) {
	Convey("Test drafting again after the network's catalog was created again under its name\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		da := pmock.NewMockDraftAccess(ctrl)
		va := pmock.NewMockVegaAccess(ctrl)
		ps := pmock.NewMockProjectService(ctrl)
		gs := pmock.NewMockGitStore(ctrl)
		setting := testSetting
		setting.QueryTimeout = 60
		setting.Timeout = time.Minute
		svc := &draftService{setting: setting, da: da, va: va, ps: ps, gs: gs, ca: pmock.NewMockCheckAccess(ctrl),
			ira: &fakeIDRegistry{}, run: func(fn func()) { fn() }}
		project := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", KNID: "kn1", Branch: "project/p1",
			CatalogIDs: []string{"c1"}}}
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil).AnyTimes()
		va.EXPECT().MissingCatalogs(gomock.Any(), caller, []string{"c1"}).Return(nil, nil).AnyTimes()
		va.EXPECT().CatalogNames(gomock.Any(), caller, []string{"c1"}).Return(map[string]string{"c1": "sales"}, nil)
		da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, nil).AnyTimes()
		da.EXPECT().StartDraft(gomock.Any(), gomock.Any(), gomock.Any(), false).Return(true, nil)
		// The project branch still names the table the network's sales orders read by its old id.
		network := existingNetwork()
		for path, data := range network {
			network[path] = bytes.ReplaceAll(data, []byte("t_orders"), []byte("t_orders_old"))
		}
		gs.EXPECT().ReadTree(gomock.Any(), "kn1", "refs/heads/project/p1").Return(network, nil)
		orders := table("t_orders", "c1", "orders", 100, []string{"order_id"}, col("order_id", "string", ""),
			col("customer_id", "string", ""), col("region_code", "string", ""), col("placed_at", "datetime", ""))
		orders.SourceName = "orders"
		customers := table("t_customers", "c1", "customers", 100, []string{"customer_id"},
			col("customer_id", "string", ""), col("customer_name", "string", ""))
		va.EXPECT().ListTables(gomock.Any(), caller, "c1").Return([]*interfaces.VegaTable{
			{ID: "t_orders", Name: "orders", CatalogID: "c1"}, {ID: "t_customers", Name: "customers", CatalogID: "c1"},
		}, nil)
		va.EXPECT().GetTables(gomock.Any(), caller, []string{"t_orders", "t_customers"}).
			Return([]*interfaces.VegaTable{orders, customers}, nil)
		// The old id is gone; the regions table, outside the catalogs, is there.
		va.EXPECT().MissingTables(gomock.Any(), caller, []string{"t_regions", "t_orders_old"}).
			Return([]string{"t_orders_old"}, nil)
		va.EXPECT().Query(gomock.Any(), caller, gomock.Any(), 60).Return([]map[string]any{{"total": float64(100),
			"distinct_values": float64(100), "nulls": float64(0), "orphans": float64(0)}}, nil).AnyTimes()
		var finished *interfaces.Draft
		da.EXPECT().FinishDraft(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, d *interfaces.Draft) error { finished = d; return nil })

		_, err := svc.GenerateDraft(context.Background(), token, "p1", caller, false)

		So(err, ShouldBeNil)
		So(finished.Error, ShouldEqual, "")
		items := finished.Items
		// The network's table is known by its name and not drafted a second time.
		So(items.ObjectTypes, ShouldHaveLength, 3)
		So(findObjectType(&items, "orders"), ShouldBeNil)
		order := findObjectType(&items, "supply_sales_order")
		So(order.Existing, ShouldBeTrue)
		So(order.Table.ResourceID, ShouldEqual, "t_orders")
		So(items.Inputs.ExistingTables, ShouldResemble, []string{"orders"})
		So(items.Inputs.Tables, ShouldHaveLength, 1)
		So(findObjectType(&items, "customers").Table.CatalogName, ShouldEqual, "sales")
	})
}
