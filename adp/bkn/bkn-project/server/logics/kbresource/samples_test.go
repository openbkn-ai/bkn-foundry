// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbresource

import (
	"fmt"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	. "github.com/smartystreets/goconvey/convey"
	"testing"
)

func sampleFiles() []interfaces.KBResourceFile {
	files := []interfaces.KBResourceFile{}
	// Several domains and knowledge bases: no business terms influence sampling.
	for group, count := range []int{5, 3, 2} {
		for i := 0; i < count; i++ {
			files = append(files, interfaces.KBResourceFile{KnowledgeBaseID: fmt.Sprint(group + 1), FileID: fmt.Sprint(i + 1), ContentVersionID: fmt.Sprint(100 + group*10 + i), CatalogID: fmt.Sprint(group), Name: []string{"Contract", "Patent", "Inspection"}[group]})
		}
	}
	return files
}
func noShuffle(_ int, _ func(int, int)) {}

func TestSampleSelection(t *testing.T) {
	Convey("Samples belong to locked files and debug/acceptance sets stay disjoint", t, func() {
		files := sampleFiles()
		Convey("Explicit choices use full source identity and preserve the input on error", func() {
			selected, err := chooseSamples(files, interfaces.KBSamplePlan{Mode: "specified", DebugIDs: []string{"1/1"}, AcceptanceIDs: []string{"2/1"}}, noShuffle)
			So(err, ShouldBeNil)
			So(selected[0].SampleRole, ShouldEqual, "debug")
			So(selected[5].SampleRole, ShouldEqual, "acceptance")
			So(files[0].SampleRole, ShouldBeEmpty)
			_, err = chooseSamples(files, interfaces.KBSamplePlan{Mode: "specified", DebugIDs: []string{"1/1"}, AcceptanceIDs: []string{"1/1"}}, noShuffle)
			So(err, ShouldEqual, interfaces.ErrKBSamplePlan)
			_, err = chooseSamples(files, interfaces.KBSamplePlan{Mode: "specified", DebugIDs: []string{"9/99"}}, noShuffle)
			So(err, ShouldEqual, interfaces.ErrKBSamplePlan)
		})
		Convey("Random counts have explicit ratios/minima, proportional directory strata, and no overlap", func() {
			ratio, min, acceptRatio, acceptMin := .3, 4, .2, 2
			selected, err := chooseSamples(files, interfaces.KBSamplePlan{Mode: "random", Ratio: &ratio, Minimum: &min, AcceptanceRatio: &acceptRatio, AcceptanceMinimum: &acceptMin}, noShuffle)
			So(err, ShouldBeNil)
			debug, accept := map[string]int{}, 0
			for _, f := range selected {
				if f.SampleRole == "debug" {
					debug[f.CatalogID]++
				}
				if f.SampleRole == "acceptance" {
					accept++
				}
			}
			So(debug, ShouldResemble, map[string]int{"0": 2, "1": 1, "2": 1})
			So(accept, ShouldEqual, 2)
		})
		Convey("Unspecified defaults and impossible counts are rejected, not silently invented", func() {
			_, err := chooseSamples(files, interfaces.KBSamplePlan{Mode: "random"}, noShuffle)
			So(err, ShouldEqual, interfaces.ErrKBSamplePlan)
			ratio, min := 1.0, 1
			_, err = chooseSamples(files, interfaces.KBSamplePlan{Mode: "random", Ratio: &ratio, Minimum: &min, AcceptanceRatio: &ratio, AcceptanceMinimum: &min}, noShuffle)
			So(err, ShouldEqual, interfaces.ErrKBSamplePlan)
		})
		Convey("Unreadable files cannot become extraction samples", func() {
			files[0].ContentVersionID = ""
			_, err := chooseSamples(files, interfaces.KBSamplePlan{Mode: "specified", DebugIDs: []string{"1/1"}}, noShuffle)
			So(err, ShouldEqual, interfaces.ErrKBSamplePlan)
		})
	})
}
