package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewHTTPClient(t *testing.T) {
	client := NewHTTPClient()

	require.NotNil(t, client)
	assert.Same(t, client, NewHTTPClient())
}
