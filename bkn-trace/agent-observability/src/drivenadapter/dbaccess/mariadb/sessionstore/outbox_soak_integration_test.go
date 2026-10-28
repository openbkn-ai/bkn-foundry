// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

//go:build integration

package sessionstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/projectorsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/sessionsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchevidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchprojection"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionoutbox"
)

// This exercises production Session lifecycle services and answer persistence,
// not an LLM, MCP transport, authentication, or actual business tool execution.
func TestProjectionOutboxContinuousLifecycleMariaDB(t *testing.T) {
	if os.Getenv("BKN_TRACE_OUTBOX_SOAK") != "1" {
		t.Skip("BKN_TRACE_OUTBOX_SOAK=1 is required")
	}
	duration := 5 * time.Minute
	if value := os.Getenv("BKN_TRACE_OUTBOX_SOAK_STAGE_DURATION"); value != "" {
		var err error
		duration, err = time.ParseDuration(value)
		if err != nil || duration < time.Second {
			t.Fatal("invalid soak stage duration")
		}
	}
	endpoint := os.Getenv("BKN_TRACE_OUTBOX_SCALE_OPENSEARCH_URL")
	if endpoint == "" {
		t.Fatal("isolated OpenSearch URL is required")
	}
	db, store := outboxRegressionDatabase(t)
	db.SetMaxOpenConns(32)
	service := sessionsvc.New(store, sessionsvc.Options{DurableAnswerEnabled: true})
	client := opensearch.New(endpoint, opensearch.AuthConfig{}, 10*time.Second)
	prefix := fmt.Sprintf("trace-outbox-soak-%d", time.Now().UnixNano())
	sink := opensearchprojection.New(client, prefix)
	if err := sink.EnsureBootstrap(context.Background(), prefix+"-v1"); err != nil {
		t.Fatal(err)
	}
	artifacts := opensearchevidencestore.New(client, prefix+"-evidence")
	t.Cleanup(func() {
		for _, index := range []string{prefix + "-v1", prefix + "-evidence-artifacts"} {
			req, _ := http.NewRequest(http.MethodDelete, endpoint+"/"+index, nil)
			resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
			if err != nil {
				t.Error(err)
				continue
			}
			_ = resp.Body.Close()
			if resp.StatusCode != 200 && resp.StatusCode != 404 {
				t.Errorf("cleanup %s: %d", index, resp.StatusCode)
			}
		}
	})
	metrics := &soakMetrics{}
	measured := &soakStore{Store: store}
	measured.metrics.Store(metrics)
	worker := projectorsvc.NewWorker(measured, sink, projectorsvc.WorkerOptions{
		BatchSize: 1, LockDuration: 30 * time.Second, LeaseIOTimeout: 2 * time.Second,
		AnswerTaskIOTimeout: 20 * time.Second, AnswerAckIOTimeout: 2 * time.Second,
		AnswerArtifactHandler: sessionsvc.NewAnswerArtifactHandler(service, artifacts),
	})
	// Calibrate both first and reused conversation calls, including the worker's
	// follow-up interaction projection after durable answer settlement.
	for i := 0; i < 2; i++ {
		before := soakQueue(t, db)
		if _, err := soakLifecycleCall(context.Background(), service, 0, uint64(i+1)); err != nil {
			t.Fatal(err)
		}
		produced := soakQueue(t, db)
		if _, err := worker.Drain(context.Background()); err != nil {
			t.Fatal(err)
		}
		after := soakQueue(t, db)
		t.Logf("LIFECYCLE_CALIBRATION call=%d producer_tasks=%d total_tasks_with_settlement=%d queue=%+v", i+1, produced.total-before.total, after.total-before.total, after)
		if after.pending != 0 || after.processing != 0 || after.dead != 0 {
			t.Fatal("calibration did not settle")
		}
	}
	t.Log("PROFILE input_output_json_bytes~=1024 answer_bytes=16384 one_tool_per_interaction agent_interval=10s worker_batch=1 db_pool=32")
	stop := make(chan struct{})
	var consume sync.WaitGroup
	var probes sync.WaitGroup
	var stopOnce sync.Once
	var stages []*soakMetrics
	consume.Add(1)
	go func() {
		defer consume.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			result, err := worker.RunOnce(context.Background())
			metrics := measured.metrics.Load()
			metrics.mu.Lock()
			metrics.workerErrors += soakBoolInt(err != nil)
			metrics.retried += result.Retried
			metrics.dead += result.Dead
			metrics.mu.Unlock()
			if err != nil {
				t.Logf("SOAK_WORKER_ERROR %v", err)
			}
			if err != nil || result.Leased == 0 {
				select {
				case <-stop:
					return
				case <-time.After(time.Second):
				}
			}
		}
	}()
	stopWorker := func() { stopOnce.Do(func() { close(stop) }); consume.Wait() }
	defer func() { stopWorker(); probes.Wait() }()
	var actors [400]sync.Mutex
	var sequence atomic.Uint64
	sequence.Store(2)
	for stage, rate := range []int{1, 2, 5, 10, 20, 40} {
		metrics := &soakMetrics{}
		stages = append(stages, metrics)
		measured.metrics.Store(metrics)
		before := soakQueue(t, db)
		started := time.Now()
		jobs := make(chan soakJob, 64)
		var producers sync.WaitGroup
		for i := 0; i < 32; i++ {
			producers.Add(1)
			go func() {
				defer producers.Done()
				for job := range jobs {
					actors[job.agent].Lock()
					ctx, cancel := context.WithDeadline(context.Background(), job.at.Add(10*time.Second))
					receipt, err := soakLifecycleCall(ctx, service, job.agent, job.sequence)
					cancel()
					actors[job.agent].Unlock()
					metrics.mu.Lock()
					metrics.calls++
					metrics.callErrors += soakBoolInt(err != nil)
					metrics.request = append(metrics.request, time.Since(job.at))
					metrics.mu.Unlock()
					if err != nil {
						t.Logf("SOAK_CALL_ERROR stage=%d agent=%d error=%v", stage+1, job.agent, err)
					}
					// One search-visibility sample per 5s, never force a refresh.
					if err == nil && job.sequence%uint64(max(1, rate*5)) == 0 {
						probes.Add(1)
						go func() { defer probes.Done(); soakSearchProbe(client, prefix, receipt.ID, job.at, metrics) }()
					}
				}
			}()
		}
		ticker := time.NewTicker(time.Second / time.Duration(rate))
		sample := time.NewTicker(30 * time.Second)
		timer := time.NewTimer(duration)
		offered, dropped := 0, 0
	stageLoop:
		for {
			select {
			case <-ticker.C:
				job := soakJob{agent: offered % (rate * 10), sequence: sequence.Add(1), at: time.Now()}
				offered++
				select {
				case jobs <- job:
				default:
					dropped++
				}
			case <-sample.C:
				q := soakQueue(t, db)
				t.Logf("SOAK_WINDOW stage=%d agents=%d target_calls_s=%d elapsed=%s offered=%d dropped=%d queue=%+v metrics=%s", stage+1, rate*10, rate, time.Since(started), offered, dropped, q, metrics.summary())
			case <-timer.C:
				break stageLoop
			}
		}
		ticker.Stop()
		sample.Stop()
		timer.Stop()
		close(jobs)
		producers.Wait()
		after := soakQueue(t, db)
		elapsed := time.Since(started)
		t.Logf("SOAK_STAGE stage=%d agents=%d target_calls_s=%d seconds=%.3f offered=%d dropped=%d produced_tasks=%d delivered_tasks=%d pending_delta=%d pending=%d oldest_pending_s=%.3f metrics=%s",
			stage+1, rate*10, rate, elapsed.Seconds(), offered, dropped, after.total-before.total, after.delivered-before.delivered, after.pending-before.pending, after.pending, after.oldest.Seconds(), metrics.summary())
	}
	// Report remaining backlog rather than hiding overload by draining it away.
	stopWorker()
	probes.Wait()
	for i, m := range stages {
		t.Logf("SOAK_STAGE_FINAL stage=%d metrics=%s", i+1, m.summary())
	}
	t.Logf("SOAK_FINAL queue=%+v", soakQueue(t, db))
}

type soakJob struct {
	agent    int
	sequence uint64
	at       time.Time
}

func soakLifecycleCall(ctx context.Context, service *sessionsvc.Service, agent int, sequence uint64) (sessionvo.Receipt, error) {
	owner := sessionvo.Owner{ApplicationPrincipalID: "capacity-agent-app", EffectiveSubjectType: sessionvo.SubjectUser, EffectiveSubjectID: fmt.Sprintf("agent-%04d", agent)}
	conv, err := service.EnsureCurrentConversation(ctx, sessionsvc.EnsureConversationCommand{Owner: owner, ExternalConversationKey: "capacity-conversation", IdempotencyKey: "ensure"})
	if err != nil {
		return sessionvo.Receipt{}, err
	}
	key := fmt.Sprintf("call-%d", sequence)
	i, err := service.StartInteraction(ctx, sessionsvc.StartInteractionCommand{Owner: owner, ConversationID: conv.ID, IdempotencyKey: key, AgentName: "capacity-agent"})
	if err != nil {
		return sessionvo.Receipt{}, err
	}
	payload, err := sessionvo.NormalizePayloadEnvelope(sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadInline, MediaType: "application/json", Inline: json.RawMessage(`{"padding":"` + strings.Repeat("x", 1000) + `"}`)})
	if err != nil {
		return sessionvo.Receipt{}, err
	}
	op, receipt, err := service.EnsureOperation(ctx, sessionsvc.EnsureOperationCommand{Owner: owner, ConversationID: conv.ID, InteractionID: i.ID, OperationKey: key, ToolName: "query_object_instance", Protocol: sessionvo.ProtocolMCP, SourceModule: "context-loader", Input: payload, Required: true, LeaseToken: i.LeaseToken, LeaseEpoch: i.LeaseEpoch})
	if err != nil {
		return sessionvo.Receipt{}, err
	}
	hash := sha256.Sum256([]byte(i.ID))
	trace := hex.EncodeToString(hash[:16])
	request := "request-" + i.ID
	_, receipt, err = service.CompleteOperationAttempt(ctx, sessionsvc.FinishAttemptCommand{Owner: owner, OperationID: op.ID, Attempt: receipt.Attempt, ReceiptID: receipt.ID, Output: payload, EvidenceDurability: sessionvo.DurabilityDurable, RequestID: request, TraceID: trace})
	if err != nil {
		return sessionvo.Receipt{}, err
	}
	artifact, validation := evidencevo.NormalizeArtifact(evidencevo.EvidenceArtifact{ArtifactID: "answer-" + i.ID, ArtifactType: evidencevo.ArtifactTypeResult, RequestID: request, InteractionID: i.ID, AccountID: owner.EffectiveSubjectID, AccountType: "user", EffectiveSubjectID: owner.EffectiveSubjectID, ApplicationPrincipalID: owner.ApplicationPrincipalID, ContentType: "text/plain", SchemaVersion: evidencevo.ArtifactContractVersion, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Content: strings.Repeat("a", 16<<10)})
	if len(validation) != 0 {
		return sessionvo.Receipt{}, fmt.Errorf("invalid synthetic answer: %v", validation)
	}
	raw, err := json.Marshal(artifact)
	if err != nil {
		return sessionvo.Receipt{}, err
	}
	_, err = service.TerminateInteraction(ctx, sessionsvc.TerminateInteractionCommand{Owner: owner, InteractionID: i.ID, Status: sessionvo.InteractionCompleted, TerminalIdempotencyKey: key, LeaseToken: i.LeaseToken, LeaseEpoch: i.LeaseEpoch, DeriveManifest: true, AnswerArtifact: raw, Manifest: sessionvo.ClosureManifest{Version: "3.0.0", CompletionReason: "completed"}})
	return receipt, err
}

type soakMetrics struct {
	mu                                                                                        sync.Mutex
	calls, callErrors, workerErrors, retried, dead, leaseErrors, leaseTimeouts, probeTimeouts int
	request, lease, delivery, visible                                                         []time.Duration
}

func (m *soakMetrics) summary() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return fmt.Sprintf("calls=%d call_errors=%d worker_errors=%d retried=%d dead=%d lease_errors=%d lease_over2s=%d probe_timeouts=%d request[%s] lease[%s] delivery[%s] visible[%s]", m.calls, m.callErrors, m.workerErrors, m.retried, m.dead, m.leaseErrors, m.leaseTimeouts, m.probeTimeouts, soakPercentiles(m.request), soakPercentiles(m.lease), soakPercentiles(m.delivery), soakPercentiles(m.visible))
}
func soakPercentiles(samples []time.Duration) string {
	if len(samples) == 0 {
		return "n=0"
	}
	v := append([]time.Duration(nil), samples...)
	sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
	return fmt.Sprintf("n=%d p95=%s p99=%s max=%s", len(v), v[(len(v)*95+99)/100-1], v[(len(v)*99+99)/100-1], v[len(v)-1])
}

type soakStore struct {
	*Store
	metrics atomic.Pointer[soakMetrics]
}

func (s *soakStore) Lease(ctx context.Context, n int, d time.Duration) ([]iprojectionoutbox.Item, error) {
	start := time.Now()
	items, err := s.Store.Lease(ctx, n, d)
	elapsed := time.Since(start)
	metrics := s.metrics.Load()
	metrics.mu.Lock()
	metrics.lease = append(metrics.lease, elapsed)
	metrics.leaseErrors += soakBoolInt(err != nil)
	metrics.leaseTimeouts += soakBoolInt(elapsed >= 2*time.Second)
	metrics.mu.Unlock()
	return items, err
}
func (s *soakStore) MarkDelivered(ctx context.Context, item iprojectionoutbox.Item) error {
	err := s.Store.MarkDelivered(ctx, item)
	if err == nil {
		metrics := s.metrics.Load()
		metrics.mu.Lock()
		metrics.delivery = append(metrics.delivery, time.Since(item.CreatedAt))
		metrics.mu.Unlock()
	}
	return err
}
func soakBoolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

type soakQueueState struct {
	total, pending, processing, delivered, dead int
	oldest                                      time.Duration
}

func soakQueue(t *testing.T, db *sql.DB) soakQueueState {
	t.Helper()
	var q soakQueueState
	rows, err := db.Query("SELECT status,COUNT(*) FROM bkn_trace_projection_outbox GROUP BY status")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			t.Fatal(err)
		}
		q.total += n
		switch status {
		case "pending":
			q.pending = n
		case "processing":
			q.processing = n
		case "delivered":
			q.delivered = n
		case "dead":
			q.dead = n
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if q.pending > 0 {
		var created time.Time
		if err := db.QueryRow("SELECT created_at FROM bkn_trace_projection_outbox FORCE INDEX(idx_bkn_trace_projection_claim_order) WHERE status='pending' ORDER BY outbox_id LIMIT 1").Scan(&created); err == nil {
			q.oldest = time.Since(created)
		} else if err != sql.ErrNoRows {
			t.Fatal(err)
		}
	}
	return q
}

func soakSearchProbe(client *opensearch.Client, index, id string, created time.Time, m *soakMetrics) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	query, _ := json.Marshal(map[string]any{"size": 1, "query": map[string]any{"ids": map[string]any{"values": []string{"receipt:" + id}}}})
	for {
		raw, err := client.Search(ctx, index, query)
		if err == nil {
			var result struct {
				Hits struct {
					Hits []struct {
						Source struct {
							Status string `json:"receipt_status"`
						} `json:"_source"`
					} `json:"hits"`
				} `json:"hits"`
			}
			if json.Unmarshal(raw, &result) == nil && len(result.Hits.Hits) > 0 && result.Hits.Hits[0].Source.Status == "completed" {
				m.mu.Lock()
				m.visible = append(m.visible, time.Since(created))
				m.mu.Unlock()
				return
			}
		}
		select {
		case <-ctx.Done():
			m.mu.Lock()
			m.probeTimeouts++
			m.mu.Unlock()
			return
		case <-time.After(250 * time.Millisecond):
		}
	}
}
