package evidencepublisher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const traceEvidenceConfigurationPath = "/api/agent-observability/v1/trace-evidence-configuration"

var errInvalidConfigurationClient = errors.New("invalid evidence publisher configuration client")

// AccessTokenSource is satisfied by OAuthTokenSource and permits the runtime
// client to add a short-lived BKN Safe Bearer token to control-plane reads.
type AccessTokenSource interface {
	Token(context.Context) (string, error)
}

type ConfigurationClientConfig struct {
	BaseURL     string
	HTTPClient  *http.Client
	TokenSource AccessTokenSource
}

// ConfigurationClient reads the existing, capability-protected configuration
// endpoint. It does not model or validate the signed policy snapshot.
type ConfigurationClient struct {
	url         string
	client      *http.Client
	tokenSource AccessTokenSource
}

type ConfigurationOperation struct {
	ID       string
	Revision uint64
}

func NewConfigurationClient(config ConfigurationClientConfig) (*ConfigurationClient, error) {
	baseURL := strings.TrimSpace(config.BaseURL)
	parsed, err := url.ParseRequestURI(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || config.TokenSource == nil {
		return nil, errInvalidConfigurationClient
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &ConfigurationClient{
		url:         strings.TrimRight(baseURL, "/") + traceEvidenceConfigurationPath,
		client:      client,
		tokenSource: config.TokenSource,
	}, nil
}

// OperationForRevision returns an ACK candidate only when the existing
// configuration read model names an active operation at the same revision as
// the already-verified signed policy snapshot. Callers must not ACK otherwise.
func (c *ConfigurationClient) OperationForRevision(ctx context.Context, revision uint64) (ConfigurationOperation, bool, error) {
	if c == nil || c.client == nil || c.tokenSource == nil || revision == 0 {
		return ConfigurationOperation{}, false, errInvalidConfigurationClient
	}
	token, err := c.tokenSource.Token(ctx)
	if err != nil {
		return ConfigurationOperation{}, false, fmt.Errorf("get BKN Safe access token: %w", err)
	}
	if strings.TrimSpace(token) == "" {
		return ConfigurationOperation{}, false, errors.New("BKN Safe access token is empty")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return ConfigurationOperation{}, false, fmt.Errorf("create trace evidence configuration request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := c.client.Do(request)
	if err != nil {
		return ConfigurationOperation{}, false, fmt.Errorf("read trace evidence configuration: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ConfigurationOperation{}, false, fmt.Errorf("read trace evidence configuration: unexpected status %d", response.StatusCode)
	}
	var configuration struct {
		Kind              string  `json:"kind"`
		PolicyRevision    uint64  `json:"policy_revision"`
		ActiveOperationID *string `json:"active_operation_id"`
	}
	decoder := json.NewDecoder(response.Body)
	if err := decoder.Decode(&configuration); err != nil {
		return ConfigurationOperation{}, false, fmt.Errorf("decode trace evidence configuration: %w", err)
	}
	if err := requireSingleJSONValue(decoder); err != nil {
		return ConfigurationOperation{}, false, err
	}
	if configuration.Kind != "configuration_get" || configuration.PolicyRevision == 0 {
		return ConfigurationOperation{}, false, errors.New("invalid trace evidence configuration response")
	}
	if configuration.PolicyRevision != revision || configuration.ActiveOperationID == nil || strings.TrimSpace(*configuration.ActiveOperationID) == "" {
		return ConfigurationOperation{}, false, nil
	}
	return ConfigurationOperation{ID: strings.TrimSpace(*configuration.ActiveOperationID), Revision: revision}, true, nil
}

func requireSingleJSONValue(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("trace evidence configuration response contains multiple JSON values")
		}
		return fmt.Errorf("decode trace evidence configuration response tail: %w", err)
	}
	return nil
}
