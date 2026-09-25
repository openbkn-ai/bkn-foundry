package evidencepublisher

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

var errInvalidOAuthTokenConfig = errors.New("invalid evidence publisher OAuth token configuration")

// OAuthTokenConfig contains only runtime-injected BKN Safe OAuth client
// credentials. Callers must source ClientSecret from a managed Secret.
type OAuthTokenConfig struct {
	TokenURL     string
	ClientID     string
	ClientSecret string
	Scopes       []string
	HTTPClient   *http.Client
}

// OAuthTokenSource obtains and reuses short-lived BKN Safe access tokens. It
// deliberately delegates token construction and refresh to oauth2 rather than
// persisting credentials or manufacturing a token locally.
type OAuthTokenSource struct {
	source oauth2.TokenSource
}

func NewOAuthTokenSource(config OAuthTokenConfig) (*OAuthTokenSource, error) {
	config.TokenURL = strings.TrimSpace(config.TokenURL)
	config.ClientID = strings.TrimSpace(config.ClientID)
	if config.TokenURL == "" || config.ClientID == "" || config.ClientSecret == "" {
		return nil, errInvalidOAuthTokenConfig
	}
	endpoint, err := url.ParseRequestURI(config.TokenURL)
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
		return nil, errInvalidOAuthTokenConfig
	}
	clientConfig := clientcredentials.Config{
		ClientID:     config.ClientID,
		ClientSecret: config.ClientSecret,
		TokenURL:     config.TokenURL,
		Scopes:       config.Scopes,
		AuthStyle:    oauth2.AuthStyleInParams,
	}
	tokenContext := context.Background()
	if config.HTTPClient != nil {
		tokenContext = context.WithValue(tokenContext, oauth2.HTTPClient, config.HTTPClient)
	}
	source := clientConfig.TokenSource(tokenContext)
	return &OAuthTokenSource{source: source}, nil
}

func (s *OAuthTokenSource) Token(ctx context.Context) (string, error) {
	if s == nil || s.source == nil {
		return "", errInvalidOAuthTokenConfig
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	token, err := s.source.Token()
	if err != nil {
		return "", err
	}
	if token.AccessToken == "" {
		return "", errors.New("OAuth token response omitted access_token")
	}
	return token.AccessToken, nil
}
