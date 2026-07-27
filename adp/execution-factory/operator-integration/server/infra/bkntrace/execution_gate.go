package bkntrace

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"

	"github.com/redis/go-redis/v9"
)

var (
	ErrActionExecutionInProgress = errors.New("bkn trace action execution is already claimed")
	ErrActionExecutionStore      = errors.New("bkn trace action execution store unavailable")
	ErrActionReplayFailed        = errors.New("bkn trace action replayed prior failure")
)

type ExecutionState struct {
	Acquired  bool
	Completed bool
	Failed    bool
	Result    []byte
}

type ExecutionGate interface {
	Acquire(context.Context, Action) (ExecutionState, error)
	Complete(context.Context, Action, []byte, bool) error
}

type ExecutionStore interface {
	PutIfAbsent(context.Context, string, string) (bool, error)
	Get(context.Context, string) (string, error)
	Set(context.Context, string, string) error
}

type executionGate struct{ store ExecutionStore }

type redisExecutionStore struct{ client *redis.Client }

func NewExecutionGate(store ExecutionStore) ExecutionGate { return &executionGate{store: store} }

func NewRedisExecutionGate(client *redis.Client) ExecutionGate {
	if client == nil {
		return &executionGate{}
	}
	return &executionGate{store: &redisExecutionStore{client: client}}
}

func (g *executionGate) Acquire(ctx context.Context, action Action) (ExecutionState, error) {
	if g == nil || g.store == nil {
		return ExecutionState{}, ErrActionExecutionStore
	}
	key := action.executionKey()
	acquired, err := g.store.PutIfAbsent(ctx, key, "executing")
	if err != nil {
		return ExecutionState{}, errors.Join(ErrActionExecutionStore, err)
	}
	if acquired {
		return ExecutionState{Acquired: true}, nil
	}
	value, err := g.store.Get(ctx, key)
	if err != nil {
		return ExecutionState{}, errors.Join(ErrActionExecutionStore, err)
	}
	if value == "executing" {
		return ExecutionState{}, ErrActionExecutionInProgress
	}
	parts := strings.SplitN(value, ":", 2)
	if len(parts) != 2 || (parts[0] != "success" && parts[0] != "failed") {
		return ExecutionState{}, ErrActionExecutionStore
	}
	result, err := base64.RawStdEncoding.DecodeString(parts[1])
	if err != nil {
		return ExecutionState{}, errors.Join(ErrActionExecutionStore, err)
	}
	return ExecutionState{Completed: true, Failed: parts[0] == "failed", Result: result}, nil
}

func (g *executionGate) Complete(ctx context.Context, action Action, result []byte, failed bool) error {
	if g == nil || g.store == nil {
		return ErrActionExecutionStore
	}
	status := "success"
	if failed {
		status = "failed"
	}
	value := status + ":" + base64.RawStdEncoding.EncodeToString(result)
	if err := g.store.Set(ctx, action.executionKey(), value); err != nil {
		return errors.Join(ErrActionExecutionStore, err)
	}
	return nil
}

func (s *redisExecutionStore) PutIfAbsent(ctx context.Context, key, value string) (bool, error) {
	return s.client.SetNX(ctx, key, value, 0).Result()
}

func (s *redisExecutionStore) Get(ctx context.Context, key string) (string, error) {
	return s.client.Get(ctx, key).Result()
}

func (s *redisExecutionStore) Set(ctx context.Context, key, value string) error {
	return s.client.Set(ctx, key, value, 0).Err()
}

func (a Action) executionKey() string {
	scope := strings.Join([]string{a.accountType, a.accountID, a.businessDomain, a.instanceID}, "\x00")
	return "bkn-trace:action-execution:" + digest(scope)
}
