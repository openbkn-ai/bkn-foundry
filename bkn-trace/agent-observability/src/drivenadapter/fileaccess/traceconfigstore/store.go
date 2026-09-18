package traceconfigstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/traceconfig"
)

type Store struct {
	path string
}

func New(path string) *Store { return &Store{path: path} }

func (store *Store) Current(ctx context.Context) (traceconfig.Configuration, error) {
	var result traceconfig.Configuration
	err := store.withLock(ctx, false, func() error {
		current, err := store.read()
		result = current
		return err
	})
	return result, err
}

func (store *Store) Update(ctx context.Context, update func(*traceconfig.Configuration) error) (traceconfig.Configuration, error) {
	var result traceconfig.Configuration
	err := store.withLock(ctx, true, func() error {
		current, err := store.read()
		if err != nil {
			return err
		}
		if err := update(&current); err != nil {
			return err
		}
		if err := store.write(current); err != nil {
			return err
		}
		result = current
		return nil
	})
	return result, err
}

func (store *Store) withLock(ctx context.Context, exclusive bool, action func() error) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(store.path), 0o750); err != nil {
		return fmt.Errorf("create trace configuration directory: %w", err)
	}
	lock, err := os.OpenFile(store.path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open trace configuration lock: %w", err)
	}
	defer func() {
		if err := lock.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close trace configuration lock: %w", err))
		}
	}()
	mode := syscall.LOCK_SH
	if exclusive {
		mode = syscall.LOCK_EX
	}
	if err := syscall.Flock(int(lock.Fd()), mode); err != nil {
		return fmt.Errorf("lock trace configuration: %w", err)
	}
	defer func() {
		if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("unlock trace configuration: %w", err))
		}
	}()
	return action()
}

func (store *Store) read() (traceconfig.Configuration, error) {
	data, err := os.ReadFile(store.path)
	if errors.Is(err, os.ErrNotExist) {
		return traceconfig.Configuration{Services: []traceconfig.ServiceStatus{}}, nil
	}
	if err != nil {
		return traceconfig.Configuration{}, fmt.Errorf("read trace configuration: %w", err)
	}
	var current traceconfig.Configuration
	if err := json.Unmarshal(data, &current); err != nil {
		return traceconfig.Configuration{}, fmt.Errorf("decode trace configuration: %w", err)
	}
	if current.Services == nil {
		current.Services = []traceconfig.ServiceStatus{}
	}
	return current, nil
}

func (store *Store) write(current traceconfig.Configuration) error {
	data, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return fmt.Errorf("encode trace configuration: %w", err)
	}
	temporary := store.path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return fmt.Errorf("write trace configuration: %w", err)
	}
	if err := os.Rename(temporary, store.path); err != nil {
		return fmt.Errorf("replace trace configuration: %w", err)
	}
	return nil
}
