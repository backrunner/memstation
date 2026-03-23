package bootstrap

import (
	"context"

	"github.com/redis/go-redis/v9"
)

func OpenRedis(ctx context.Context, cfg Config) (*redis.Client, error) {
	if cfg.RedisURL == "" {
		return nil, nil
	}
	options, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return nil, err
	}
	client := redis.NewClient(options)
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}
