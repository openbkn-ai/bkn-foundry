// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package anyshare

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultFilterable(t *testing.T) {

	for _, tc := range []struct {
		nativeType string
		want       bool
	}{
		{"string", true},
		{"integer", true},
		{"boolean", true},
		{"json", false},
		{"binary", false},
		{"vector", false},
		{"datetime", false},
		{"future_type", false},
		{"", false},
	} {
		t.Run(tc.nativeType, func(t *testing.T) {
			assert.Equal(t, tc.want, DefaultFilterable(tc.nativeType))
		})
	}
}
