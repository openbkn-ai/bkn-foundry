// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencepublisher

import (
	"encoding/json"
	"strings"
	"testing"
)

func BenchmarkConfirmationPublisher(b *testing.B) {
	for _, size := range []int{128, 65536} {
		name := "small"
		if size > 128 {
			name = "64KiB"
		}
		b.Run(name, func(b *testing.B) {
			cfg := publisherTestConfig()
			cfg.QueueMaxBytes = 1 << 22
			p, err := New(cfg, &fakeSender{})
			if err != nil {
				b.Fatal(err)
			}
			event := publisherTestEvent()
			event.Envelope = json.RawMessage(`{"body":"` + strings.Repeat("a", size) + `"}`)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if r := p.TryPublish(event); r.Disposition != Accepted {
					b.Fatal(r)
				}
				p.mu.Lock()
				p.queue = p.queue[:0]
				p.queueBytes = 0
				p.mu.Unlock()
			}
		})
	}
}
