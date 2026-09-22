// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.

package rest

import (
	"crypto/tls"
	"testing"
)

func TestOpenSearchTransportUsesVerifiedTLSByDefault(t *testing.T) {
	transport := newOpenSearchTransport(nil)
	if transport.TLSClientConfig != nil && transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("TLS certificate verification is disabled")
	}
}

func TestOpenSearchTransportClonesCustomTLSConfig(t *testing.T) {
	config := &tls.Config{ServerName: "opensearch.internal"}
	transport := newOpenSearchTransport(config)
	if transport.TLSClientConfig == config {
		t.Fatal("TLS config was not cloned")
	}
	if got := transport.TLSClientConfig.ServerName; got != config.ServerName {
		t.Fatalf("ServerName = %q, want %q", got, config.ServerName)
	}
	if transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("custom TLS config disabled certificate verification")
	}
}
