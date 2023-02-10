package rlock

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
)

var (
	//go:embed script/lua/lock.lua
	luaLock string
	//go:embed script/lua/unlock.lua
	luaUnlock string
	//go:embed script/lua/refresh.lua
	luaRefresh string

	ErrLockNotHold         = errors.New("rlock: 未持有锁")
	ErrFailedToPreemptLock = errors.New("rlock: 抢锁失败")
)

type Client struct {
	client redis.Cmdable

	g singleflight.Group

	valuer func() string
}

func NewClient(client redis.Cmdable) *Client {
	return &Client{
		client: client,
		valuer: func() string {
			return uuid.New().String()
		},
	}
}

func (c *Client) SingleflightLock(ctx context.Context, key string, expiration time.Duration, retry RetryStrategy, timeout time.Duration) (*Lock, error) {
	for {
		flag := false
		resCh := c.g.DoChan(key, func() (interface{}, error) {
			flag = true
			return c.Lock(ctx, key, expiration, retry, timeout)
		})
		select {
		case res := <-resCh:
			if flag {
				c.g.Forget(key)
				if res.Err != nil {
					return nil, res.Err
				}
				return res.Val.(*Lock), nil
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// Lock 是尽可能重试减少加锁失败的可能
// Lock 会在超时或者锁正被人持有的时候进行重试
// 最后返回的 error 使用 errors.Is 判断，可能是：
// - context.DeadlineExceeded: Lock 整体调用超时
// - ErrFailedToPreemptLock: 超过重试次数，但是整个重试过程都没有出现错误
// - DeadlineExceeded 和 ErrFailedToPreemptLock: 超过重试次数，但是最后一次重试超时了
// 使用过程中，应注意：
// - 如果 errors.Is(err, context.DeadlineExceeded) 那么最终有没有加锁成功，谁也不知道
// - 如果 errors.Is(err, ErrFailedToPreemptLock) 说明肯定没成功，而且超过了重试次数
// - 否则，和 Redis 通信出了问题
func (c *Client) Lock(ctx context.Context, key string, expiration time.Duration, retry RetryStrategy, timeout time.Duration) (*Lock, error) {
	val := c.valuer()
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

	for {
		lctx, cancel := context.WithTimeout(ctx, timeout)
		res, err := c.client.Eval(lctx, luaLock, []string{key}, val, expiration.Seconds()).Result()
		cancel()

		if err != nil && errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}

		if res == "OK" {
			return newLock(c.client, key, val, expiration), nil
		}

		interval, ok := retry.Next()
		if !ok {
			if err != nil {
				err = fmt.Errorf("最后一次重试错误: %w", err)
			} else {
				err = fmt.Errorf("锁被人持有: %w", ErrFailedToPreemptLock)
			}
			return nil, fmt.Errorf("rlock: 重试机会耗尽，%w", err)
		}

		if timer == nil {
			timer = time.NewTimer(interval)
		} else {
			timer.Reset(interval)
		}

		select {
		case <-timer.C:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (c *Client) TryLock(ctx context.Context, key string, expiration time.Duration) (*Lock, error) {
	val := c.valuer()
	ok, err := c.client.SetNX(ctx, key, val, expiration).Result()

	// redis error
	if err != nil {
		return nil, err
	}

	// lock error
	if !ok {
		return nil, ErrFailedToPreemptLock
	}
	return newLock(c.client, key, val, expiration), nil
}

type Lock struct {
	client redis.Cmdable

	key   string
	value string

	expiration time.Duration

	unlock            chan struct{}
	singnalUnlockOnce sync.Once
}

func newLock(client redis.Cmdable, key string, value string, expiration time.Duration) *Lock {
	return &Lock{
		client: client, key: key, value: value,
		expiration: expiration,

		unlock: make(chan struct{}, 1),
	}
}

func (l *Lock) AutoRefresh(interval time.Duration, timeout time.Duration) error {
	ch := make(chan struct{}, 1)
	ticker := time.NewTicker(interval)

	defer func() {
		close(ch)
		ticker.Stop()
	}()

	for {
		select {
		case <-ch: // retry
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			err := l.Refresh(ctx)
			cancel()

			if err == context.DeadlineExceeded {
				select {
				case ch <- struct{}{}:
				default:
				}
				continue
			}
			if err != nil {
				return err
			}
		case <-ticker.C: // ticker timeout
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			err := l.Refresh(ctx)
			cancel()

			if err == context.DeadlineExceeded {
				select {
				case ch <- struct{}{}:
				default:
				}
				continue
			}
			if err != nil {
				return err
			}
		case <-l.unlock: // unlock stop AutoFresh
			return nil
		}
	}
}

func (l *Lock) Refresh(ctx context.Context) error {
	res, err := l.client.Eval(ctx, luaRefresh, []string{l.key}, l.value, l.expiration.Seconds()).Int64()
	if err != nil {
		return err
	}
	if res != 1 {
		return ErrLockNotHold
	}
	return nil
}

func (l *Lock) Unlock(ctx context.Context) error {
	defer func() {
		l.singnalUnlockOnce.Do(func() {
			l.unlock <- struct{}{}
			close(l.unlock)
		})
	}()

	res, err := l.client.Eval(ctx, luaUnlock, []string{l.key}, l.value).Int64()
	if err == redis.Nil {
		return ErrLockNotHold
	}

	if err != nil {
		return err
	}

	// other lock, or lock not exist
	if res != 1 {
		return ErrLockNotHold
	}

	return nil
}
