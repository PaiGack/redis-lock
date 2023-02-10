## pkgs

```bash

go get github.com/redis/go-redis/v9
go get github.com/google/uuid
go get golang.org/x/sync/singleflight

go install github.com/golang/mock/mockgen@v1.6.0
```

## api

- NewClient
- SingleflightLock
- Lock
- TryLock
- AutoRefresh
- Refresh
- Unlock

## mock

```linux
mockgen -package=mocks -destination=mocks/redis_cmdable.mock.go github.com/redis/go-redis/v9 Cmdable
```

```win
mockgen -package=mocks -destination=mocks/redis_cmdable_mock_go github.com/redis/go-redis/v9 Cmdable
mv mocks/redis_cmdable_mock_go mocks/redis_cmdable.mock.go
```

## test

- 单元测试使用 mock 工具，不能依赖于任何的第三方工具

## QAQ

### 分布式锁如何实现？

- 核心 SetNX
- 重试机制实现需要使用 lua 脚本

### 分布式锁的过期时间怎么设置？

- 根据业务耗时，设置超时时间
- 引入自动的续约机制

### 怎么延长过期时间（续约）？

- 拿到属于自己的锁，重新设置过期时间

### 分布式锁失败的原因？

- 超时
- 网络故障
- Redis 服务器故障
- 别人正持有锁

### 怎么优化分布式锁的性能？

- 应该尽量避免使用分布式锁
- singleflight 限制只能一个 goroutine 调用函数，再通过判断标志位判断是不是自己执行的结果

### redis 事务如何实现？

- redis 事务通过使用 lua 脚本实现，通过 Eval 方法调用
- lua 脚本实现的事务，只保证原子性；若中间有执行失败，已经执行的命令不回滚

### 如何兼容不同的 redis 应用

```go
redis.client //单体
redis.ClusterClient //集群
redis.Cmdable //接口更通用
```
