// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package config

import (
	"testing"
)

func TestAuthorizationChunkSizeDefaultsAndEnvironment(t *testing.T) {
	conf := &Config{}
	if err := conf.localConfig("/path/that/does/not/exist"); err == nil {
		t.Fatal("expected missing fixture error")
	}
	if conf.Auth.ResourceFilterChunkSize != 200 {
		t.Fatalf("default chunk size = %d, want 200", conf.Auth.ResourceFilterChunkSize)
	}
	if conf.Project.SandboxPort != 30780 {
		t.Fatalf("default sandbox port = %d, want 30780", conf.Project.SandboxPort)
	}

	t.Setenv("CONTEXT_LOADER_RESOURCE_FILTER_CHUNK_SIZE", "73")
	overrideWithEnv(conf)
	if conf.Auth.ResourceFilterChunkSize != 73 {
		t.Fatalf("environment override failed: %+v", conf.Auth)
	}
}
