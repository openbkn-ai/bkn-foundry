package locale

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRegister(t *testing.T) {
	require.NotPanics(t, Register)
}
