// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logics

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func Test_FirstReleaseVersion(t *testing.T) {
	Convey("Test FirstReleaseVersion\n", t, func() {
		labelled := func(labels ...string) []*interfaces.KNVersion {
			versions := []*interfaces.KNVersion{}
			for i, l := range labels {
				versions = append(versions, &interfaces.KNVersion{Version: i + 1, Label: l})
			}
			return versions
		}

		Convey("A network without versions starts at 1.0.0\n", func() {
			So(FirstReleaseVersion(nil), ShouldEqual, "1.0.0")
		})

		Convey("Versions without x.y.z labels are a first version already: 1.1.0 follows\n", func() {
			So(FirstReleaseVersion(labelled("")), ShouldEqual, "1.1.0")
			So(FirstReleaseVersion(labelled("baseline", "1.0", "v1.0.0-rc1", "01.2.3")), ShouldEqual, "1.1.0")
		})

		Convey("Otherwise the minor after the highest x.y.z label follows, v or not\n", func() {
			So(FirstReleaseVersion(labelled("v1.0.0")), ShouldEqual, "1.1.0")
			So(FirstReleaseVersion(labelled("1.2.0", "v1.10.3", "v1.9.9", "draft")), ShouldEqual, "1.11.0")
			So(FirstReleaseVersion(labelled("v2.0.1", "1.12.0")), ShouldEqual, "2.1.0")
		})
	})
}
