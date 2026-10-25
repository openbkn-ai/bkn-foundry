// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package projectorsvc_test

import (
	"context"
	"errors"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/projectorsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionoutbox"
)

type fakeAnswerHandler struct {
	calls, deaths int
	err, deadErr  error
}

func (h *fakeAnswerHandler) HandleAnswerArtifact(context.Context, iprojectionoutbox.Item) error {
	h.calls++
	return h.err
}
func (h *fakeAnswerHandler) MarkAnswerArtifactDead(context.Context, iprojectionoutbox.Item) error {
	h.deaths++
	return h.deadErr
}
func TestAnswerTaskDispatchAndPoisonQuarantine(t *testing.T) {
	for _, kind := range []string{"success", "retry", "permanent", "oversized", "unverifiable-dead", "missing-handler"} {
		t.Run(kind, func(t *testing.T) {
			item := iprojectionoutbox.Item{ID: 1, EventID: "answer-task", EventType: "artifact.persist_requested"}
			handler := &fakeAnswerHandler{}
			switch kind {
			case "retry":
				handler.err = errors.New("temporary outage")
			case "permanent", "unverifiable-dead":
				handler.err = iprojectionoutbox.Permanent(errors.New("invalid answer"))
			case "oversized":
				item.PayloadTooLarge = true
			}
			if kind == "unverifiable-dead" {
				handler.deadErr = iprojectionoutbox.Permanent(errors.New("unknown original interaction"))
			}
			store := &fakeOutboxStore{items: []iprojectionoutbox.Item{item}}
			sink := &fakeProjectionSink{}
			options := projectorsvc.WorkerOptions{AnswerArtifactHandler: handler}
			if kind == "missing-handler" {
				options.AnswerArtifactHandler = nil
			}
			result, err := projectorsvc.NewWorker(store, sink, options).RunOnce(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if sink.calls != 0 {
				t.Fatal("answer leaked into generic search projection")
			}
			switch kind {
			case "success":
				if result.Delivered != 1 || handler.deaths != 0 {
					t.Fatal(result)
				}
			case "retry", "missing-handler":
				if result.Retried != 1 || handler.deaths != 0 {
					t.Fatal(result)
				}
			default:
				if result.Dead != 1 || handler.deaths != 1 {
					t.Fatal("poison item blocked DLQ", result)
				}
			}
			if kind == "oversized" && handler.calls != 0 {
				t.Fatal("oversized payload reached decoder")
			}
		})
	}
}
