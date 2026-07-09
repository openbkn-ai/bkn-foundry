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

func TestNewAsynqAccess(t *testing.T) {
	t.Run("returns singleton access", func(t *testing.T) {
		access1 := NewAsynqAccess(newAsynqAppSetting("standalone"))
		access2 := NewAsynqAccess(newAsynqAppSetting("cluster"))

		require.NotNil(t, access1)
		assert.Same(t, access1, access2)
	})
}

func TestAsynqAccessCreateClient(t *testing.T) {
	t.Run("creates client", func(t *testing.T) {
		access := &asynqAccess{appSetting: newAsynqAppSetting("standalone")}

		client := access.CreateClient()
		t.Cleanup(func() { _ = client.Close() })

		require.NotNil(t, client)
	})
}

func TestAsynqAccessCreateInspector(t *testing.T) {
	t.Run("creates inspector", func(t *testing.T) {
		access := &asynqAccess{appSetting: newAsynqAppSetting("standalone")}

		inspector := access.CreateInspector()
		t.Cleanup(func() { _ = inspector.Close() })

		require.NotNil(t, inspector)
	})
}

func TestAsynqAccessCreateServer(t *testing.T) {
	t.Run("creates server", func(t *testing.T) {
		access := &asynqAccess{appSetting: newAsynqAppSetting("standalone")}

		server := access.CreateServer()

		require.NotNil(t, server)
	})
}

func TestGetRedisClientOpt(t *testing.T) {
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

func newAsynqAppSetting(connectType string) *common.AppSetting {
	return &common.AppSetting{RedisSetting: common.RedisSetting{
		ConnectType: connectType,
		Host:        "redis.local",
		Port:        6379,
		Username:    "user",
		Password:    "pass",
	}}
}
