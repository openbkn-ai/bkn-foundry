// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

type fakeNames map[interfaces.AccountInfo]string

func (f fakeNames) Names(_ context.Context, _ []interfaces.AccountInfo) map[interfaces.AccountInfo]string {
	return f
}

func Test_NameAccounts(t *testing.T) {
	Convey("Test replies name owners and publishers\n", t, func() {
		alice := interfaces.AccountInfo{ID: "u1", Type: "user"}
		bob := interfaces.AccountInfo{ID: "u2", Type: "user"}
		handler := &restHandler{ana: fakeNames{alice: "Alice"}}

		Convey("each project gets its owner's name, an unknown owner none\n", func() {
			named := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", Owner: alice}}
			unknown := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p2", Owner: bob}}
			handler.nameOwners(context.Background(), named, unknown, nil)
			So(named.OwnerName, ShouldEqual, "Alice")
			So(unknown.OwnerName, ShouldEqual, "")
		})

		Convey("versions and the prepared release get their publisher's name, the stored release untouched\n", func() {
			stored := &interfaces.Release{ID: "r1", Publisher: alice}
			versions := &interfaces.ProjectVersions{
				Prepared: stored,
				Entries: []interfaces.ProjectVersion{
					{KNVersion: interfaces.KNVersion{Version: 2, Publisher: alice}},
					{KNVersion: interfaces.KNVersion{Version: 1, Publisher: bob}},
				},
			}
			handler.namePublishers(context.Background(), versions)
			So(versions.Entries[0].PublisherName, ShouldEqual, "Alice")
			So(versions.Entries[1].PublisherName, ShouldEqual, "")
			So(versions.Prepared.PublisherName, ShouldEqual, "Alice")
			So(stored.PublisherName, ShouldEqual, "")
		})

		Convey("without a directory nothing is named\n", func() {
			entry := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", Owner: alice}}
			(&restHandler{}).nameOwners(context.Background(), entry)
			So(entry.OwnerName, ShouldEqual, "")
		})
	})
}
