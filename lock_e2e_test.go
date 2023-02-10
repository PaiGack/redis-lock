package rlock

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClient_TryLock_e2e(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "", // no password set
		DB:       0,  // use default DB
	})
	c := NewClient(rdb)
	// 确保 Redis 已经启动
	c.Wait()

	testCases := []struct {
		name string

		before func() // 准备数据
		after  func() // 清理数据

		key        string
		expiration time.Duration

		wantLock *Lock
		wantErr  error
	}{
		{
			name: "locked",

			before: func() {},
			after: func() {
				res, err := rdb.Del(context.Background(), "locked-key").Result()
				require.NoError(t, err)
				require.Equal(t, 1, res)
			},

			key:        "locked-key",
			expiration: time.Minute,

			wantLock: &Lock{key: "locked-key"},
		},
		{
			name: "failed to lock",

			before: func() {
				res, err := rdb.SetNX(context.Background(), "failed-to-lock-key", "123456", time.Minute).Result()
				require.NoError(t, err)
				require.Equal(t, 1, res)
			},
			after: func() {
				res, err := rdb.Get(context.Background(), "failed-to-lock-key").Result()
				require.NoError(t, err)
				require.Equal(t, "123456", res)

				res2, err := rdb.Del(context.Background(), "failed-to-lock-key").Result()
				require.NoError(t, err)
				require.Equal(t, 1, res2)
			},

			key:        "failed-to-lock-key",
			expiration: time.Minute,

			wantErr: ErrFailedToPreemptLock,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tc.before()

			l, err := c.TryLock(context.Background(), tc.key, tc.expiration)
			assert.Equal(t, tc.wantErr, err)
			if err != nil {
				return
			}
			tc.after()

			assert.NotNil(t, l.client)
			assert.Equal(t, tc.wantLock.key, l.key)
			assert.NotEmpty(t, l.value)
		})
	}

}

func (c *Client) Wait() {
	for c.client.Ping(context.Background()) != nil {
	}
}
