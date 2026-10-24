// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.
package evidencesvc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func compressedFixture(t *testing.T) (capturePackage, []byte) {
	t.Helper()
	raw, _, err := EncodeCapture(packageFixture(), 100000)
	if err != nil {
		t.Fatal(err)
	}
	var p capturePackage
	if err = json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	compressed, err := compressCapturePackage(context.Background(), p, 100000)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(compressed, &p); err != nil {
		t.Fatal(err)
	}
	return p, compressed
}
func TestCaptureCompressionCompatibilityAndStates(t *testing.T) {
	m := packageFixture()
	m.Snapshot.CallFacts[0].Output.Inline = json.RawMessage{} // nonnil empty must remain distinct
	raw, hash, err := EncodeCapture(m, 100000)
	if err != nil {
		t.Fatal(err)
	}
	legacy, legacyHash, err := encodeCapture(context.Background(), m, 100000, false)
	if err != nil || !bytes.Equal(raw, legacy) || hash != legacyHash {
		t.Fatal("small legacy bytes changed")
	}
	var p capturePackage
	if err = json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	compressed, err := compressCapturePackage(context.Background(), p, 100000)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeCapture(compressed, capturePackageHash(compressed), 100000)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, m) {
		t.Fatal("compressed metadata/source bytes/nil-empty changed")
	}
	if _, err := DecodeRevisionInput(compressed, capturePackageHash(compressed), "int-a", "revision", 1000, 100000); err == nil {
		t.Fatal("revision accepted historical compressed format")
	}
}
func TestCaptureCompressionRejectsMalformedAndOverBudget(t *testing.T) {
	for _, name := range []string{"bad-gzip", "extra-member", "trailing", "missing-body", "embedded", "unknown", "aggregate-budget"} {
		t.Run(name, func(t *testing.T) {
			p, _ := compressedFixture(t)
			limit := 100000
			switch name {
			case "bad-gzip":
				p.Bodies[0] = []byte("bad")
			case "extra-member":
				p.Bodies[0] = append(p.Bodies[0], p.Bodies[0]...)
			case "trailing":
				p.Bodies[0] = append(p.Bodies[0], 0)
			case "missing-body":
				p.Bodies = p.Bodies[1:]
			case "embedded":
				p.Manifest.Snapshot.CallFacts[0].Input.Inline = json.RawMessage(`{}`)
			case "unknown":
				p.Version = "future"
			case "aggregate-budget":
				original := packageFixture()
				original.Snapshot.CallFacts[0].Input.Inline = json.RawMessage(bytes.Repeat([]byte{'x'}, 20000))
				original.Snapshot.CallFacts[0].Output.Inline = json.RawMessage(bytes.Repeat([]byte{'y'}, 20000))
				r, _, e := EncodeCapture(original, 100000)
				if e != nil {
					t.Fatal(e)
				}
				if e = json.Unmarshal(r, &p); e != nil {
					t.Fatal(e)
				}
				r, e = compressCapturePackage(context.Background(), p, 100000)
				if e != nil {
					t.Fatal(e)
				}
				if e = json.Unmarshal(r, &p); e != nil {
					t.Fatal(e)
				}
				limit = 30000
			}
			raw, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = DecodeCapture(raw, capturePackageHash(raw), limit); err == nil {
				t.Fatal("accepted invalid compressed package")
			} else if name == "aggregate-budget" && !errors.Is(err, ErrCapturePackageBudget) {
				t.Fatalf("decoded budget is not typed: %v", err)
			}
		})
	}
}
func TestCaptureCompressionCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := EncodeCaptureWithContext(ctx, packageFixture(), 100000); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
	p, _ := compressedFixture(t)
	if _, err := compressCapturePackage(ctx, p, 100000); !errors.Is(err, context.Canceled) {
		t.Fatalf("compression cancellation=%v", err)
	}
}

// A deterministic context cancels between compression chunks, without timing
// races or allocating another large production-sized fixture.
type captureChunkCancelContext struct {
	context.Context
	checks int
}

func (c *captureChunkCancelContext) Err() error {
	c.checks++
	if c.checks >= 4 {
		return context.Canceled
	}
	return nil
}
func TestCaptureCompressionChecksCancellationBetweenChunks(t *testing.T) {
	raw, _, err := EncodeCapture(packageFixture(), 100000)
	if err != nil {
		t.Fatal(err)
	}
	var p capturePackage
	if err = json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	p.Bodies[0] = bytes.Repeat([]byte{'x'}, 128<<10)
	ctx := &captureChunkCancelContext{Context: context.Background()}
	if _, err := compressCapturePackage(ctx, p, 200000); !errors.Is(err, context.Canceled) {
		t.Fatalf("chunk cancellation=%v", err)
	}
}
