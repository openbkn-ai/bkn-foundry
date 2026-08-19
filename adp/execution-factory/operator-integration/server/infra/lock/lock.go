// Package lock redis locker
package lock

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	maxRenewRetryTimes = 3 // Maximum number of retries for failed renewal.
	connectTimeout     = 5 * time.Second
	divisor            = 2
)

// RedisLocker redis distributed lock.
type RedisLocker struct {
	client *redis.Client
	key    string
	value  string
	expiry time.Duration // Lock validity period.
	ticker *time.Ticker
	done   chan struct{}
}

// NewRedisLocker creates a new redis distributed lock.
func NewRedisLocker(client *redis.Client, key, value string, expiry time.Duration) *RedisLocker {
	return &RedisLocker{
		client: client,
		key:    key,
		value:  value,
		expiry: expiry,
		ticker: time.NewTicker(expiry / divisor),
		done:   make(chan struct{}),
	}
}

// Lock Get the lock.
func (l *RedisLocker) Lock(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	ok, err := l.acquireLock(ctx)
	if err != nil {
		return false, err
	}
	if ok {
		return true, nil
	}
	return false, nil
}

// Unlock releases the lock.
func (l *RedisLocker) Unlock(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	l.stopRenewal()
	if l.value == l.client.Get(ctx, l.key).Val() {
		l.client.Del(ctx, l.key)
	}
}

func (l *RedisLocker) acquireLock(ctx context.Context) (bool, error) {
	ok, err := l.client.SetNX(ctx, l.key, l.value, l.expiry).Result()
	if err != nil {
		return false, err
	}

	if ok {
		go l.startRenewal()
		return true, nil
	}

	currentValue, err := l.client.Get(ctx, l.key).Result()
	if err != nil {
		return false, err
	}

	if currentValue == l.value {
		go l.startRenewal()
		return true, nil
	}

	return false, nil
}

func (l *RedisLocker) startRenewal() {
	go func() {
		retryCh := make(chan struct{}, 1)
		defer close(retryCh)
		retryTimes := 0
		for {
			select {
			case <-l.ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
				err := l.renew(ctx)
				cancel()
				if err == context.DeadlineExceeded {
					retryCh <- struct{}{}
				}
			case <-retryCh:
				// The maximum number of retries is exceeded or the validity period of the lock is before the current retry times out (if the validity period is set to a small value, retry may not be possible)
				if retryTimes > maxRenewRetryTimes || l.expiry/divisor < connectTimeout*time.Duration(retryTimes+1) {
					return
				}
				ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
				err := l.renew(ctx)
				cancel()
				if err == context.DeadlineExceeded {
					retryCh <- struct{}{}
				}
				retryTimes++
			case <-l.done:
				return
			}
		}
	}()
}

func (l *RedisLocker) renew(ctx context.Context) (err error) {
	_, err = l.client.Expire(ctx, l.key, l.expiry).Result()
	return
}

func (l *RedisLocker) stopRenewal() {
	l.done <- struct{}{}
}
