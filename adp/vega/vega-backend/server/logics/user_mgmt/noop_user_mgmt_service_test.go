package user_mgmt

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/common"
	"vega-backend/interfaces"
)

func TestNoopUserMgmtServiceGetAccountNames(t *testing.T) {
	t.Run("fills blank names with account ids", func(t *testing.T) {
		service := NewNoopUserMgmtService(&common.AppSetting{})
		accounts := []*interfaces.AccountInfo{
			{ID: "u1", Type: interfaces.ACCESSOR_TYPE_USER},
			{ID: "app1", Type: interfaces.ACCESSOR_TYPE_APP, Name: "Existing"},
		}

		err := service.GetAccountNames(context.Background(), accounts)

		require.NoError(t, err)
		assert.Equal(t, "u1", accounts[0].Name)
		assert.Equal(t, "Existing", accounts[1].Name)
	})
}
