//go:build redis

package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// Lua script to atomically check 512 key limit and set a single KV pair
var setKVScript = redis.NewScript(`
local key = KEYS[1]
local field = ARGV[1]
local val = ARGV[2]
local exists = redis.call('HEXISTS', key, field)
local len = redis.call('HLEN', key)
if exists == 0 and len >= 512 then
    return -1
end
redis.call('HSET', key, field, val)
return 0
`)

// Lua script to atomically check 512 key limit and set multiple KV pairs
var multiSetKVScript = redis.NewScript(`
local key = KEYS[1]
local current_len = redis.call('HLEN', key)
local new_keys_count = 0
for i = 1, #ARGV, 2 do
    local f = ARGV[i]
    if redis.call('HEXISTS', key, f) == 0 then
        new_keys_count = new_keys_count + 1
    end
end
if (current_len + new_keys_count) > 512 then
    return -1
end
for i = 1, #ARGV, 2 do
    redis.call('HSET', key, ARGV[i], ARGV[i+1])
end
return 0
`)

type RedisStore struct {
	client *redis.Client
}

func NewRedisStore(redisURL string) (*RedisStore, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("invalid redis url: %w", err)
	}
	client := redis.NewClient(opts)
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping failed: %w", err)
	}
	return &RedisStore{client: client}, nil
}

func (s *RedisStore) Client() *redis.Client {
	return s.client
}

func (s *RedisStore) GetKV(ctx context.Context, userID string) (map[string]string, error) {
	key := fmt.Sprintf("lanyard_kv:%s", userID)
	return s.client.HGetAll(ctx, key).Result()
}

func (s *RedisStore) SetKV(ctx context.Context, userID, key, value string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	if err := ValidateValue(value); err != nil {
		return err
	}

	redisKey := fmt.Sprintf("lanyard_kv:%s", userID)
	res, err := setKVScript.Run(ctx, s.client, []string{redisKey}, key, value).Int()
	if err != nil {
		return err
	}
	if res == -1 {
		return ErrSingleLimit
	}
	return nil
}

func (s *RedisStore) MultiSetKV(ctx context.Context, userID string, pairs map[string]string) error {
	for k, v := range pairs {
		if err := ValidateKey(k); err != nil {
			return err
		}
		if err := ValidateValue(v); err != nil {
			return err
		}
	}

	redisKey := fmt.Sprintf("lanyard_kv:%s", userID)
	args := make([]any, 0, len(pairs)*2)
	for k, v := range pairs {
		args = append(args, k, v)
	}

	res, err := multiSetKVScript.Run(ctx, s.client, []string{redisKey}, args...).Int()
	if err != nil {
		return err
	}
	if res == -1 {
		return ErrMultiLimit
	}
	return nil
}

func (s *RedisStore) DelKV(ctx context.Context, userID, key string) error {
	redisKey := fmt.Sprintf("lanyard_kv:%s", userID)
	return s.client.HDel(ctx, redisKey, key).Err()
}

func (s *RedisStore) UserIDForKey(ctx context.Context, key string) (string, bool, error) {
	val, err := s.client.Get(ctx, fmt.Sprintf("api_key:%s", key)).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return val, true, nil
}

func (s *RedisStore) KeyForUser(ctx context.Context, userID string) (string, bool, error) {
	val, err := s.client.Get(ctx, fmt.Sprintf("user_api_key:%s", userID)).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return val, true, nil
}

func (s *RedisStore) RotateKey(ctx context.Context, userID string) (string, error) {
	newKey, err := GenerateAPIKey()
	if err != nil {
		return "", err
	}

	oldKey, ok, _ := s.KeyForUser(ctx, userID)

	pipe := s.client.TxPipeline()
	if ok && oldKey != "" {
		pipe.Del(ctx, fmt.Sprintf("api_key:%s", oldKey))
	}
	pipe.Set(ctx, fmt.Sprintf("api_key:%s", newKey), userID, 0)
	pipe.Set(ctx, fmt.Sprintf("user_api_key:%s", userID), newKey, 0)
	_, err = pipe.Exec(ctx)
	if err != nil {
		return "", err
	}
	return newKey, nil
}

func (s *RedisStore) Close() error {
	return s.client.Close()
}
