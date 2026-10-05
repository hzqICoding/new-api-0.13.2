package model

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/go-redis/redis/v8"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Simulate a DB snapshot already read by GetTokenByKey before its asynchronous
// cache fill runs. Deliberately execute that fill after successful revocation.
func TestReviewAdminRevokeRejectsLateCacheFill(t *testing.T) {
	addr := os.Getenv("CARPOOL_REVIEW_REDIS_ADDR")
	if addr == "" {
		t.Skip("set CARPOOL_REVIEW_REDIS_ADDR to a disposable loopback Redis")
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host != "127.0.0.1" {
		t.Fatal("review Redis must be loopback")
	}
	oldDB, oldRDB, oldEnabled := DB, common.RDB, common.RedisEnabled
	oldSQLite, oldMySQL, oldPostgres := common.UsingSQLite, common.UsingMySQL, common.UsingPostgreSQL
	oldKey, oldGroup, oldTrue, oldFalse := commonKeyCol, commonGroupCol, commonTrueVal, commonFalseVal
	oldSync := common.SyncFrequency
	t.Cleanup(func() {
		DB = oldDB
		common.RDB = oldRDB
		common.RedisEnabled = oldEnabled
		common.UsingSQLite = oldSQLite
		common.UsingMySQL = oldMySQL
		common.UsingPostgreSQL = oldPostgres
		commonKeyCol = oldKey
		commonGroupCol = oldGroup
		commonTrueVal = oldTrue
		commonFalseVal = oldFalse
		common.SyncFrequency = oldSync
	})
	common.UsingSQLite = true
	common.UsingMySQL = false
	common.UsingPostgreSQL = false
	common.SyncFrequency = 60
	initCol()
	DB, err = gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err = DB.AutoMigrate(&Token{}, &User{}, &AdminResourceOperation{}); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := DB.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	redisClient := redis.NewClient(&redis.Options{Addr: addr, DB: 13})
	defer redisClient.Close()
	if err = redisClient.Ping(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	common.RDB = redisClient
	common.RedisEnabled = true
	raw := make([]byte, 24)
	if _, err = rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	token := Token{UserId: 12, Name: "review-late-cache", Key: hex.EncodeToString(raw), Status: common.TokenStatusEnabled, UnlimitedQuota: true, ExpiredTime: -1, Group: "default"}
	if err = DB.Create(&token).Error; err != nil {
		t.Fatal(err)
	}
	cachedKey := "token:" + common.GenerateHMAC(token.Key)
	defer redisClient.Del(context.Background(), cachedKey)
	if err = cacheSetToken(token); err != nil {
		t.Fatal(err)
	}
	if _, err = ValidateUserToken(token.Key); err != nil {
		t.Fatal("fixture live token must validate before revoke")
	}
	if err = cacheIncrTokenQuota(token.Key, 7); err != nil {
		t.Fatal(err)
	}
	cached, err := cacheGetTokenByKey(token.Key)
	if err != nil || cached.RemainQuota != 7 {
		t.Fatal("ordinary token quota cache update regressed")
	}
	if err = cacheDeleteToken(token.Key); err != nil {
		t.Fatal(err)
	}
	if err = cacheSetToken(token); err != nil {
		t.Fatal(err)
	}
	if _, err = ValidateUserToken(token.Key); err != nil {
		t.Fatal("ordinary invalidation incorrectly prevented cache refill")
	}
	staleSnapshot := token
	if err = AdminRevokeToken(token.UserId, token.Id); err != nil {
		t.Fatal(err)
	}
	// The production gopool goroutine invokes this exact function on its snapshot.
	fillErr := cacheSetToken(staleSnapshot)
	if _, err = ValidateUserToken(token.Key); err == nil {
		t.Fatal("revoked token became valid after the old asynchronous cache fill")
	}
	if fillErr != nil && !strings.Contains(fillErr.Error(), "revok") {
		t.Log("late cache fill returned a safe rejection/error")
	}
}
