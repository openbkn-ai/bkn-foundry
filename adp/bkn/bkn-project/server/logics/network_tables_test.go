// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logics

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

const regionsBKN = "---\ntype: object_type\nid: regions\nname: Regions\n---\n\n## ObjectType: Regions\n\n" +
	"### Data Source\n\n| Type | ID | Name |\n|------|----|------|\n| resource | t_regions | regions |\n"

func Test_WithNetworkTables(t *testing.T) {
	Convey("Test writing the tables the network reads now into the definitions a release leaves\n", t, func() {
		files := map[string][]byte{"object_types/regions.bkn": []byte(regionsBKN), "network.bkn": []byte("x")}

		Convey("an object type as main has it reads the network's table\n", func() {
			got := WithNetworkTables(files, files, map[string]string{"regions": "t_regions_new"})
			So(string(got["object_types/regions.bkn"]), ShouldContainSubstring, "| t_regions_new |")
			So(string(got["network.bkn"]), ShouldEqual, "x")
			// The files read are left as they were.
			So(string(files["object_types/regions.bkn"]), ShouldContainSubstring, "| t_regions |")
		})

		Convey("one the release changes, or the network does not name, stays as written\n", func() {
			base := map[string][]byte{"object_types/regions.bkn": []byte(regionsBKN + "\n")}
			got := WithNetworkTables(files, base, map[string]string{"regions": "t_regions_new"})
			So(string(got["object_types/regions.bkn"]), ShouldEqual, regionsBKN)
			got = WithNetworkTables(files, files, map[string]string{"other": "t_other"})
			So(string(got["object_types/regions.bkn"]), ShouldEqual, regionsBKN)
			So(WithNetworkTables(files, files, nil), ShouldEqual, files)
		})
	})
}
