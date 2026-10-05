package model

import (
	"context"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
)

func cacheSetToken(token Token) error {
	key := common.GenerateHMAC(token.Key)
	token.Clean()
	err := common.RedisHSetObjUnlessMarked(fmt.Sprintf("token:%s", key), &token, time.Duration(common.RedisKeyCacheSeconds())*time.Second, "_admin_revoked")
	if err != nil {
		return err
	}
	return nil
}

func cacheDeleteToken(key string) error {
	key = common.GenerateHMAC(key)
	err := common.RDB.Eval(context.Background(), `if redis.call('HEXISTS',KEYS[1],'_admin_revoked') == 0 then return redis.call('DEL',KEYS[1]) end return 0`, []string{fmt.Sprintf("token:%s", key)}).Err()
	if err != nil {
		return err
	}
	return nil
}

func revokeTokenCache(key string) error {
	cacheKey := fmt.Sprintf("token:%s", common.GenerateHMAC(key))
	return common.RDB.Eval(context.Background(), `redis.call('DEL',KEYS[1]); redis.call('HSET',KEYS[1],'_admin_revoked','1'); return 1`, []string{cacheKey}).Err()
}

func cacheIncrTokenQuota(key string, increment int64) error {
	key = common.GenerateHMAC(key)
	err := common.RedisHIncrByUnlessMarked(fmt.Sprintf("token:%s", key), constant.TokenFiledRemainQuota, increment, "_admin_revoked")
	if err != nil {
		return err
	}
	return nil
}

func cacheDecrTokenQuota(key string, decrement int64) error {
	return cacheIncrTokenQuota(key, -decrement)
}

func cacheSetTokenField(key string, field string, value string) error {
	key = common.GenerateHMAC(key)
	err := common.RedisHSetFieldUnlessMarked(fmt.Sprintf("token:%s", key), field, value, "_admin_revoked")
	if err != nil {
		return err
	}
	return nil
}

// CacheGetTokenByKey 从缓存中获取 token，如果缓存中不存在，则从数据库中获取
func cacheGetTokenByKey(key string) (*Token, error) {
	hmacKey := common.GenerateHMAC(key)
	if !common.RedisEnabled {
		return nil, fmt.Errorf("redis is not enabled")
	}
	var token Token
	err := common.RedisHGetObj(fmt.Sprintf("token:%s", hmacKey), &token)
	if err != nil {
		return nil, err
	}
	if token.Id <= 0 {
		return nil, fmt.Errorf("token cache revoked")
	}
	token.Key = key
	return &token, nil
}
