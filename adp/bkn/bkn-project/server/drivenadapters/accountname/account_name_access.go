// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package accountname names accounts through bkn-safe's directory, as bkn-backend does for the
// creators and updaters of a network.
package accountname

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// BknSafeURLEnv is the bkn-safe base URL the chart sets, e.g. http://bkn-safe:3000.
const BknSafeURLEnv = "BKN_SAFE_URL"

const (
	accountTypeUser = "user"
	accountTypeApp  = "app"
	lookupTimeout   = 3 * time.Second
)

type accountNameAccess struct {
	url    string
	client *http.Client
}

// NewAccountNameAccess names accounts through bkn-safe at BKN_SAFE_URL; without it, it names none.
func NewAccountNameAccess() interfaces.AccountNameAccess {
	return NewAccountNameAccessAt(os.Getenv(BknSafeURLEnv))
}

// NewAccountNameAccessAt names accounts through bkn-safe at baseURL.
func NewAccountNameAccessAt(baseURL string) interfaces.AccountNameAccess {
	return &accountNameAccess{url: strings.TrimRight(strings.TrimSpace(baseURL), "/"), client: &http.Client{Timeout: lookupTimeout}}
}

type namedID struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (a *accountNameAccess) Names(ctx context.Context, accounts []interfaces.AccountInfo) map[interfaces.AccountInfo]string {
	names := map[interfaces.AccountInfo]string{}
	if a.url == "" || len(accounts) == 0 {
		return names
	}
	users, apps, seen := []string{}, []string{}, map[interfaces.AccountInfo]bool{}
	for _, account := range accounts {
		if account.ID == "" || seen[account] {
			continue
		}
		seen[account] = true
		switch account.Type {
		case accountTypeUser:
			users = append(users, account.ID)
		case accountTypeApp:
			apps = append(apps, account.ID)
		}
	}
	if len(users)+len(apps) == 0 {
		return names
	}
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetAccountNames")
	defer span.End()

	body, err := json.Marshal(map[string][]string{"user_ids": users, "app_ids": apps})
	if err != nil {
		return names
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.url+"/api/safe/v1/directory/names", bytes.NewReader(body))
	if err != nil {
		return names
	}
	request.Header.Set("Content-Type", "application/json")
	resp, err := a.client.Do(request)
	if err != nil {
		logger.Warnf("Get account names failed: %v", err)
		return names
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		logger.Warnf("Get account names failed: HTTP %d", resp.StatusCode)
		return names
	}
	var reply struct {
		UserNames []namedID `json:"user_names"`
		AppNames  []namedID `json:"app_names"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		logger.Warnf("Get account names failed: %v", err)
		return names
	}
	for _, u := range reply.UserNames {
		if name := strings.TrimSpace(u.Name); name != "" {
			names[interfaces.AccountInfo{ID: u.ID, Type: accountTypeUser}] = name
		}
	}
	for _, app := range reply.AppNames {
		if name := strings.TrimSpace(app.Name); name != "" {
			names[interfaces.AccountInfo{ID: app.ID, Type: accountTypeApp}] = name
		}
	}
	return names
}
