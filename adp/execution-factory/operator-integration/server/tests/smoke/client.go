package smoke

import (
	"fmt"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

// Smoke test cases.
type smokeClient struct {
	baseURL string
	client  interfaces.HTTPClient
}

// NewPublicSmokeClient creates a new test client.
func NewPublicSmokeClient(host string) *smokeClient {
	baseURL := fmt.Sprintf("http://%s/api/agent-operator-integration/v1", host)
	return &smokeClient{
		baseURL: baseURL,
		client:  rest.NewHTTPClient(),
	}
}

func NewPrivateSmokeClient(host string) *smokeClient {
	baseURL := fmt.Sprintf("http://%s/api/agent-operator-integration/internal-v1", host)
	return &smokeClient{
		baseURL: baseURL,
		client:  rest.NewHTTPClient(),
	}
}
