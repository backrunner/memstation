package bootstrap

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/hibiken/asynq"
)

func AsynqRedisOpt(redisURL string) (asynq.RedisClientOpt, error) {
	if redisURL == "" {
		return asynq.RedisClientOpt{}, fmt.Errorf("redis url is required")
	}
	parsed, err := url.Parse(redisURL)
	if err != nil {
		return asynq.RedisClientOpt{}, err
	}
	password, _ := parsed.User.Password()
	db := 0
	if raw := strings.TrimPrefix(parsed.Path, "/"); raw != "" {
		db, err = strconv.Atoi(raw)
		if err != nil {
			return asynq.RedisClientOpt{}, err
		}
	}
	return asynq.RedisClientOpt{
		Addr:     parsed.Host,
		Username: parsed.User.Username(),
		Password: password,
		DB:       db,
	}, nil
}
