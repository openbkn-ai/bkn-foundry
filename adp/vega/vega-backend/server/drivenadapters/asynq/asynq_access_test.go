// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package asynq

import (
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/common"
)

func TestAsynqAccessGetRedisClientOpt(t *testing.T) {
	t.Run("sentinel", func(t *testing.T) {
		access := &asynqAccess{appSetting: &common.AppSetting{RedisSetting: common.RedisSetting{
			ConnectType:      "sentinel",
			Username:         "redis-user",
			Password:         "redis-pass",
			SentinelHost:     "sentinel.local",
			SentinelPort:     26379,
			SentinelUsername: "sentinel-user",
			SentinelPassword: "sentinel-pass",
			MasterGroupName:  "mymaster",
		}}}

		opt, ok := access.getRedisClientOpt().(asynq.RedisFailoverClientOpt)

		require.True(t, ok)
		assert.Equal(t, []string{"sentinel.local:26379"}, opt.SentinelAddrs)
		assert.Equal(t, "redis-user", opt.Username)
		assert.Equal(t, "redis-pass", opt.Password)
		assert.Equal(t, "sentinel-user", opt.SentinelUsername)
		assert.Equal(t, "sentinel-pass", opt.SentinelPassword)
		assert.Equal(t, "mymaster", opt.MasterName)
		assert.Equal(t, 20, opt.PoolSize)
		assert.Equal(t, 5*time.Second, opt.DialTimeout)
		assert.Equal(t, 60*time.Second, opt.ReadTimeout)
		assert.Equal(t, 60*time.Second, opt.WriteTimeout)
	})

	t.Run("master slave uses master address", func(t *testing.T) {
		access := &asynqAccess{appSetting: &common.AppSetting{RedisSetting: common.RedisSetting{
			ConnectType: "master-slave",
			Username:    "user",
			Password:    "pass",
			MasterHost:  "master.local",
			MasterPort:  6379,
		}}}

		opt, ok := access.getRedisClientOpt().(asynq.RedisClientOpt)

		require.True(t, ok)
		assert.Equal(t, "master.local:6379", opt.Addr)
		assert.Equal(t, "user", opt.Username)
		assert.Equal(t, "pass", opt.Password)
		assert.Equal(t, 20, opt.PoolSize)
	})

	t.Run("standalone and unknown use host address", func(t *testing.T) {
		for _, connectType := range []string{"standalone", "cluster", "unknown"} {
			access := &asynqAccess{appSetting: &common.AppSetting{RedisSetting: common.RedisSetting{
				ConnectType: connectType,
				Host:        "redis.local",
				Port:        6380,
			}}}

			opt, ok := access.getRedisClientOpt().(asynq.RedisClientOpt)

			require.True(t, ok)
			assert.Equal(t, "redis.local:6380", opt.Addr)
		}
	})
}
