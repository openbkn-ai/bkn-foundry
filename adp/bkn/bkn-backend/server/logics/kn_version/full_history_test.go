// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kn_version

import (
	"testing"

	"github.com/openbkn-ai/licverify"
)

// The full version history (every version readable; deprecate, restore and purge) is Enterprise.
// Below it a network keeps only the running and the previous version.
func TestKeepsFullHistoryFromEnterprise(t *testing.T) {
	for _, tc := range []struct {
		edition licverify.Edition
		want    bool
	}{
		{licverify.EditionCommunity, false},
		{licverify.EditionProfessional, false},
		{licverify.EditionEnterprise, true},
		{licverify.EditionIndustry, true},
	} {
		if got := keepsFullHistory(tc.edition); got != tc.want {
			t.Errorf("keepsFullHistory(%s) = %v, want %v", tc.edition, got, tc.want)
		}
	}
}
