// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package query_authorization

import (
	"context"
	"testing"
)

func TestValidateTrialIdentityAcceptsDraftOnly(t *testing.T) {
	for branch, ok := range map[string]bool{"main": true, "draft": true, "v2": false, "dev": false} {
		err := validateTrialIdentity(context.Background(), "kn1", branch)
		if (err == nil) != ok {
			t.Errorf("branch %q: err = %v, want ok = %v", branch, err, ok)
		}
	}
}
