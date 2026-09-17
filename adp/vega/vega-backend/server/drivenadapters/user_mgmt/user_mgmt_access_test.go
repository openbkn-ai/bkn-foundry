// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package user_mgmt

import (
	"context"
	"testing"

	rmock "github.com/openbkn-ai/bkn-foundry/comm-go/rest/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
)

func TestUserMgmtAccessGetAccountNames(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := rmock.NewMockHTTPClient(ctrl)
	client.EXPECT().PostNoUnmarshal(gomock.Any(), "http://safe/api/safe/v1/directory/names", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _ map[string]string, body any) (int, []byte, error) {
			assert.Equal(t, []string{"u1"}, body.(map[string]any)["user_ids"])
			return 200, []byte(`{"user_names":[{"id":"u1","name":"User One"}]}`), nil
		})

	accounts := []*interfaces.AccountInfo{{ID: "u1", Type: interfaces.ACCESSOR_TYPE_USER}}
	err := (&userMgmtAccess{httpClient: client, bknSafeURL: "http://safe"}).GetAccountNames(context.Background(), accounts)
	require.NoError(t, err)
	assert.Equal(t, "User One", accounts[0].Name)
}
