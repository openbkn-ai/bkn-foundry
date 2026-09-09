// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"strings"
	"testing"
)

func TestBoundPTCStreamKeepsHeadAndTail(t *testing.T) {
	text := strings.Repeat("h", 100) + strings.Repeat("m", 1000) + "答案: 42\n"
	got := boundPTCStream(text, 100, 20, "stdout")
	if !strings.HasPrefix(got, strings.Repeat("h", 100)) {
		t.Fatalf("head lost: %q", got[:120])
	}
	if !strings.HasSuffix(got, "答案: 42\n") {
		t.Fatalf("tail lost: %q", got[len(got)-40:])
	}
	if !strings.Contains(got, "[stdout truncated: 987 characters omitted") {
		t.Fatalf("marker must name the stream and the omitted count: %q", got)
	}
	if strings.Count(got, "m") > 20 {
		t.Fatalf("middle must be cut, still %d m's", strings.Count(got, "m"))
	}
}

func TestBoundPTCStreamTailOnlyForStderr(t *testing.T) {
	text := strings.Repeat("noise\n", 100) + "Traceback (most recent call last):\nKeyError: 'x'\n"
	got := boundPTCStream(text, 0, 60, "stderr")
	if !strings.HasSuffix(got, "KeyError: 'x'\n") || strings.HasPrefix(got, "noise") {
		t.Fatalf("stderr must keep only its tail: %q", got)
	}
}

func TestBoundPTCStreamLeavesShortTextAlone(t *testing.T) {
	if got := boundPTCStream("ok\n", 10, 10, "stdout"); got != "ok\n" {
		t.Fatalf("short text altered: %q", got)
	}
}
