// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import "testing"

// TestEveryToolIsAnnotated is the drift guard. A tool added without an entry
// here is advertised with the protocol defaults - not read-only, destructive,
// open world - so a host has to treat reading a schema as it would treat a
// shell. That is safe and wrong, and nothing else would report it.
func TestEveryToolIsAnnotated(t *testing.T) {
	for key := range allToolMeta() {
		if _, ok := toolAnnotations[key]; !ok {
			t.Errorf("%s has no annotation: it would advertise the pessimistic defaults", key)
		}
	}
}

// TestAnnotationsAreConsistent checks the hints do not contradict each other.
// destructiveHint and idempotentHint are defined only for tools that write, so
// a read-only tool must not carry them, and a tool that only reads must not
// claim an open world.
func TestAnnotationsAreConsistent(t *testing.T) {
	for key := range toolAnnotations {
		a := annotationFor(key)
		if a.ReadOnlyHint == nil {
			t.Errorf("%s: readOnlyHint must be stated either way", key)
			continue
		}
		if !*a.ReadOnlyHint {
			if a.DestructiveHint == nil || a.IdempotentHint == nil {
				t.Errorf("%s: a writing tool must state destructiveHint and idempotentHint", key)
			}
			continue
		}
		if a.DestructiveHint != nil || a.IdempotentHint != nil {
			t.Errorf("%s: destructiveHint and idempotentHint are undefined for a read-only tool", key)
		}
		if a.OpenWorldHint == nil || *a.OpenWorldHint {
			t.Errorf("%s: a read-only knowledge-network tool stays inside a bounded domain", key)
		}
	}
}

// TestReadOnlyToolsOutnumberWriters records the point of the change. Most of
// this surface only reads; before annotations a host could not tell.
func TestReadOnlyToolsOutnumberWriters(t *testing.T) {
	readers, writers := 0, 0
	for key := range toolAnnotations {
		if a := annotationFor(key); a.ReadOnlyHint != nil && *a.ReadOnlyHint {
			readers++
		} else {
			writers++
		}
	}
	if readers <= writers {
		t.Fatalf("expected the surface to be mostly readers, got %d readers / %d writers", readers, writers)
	}
	t.Logf("%d read-only tools, %d writing tools", readers, writers)
}

// TestRunSQLIsReadOnly is called out on its own because the name invites the
// opposite conclusion. run_sql accepts a single SELECT against resources already
// bound to the network; its grammar excludes every write and every DDL.
func TestRunSQLIsReadOnly(t *testing.T) {
	a := annotationFor(toolKeyRunSQL)
	if a.ReadOnlyHint == nil || !*a.ReadOnlyHint {
		t.Fatal("run_sql executes read-only SQL and must be annotated as read-only")
	}
}

// TestArbitraryEffectToolsAreNotReadOnly guards the other direction: the tools
// whose effect is whatever the caller submits must never be marked safe.
func TestArbitraryEffectToolsAreNotReadOnly(t *testing.T) {
	for _, key := range []string{toolKeyRunCode, toolKeyRunShell, toolKeyExecuteAction, toolKeyExecuteSkill} {
		a := annotationFor(key)
		if a.ReadOnlyHint == nil || *a.ReadOnlyHint {
			t.Errorf("%s runs what the caller supplies and must not be read-only", key)
		}
		if a.DestructiveHint == nil || !*a.DestructiveHint {
			t.Errorf("%s must be marked destructive", key)
		}
	}
}
