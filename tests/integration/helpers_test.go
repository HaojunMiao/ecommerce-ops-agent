//go:build integration

// Package integration_test 提供基于真实 PostgreSQL 和 Redis 的端到端测试。
//
// StartPostgres 复用 internal/.../testpg(pgvector + 自动迁移 + KBOT_TEST_DATABASE_URL 复用);
// StartRedis 起 redis:7-alpine(或复用 KBOT_TEST_REDIS_URL)。两者都 t.Cleanup 自动回收。
// newPGPlatform 用真 PG(+可选 Redis)装配 platform.Service —— 注意 embedder 维度必须 = pgvectorDim(2048)。
package integration_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ory/dockertest/v3"
	"github.com/ory/dockertest/v3/docker"
	"github.com/redis/go-redis/v9"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/infrastructure/postgres/testpg"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/retriever"
)

// StartPostgres 返回一个已迁移、可用的 pgvector 连接池(dockertest 起容器,或复用 KBOT_TEST_DATABASE_URL)。
func StartPostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return testpg.Start(t)
}

// StartRedis 返回一个就绪的 redis 客户端(复用 KBOT_TEST_REDIS_URL,否则 dockertest 起 redis:7-alpine)。
func StartRedis(t *testing.T) *redis.Client {
	t.Helper()
	if url := os.Getenv("KBOT_TEST_REDIS_URL"); url != "" {
		opt, err := redis.ParseURL(url)
		if err != nil {
			t.Fatalf("StartRedis: 解析 KBOT_TEST_REDIS_URL 失败: %v", err)
		}
		c := redis.NewClient(opt)
		t.Cleanup(func() { _ = c.Close() })
		return c
	}

	pool, err := dockertest.NewPool("")
	if err != nil {
		t.Fatalf("StartRedis: 连不上 Docker: %v", err)
	}
	if err := pool.Client.Ping(); err != nil {
		t.Fatalf("StartRedis: Docker daemon 未就绪: %v", err)
	}
	res, err := pool.RunWithOptions(&dockertest.RunOptions{
		Repository: "redis", Tag: "7-alpine",
	}, func(c *docker.HostConfig) {
		c.AutoRemove = true
		c.RestartPolicy = docker.RestartPolicy{Name: "no"}
	})
	if err != nil {
		t.Fatalf("StartRedis: 启动容器失败: %v", err)
	}
	t.Cleanup(func() { _ = pool.Purge(res) })
	res.Expire(600)

	addr := "localhost:" + res.GetPort("6379/tcp")
	var client *redis.Client
	pool.MaxWait = 60 * time.Second
	if err := pool.Retry(func() error {
		client = redis.NewClient(&redis.Options{Addr: addr})
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return client.Ping(ctx).Err()
	}); err != nil {
		t.Fatalf("StartRedis: 等待就绪失败: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// newPGPlatform 用真 PG(+可选 Redis)装配 platform.Service。
// embedder 固定 2048 维以匹配 kb_chunks.embedding。
func newPGPlatform(t *testing.T, pool *pgxpool.Pool, rds *redis.Client) *platform.Service {
	t.Helper()
	service := platform.NewService(pool, rds, make([]byte, 32), retriever.NewLocalEmbedder(2048), nil)
	// Audit Writer 必须在数据库连接池回收前排空，避免测试和优雅退出窗口丢最后一批日志。
	t.Cleanup(service.Audit.Close)
	return service
}
