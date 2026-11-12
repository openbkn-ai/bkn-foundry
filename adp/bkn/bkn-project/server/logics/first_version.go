// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logics

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// FIRST_RELEASE_VERSION is the version suggested for a lineage's first release on a network
// without any version.
const FIRST_RELEASE_VERSION = "1.0.0"

// FirstReleaseVersion is the version suggested for a lineage's first release on a network that has
// versions: the minor after the highest x.y.z label among them, a leading v allowed, or 1.1.0
// when none is labelled so, as the network already runs a first version. A network without
// versions starts at FIRST_RELEASE_VERSION.
func FirstReleaseVersion(versions []*interfaces.KNVersion) string {
	if len(versions) == 0 {
		return FIRST_RELEASE_VERSION
	}
	var highest []int
	for _, v := range versions {
		if parts, ok := semver(v.Label); ok && (highest == nil || less(highest, parts)) {
			highest = parts
		}
	}
	if highest == nil {
		return "1.1.0"
	}
	return fmt.Sprintf("%d.%d.0", highest[0], highest[1]+1)
}

// semver reads label as x.y.z, with or without a leading v.
func semver(label string) ([]int, bool) {
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(label), "v"), ".")
	if len(parts) != 3 {
		return nil, false
	}
	numbers := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p != strconv.Itoa(n) {
			return nil, false
		}
		numbers[i] = n
	}
	return numbers, true
}

func less(a, b []int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}
