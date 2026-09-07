// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseRawIDs(t *testing.T) {
	assert.Equal(t, []string{"first", "second", "third"}, parseRawIDs(" first,second,, first , third,second "))
	assert.Empty(t, parseRawIDs(" , , "))
}
