// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsBuiltinAdmin(t *testing.T) {
	assert.False(t, IsBuiltinAdmin(context.Background()))

	userCtx := context.WithValue(context.Background(), ACCOUNT_INFO_KEY, AccountInfo{ID: "user-1"})
	assert.False(t, IsBuiltinAdmin(userCtx))

	adminCtx := context.WithValue(context.Background(), ACCOUNT_INFO_KEY, AccountInfo{ID: BuiltinAdminID})
	assert.True(t, IsBuiltinAdmin(adminCtx))
}
