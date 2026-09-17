// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package conf

import "testing"

func TestHTTPServerConfigSupportsIsolatedDevelopmentPorts(t *testing.T) {
	t.Setenv("BKN_TRACE_HTTP_ADDRESS", "127.0.0.1:18086")
	t.Setenv("BKN_TRACE_INTERNAL_HTTP_ADDRESS", "127.0.0.1:18087")
	config := NewHTTPServerConfig()
	if config.Address != "127.0.0.1:18086" || config.InternalAddress != "127.0.0.1:18087" {
		t.Fatalf("HTTP addresses = %#v", config)
	}
}
