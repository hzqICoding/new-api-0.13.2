package model

import (
	"context"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/go-redis/redis/v8"
)

// The old path pauses after TTL has been read; the atomic path pauses before
// its Lua command. Both release the pending update only after revocation.
type revocationMutationHook struct {
	reached chan struct{}
	release chan struct{}
	used    atomic.Bool
}

func (h *revocationMutationHook) pause(ctx context.Context) error {
	if !h.used.CompareAndSwap(false, true) {
		return nil
	}
	close(h.reached)
	select {
	case <-h.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (h *revocationMutationHook) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	if cmd.Name() == "eval" && len(cmd.Args()) > 1 {
		if script, ok := cmd.Args()[1].(string); ok && strings.Contains(script, "local ttl") {
			return ctx, h.pause(ctx)
		}
	}
	return ctx, nil
}
func (h *revocationMutationHook) AfterProcess(ctx context.Context, cmd redis.Cmder) error {
	if cmd.Name() == "ttl" {
		return h.pause(ctx)
	}
	return nil
}
func (h *revocationMutationHook) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	return ctx, nil
}
func (h *revocationMutationHook) AfterProcessPipeline(context.Context, []redis.Cmder) error {
	return nil
}

func mutationTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	address := os.Getenv("CARPOOL_REVIEW_REDIS_ADDR")
	if address == "" {
		t.Skip("set CARPOOL_REVIEW_REDIS_ADDR to a disposable loopback Redis")
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil || host != "127.0.0.1" {
		t.Fatal("test Redis must be loopback")
	}
	client := redis.NewClient(&redis.Options{Addr: address, DB: 14})
	if err := client.Ping(context.Background()).Err(); err != nil {
		client.Close()
		t.Fatal(err)
	}
	oldClient, oldEnabled, oldSync := common.RDB, common.RedisEnabled, common.SyncFrequency
	common.RDB = client
	common.RedisEnabled = true
	common.SyncFrequency = 60
	t.Cleanup(func() {
		common.RDB = oldClient
		common.RedisEnabled = oldEnabled
		common.SyncFrequency = oldSync
		client.Close()
	})
	return client
}

func TestTokenMutationCannotExpireRevocationMarker(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		legacy bool
		update func(string, string) error
	}{
		{"legacy_increment_control", true, func(_ string, cacheKey string) error { return common.RedisHIncrBy(cacheKey, "RemainQuota", -1) }},
		{"legacy_field_control", true, func(_ string, cacheKey string) error { return common.RedisHSetField(cacheKey, "Status", "1") }},
		{"token_increment", false, func(key, _ string) error { return cacheIncrTokenQuota(key, 7) }},
		{"token_decrement", false, func(key, _ string) error { return cacheDecrTokenQuota(key, 3) }},
		{"token_field", false, func(key, _ string) error { return cacheSetTokenField(key, "Status", "1") }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			client := mutationTestRedis(t)
			key, err := common.GenerateKey()
			if err != nil {
				t.Fatal(err)
			}
			cacheKey := "token:" + common.GenerateHMAC(key)
			t.Cleanup(func() { client.Del(context.Background(), cacheKey) })
			snapshot := Token{Id: 71, UserId: 72, Key: key, Status: common.TokenStatusEnabled, RemainQuota: 100, ExpiredTime: -1}
			if err := cacheSetToken(snapshot); err != nil {
				t.Fatal(err)
			}
			hook := &revocationMutationHook{reached: make(chan struct{}), release: make(chan struct{})}
			client.AddHook(hook)
			done := make(chan error, 1)
			go func() { done <- scenario.update(key, cacheKey) }()
			select {
			case <-hook.reached:
			case <-time.After(3 * time.Second):
				close(hook.release)
				t.Fatal("update did not reach deterministic pause")
			}
			err = revokeTokenCache(key)
			close(hook.release)
			if err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("delayed update did not finish")
			}
			ttl, err := client.TTL(context.Background(), cacheKey).Result()
			if err != nil {
				t.Fatal(err)
			}
			if scenario.legacy {
				if ttl <= 0 {
					t.Fatalf("legacy control failed to reproduce expiry race: TTL=%s", ttl)
				}
				t.Log("legacy TTL read followed by late transaction reattached expiration")
				return
			}
			if ttl != -time.Nanosecond {
				t.Fatalf("revocation marker gained an expiry: TTL=%s", ttl)
			}
			fields, err := client.HGetAll(context.Background(), cacheKey).Result()
			if err != nil {
				t.Fatal(err)
			}
			if len(fields) != 1 || fields["_admin_revoked"] != "1" {
				t.Fatalf("late mutation modified tombstone: fields=%v", fields)
			}
			if err := cacheSetToken(snapshot); err != nil {
				t.Fatal(err)
			}
			if _, err := cacheGetTokenByKey(key); err == nil {
				t.Fatal("late mutation or refill revived revoked token")
			}
		})
	}
}

func TestGuardedTokenMutationPreservesNormalCacheSemantics(t *testing.T) {
	client := mutationTestRedis(t)
	key, err := common.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	cacheKey := "token:" + common.GenerateHMAC(key)
	userKey := "user:release-review:" + common.GenerateHMAC(key)
	t.Cleanup(func() { client.Del(context.Background(), cacheKey, userKey) })
	ctx := context.Background()
	if err := cacheSetToken(Token{Id: 81, UserId: 82, Key: key, Status: common.TokenStatusEnabled, RemainQuota: 100, ExpiredTime: -1}); err != nil {
		t.Fatal(err)
	}
	if err := cacheIncrTokenQuota(key, 7); err != nil {
		t.Fatal(err)
	}
	if err := cacheDecrTokenQuota(key, 3); err != nil {
		t.Fatal(err)
	}
	if err := cacheSetTokenField(key, "Name", "updated"); err != nil {
		t.Fatal(err)
	}
	token, err := cacheGetTokenByKey(key)
	if err != nil || token.RemainQuota != 104 || token.Name != "updated" {
		t.Fatalf("normal token update changed: token=%+v err=%v", token, err)
	}
	if ttl := client.TTL(ctx, cacheKey).Val(); ttl <= 0 || ttl > time.Minute {
		t.Fatalf("normal token TTL=%s", ttl)
	}
	if err := client.Persist(ctx, cacheKey).Err(); err != nil {
		t.Fatal(err)
	}
	if err := cacheIncrTokenQuota(key, 7); err != nil {
		t.Fatal(err)
	}
	if err := cacheSetTokenField(key, "Name", "should-not-change"); err != nil {
		t.Fatal(err)
	}
	token, err = cacheGetTokenByKey(key)
	if err != nil || token.RemainQuota != 104 || token.Name != "updated" {
		t.Fatal("persistent hash skip behavior changed")
	}
	client.Del(ctx, cacheKey)
	if err := cacheDecrTokenQuota(key, 3); err != nil {
		t.Fatal(err)
	}
	if err := cacheSetTokenField(key, "Name", "absent"); err != nil {
		t.Fatal(err)
	}
	if client.Exists(ctx, cacheKey).Val() != 0 {
		t.Fatal("mutation created absent token hash")
	}
	if err := client.HSet(ctx, userKey, "Quota", 100, "Status", 1).Err(); err != nil {
		t.Fatal(err)
	}
	client.Expire(ctx, userKey, time.Minute)
	if err := common.RedisHIncrBy(userKey, "Quota", -5); err != nil {
		t.Fatal(err)
	}
	if err := common.RedisHSetField(userKey, "Status", "2"); err != nil {
		t.Fatal(err)
	}
	if client.HGet(ctx, userKey, "Quota").Val() != "95" || client.HGet(ctx, userKey, "Status").Val() != "2" {
		t.Fatal("ordinary user cache helper behavior changed")
	}
}
