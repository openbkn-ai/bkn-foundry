// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package rest

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/opensearch-project/opensearch-go/v2"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
)

// OpenSearchClientConfig configures an OpenSearch client.
type OpenSearchClientConfig struct {
	Protocol string
	Host     string
	Port     int
	Username string
	Password string `json:"-"`
}

// NewOpenSearchClient initializes an OpenSearch client.
func NewOpenSearchClient(cfg OpenSearchClientConfig) *opensearch.Client {
	// Initialize the HTTP client.
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second, // Connection timeout.
			KeepAlive: 60 * time.Second, // Keep-alive duration.
		}).DialContext, // Dialer configuration.
		MaxIdleConns:          1000,             // Maximum idle connections.
		IdleConnTimeout:       60 * time.Second, // Idle connection timeout.
		ExpectContinueTimeout: 30 * time.Second, // Wait time for the first response.
		MaxIdleConnsPerHost:   500,              // Maximum idle connections per host.
		TLSHandshakeTimeout:   30 * time.Second,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	}

	// Endpoint address.
	address := fmt.Sprintf("%s://%s:%d", cfg.Protocol, cfg.Host, cfg.Port)

	// Retry policy.
	retryBackoff := backoff.NewExponentialBackOff()

	// Initialize the OpenSearch client.
	osc, _ := opensearch.NewClient(opensearch.Config{
		Addresses: []string{
			address,
		},
		Username:      cfg.Username,
		Password:      cfg.Password,
		Transport:     transport,
		RetryOnStatus: []int{502, 503, 504, 429},
		RetryBackoff: func(attempt int) time.Duration {
			if attempt == 1 {
				retryBackoff.Reset()
			}
			return retryBackoff.NextBackOff()
		},
		MaxRetries: 1,
	})

	CheckConnection(osc)
	return osc
}

// CheckConnection verifies the OpenSearch connection.
func CheckConnection(osc *opensearch.Client) bool {
	res, err := osc.Info()
	if err != nil {
		logger.Errorf("new opensearch client failed: %v", err.Error())
		return false
	}

	if res.IsError() {
		resBytes, err := io.ReadAll(res.Body)
		if err != nil {
			logger.Errorf("new opensearch client failed: %v", err.Error())
			return false
		}
		logger.Errorf("new opensearch client failed: %s", string(resBytes))
		return false
	}
	return true
}
