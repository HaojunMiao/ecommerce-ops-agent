package redis

import (
	"context"

	"github.com/redis/go-redis/v9"
)

// Publisher 用 Redis Pub/Sub 广播。
type Publisher struct {
	client *redis.Client
}

// NewPublisher 创建发布器。
func NewPublisher(client *redis.Client) *Publisher {
	return &Publisher{client: client}
}

// Publish 向频道广播一条消息。
func (p *Publisher) Publish(ctx context.Context, channel, message string) error {
	return p.client.Publish(ctx, channel, message).Err()
}
