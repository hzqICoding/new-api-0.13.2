package model

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	UsageAggregateVersion       = 1
	UsageAggregateMaxRows       = 100000
	UsageAggregateMaxOtherBytes = 64 << 20
	UsageAggregateTimeout       = 5 * time.Second
)

var (
	ErrUsageAggregateRequest = errors.New("invalid usage aggregate request")
	ErrUsageAggregateLimit   = errors.New("usage aggregate limit exceeded")
	ErrUsageAggregateData    = errors.New("invalid usage aggregate data")
)

type UsageAggregateToken struct {
	TokenID int64  `json:"token_id"`
	Bucket  string `json:"bucket"`
}

type UsageAggregateRequest struct {
	Version      int                   `json:"version"`
	Tokens       []UsageAggregateToken `json:"tokens"`
	FromUnix     *int64                `json:"from_unix"`
	ToUnix       *int64                `json:"to_unix"`
	AfterLogID   int64                 `json:"after_log_id"`
	ThroughLogID *int64                `json:"through_log_id"`
}

func (r UsageAggregateRequest) Validate() error {
	if r.Version != UsageAggregateVersion || len(r.Tokens) > 1000 || r.AfterLogID < 0 || r.ThroughLogID != nil && *r.ThroughLogID < 0 {
		return ErrUsageAggregateRequest
	}
	if (r.FromUnix == nil) != (r.ToUnix == nil) || r.FromUnix != nil && *r.ToUnix <= *r.FromUnix {
		return ErrUsageAggregateRequest
	}
	if len(r.Tokens) > 0 && r.FromUnix == nil && r.AfterLogID == 0 && r.ThroughLogID == nil {
		return ErrUsageAggregateRequest
	}
	seen := make(map[int64]bool, len(r.Tokens))
	buckets := make(map[string]bool)
	for _, token := range r.Tokens {
		if token.TokenID <= 0 || seen[token.TokenID] || !utf8.ValidString(token.Bucket) || len(token.Bucket) > 128 || strings.TrimSpace(token.Bucket) == "" {
			return ErrUsageAggregateRequest
		}
		seen[token.TokenID], buckets[token.Bucket] = true, true
	}
	if len(buckets) > 100 {
		return ErrUsageAggregateRequest
	}
	return nil
}

type UsageAggregateTotals struct {
	Requests     int64 `json:"requests"`
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	CachedTokens int64 `json:"cached_tokens"`
	AmountMicro  int64 `json:"amount_micro"`
}

type UsageAggregateItem struct {
	Bucket string `json:"bucket"`
	UsageAggregateTotals
}

type UsageAggregateResult struct {
	Version  int                   `json:"version"`
	Complete bool                  `json:"complete"`
	Scope    UsageAggregateRequest `json:"scope"`
	Totals   UsageAggregateTotals  `json:"totals"`
	Items    []UsageAggregateItem  `json:"items"`
}

type usageAggregateLimits struct {
	rows       int
	otherBytes int64
}

// ReadUsageAggregate performs one bounded SELECT, not COUNT/OFFSET pagination or
// SQL JSON conversion. Go parsing matches the existing carpool usage aggregator.
// On any error the result is nil; a partial sum must never reach the caller.
func ReadUsageAggregate(ctx context.Context, request UsageAggregateRequest) (*UsageAggregateResult, error) {
	ctx, cancel := context.WithTimeout(ctx, UsageAggregateTimeout)
	defer cancel()
	return readUsageAggregate(ctx, LOG_DB, request, usageAggregateLimits{UsageAggregateMaxRows, UsageAggregateMaxOtherBytes})
}

func usageAggregateQuery(db *gorm.DB, request UsageAggregateRequest, tokenIDs []int64, maxRows int) *gorm.DB {
	columns := "id, token_id, quota, prompt_tokens, completion_tokens, other"
	if db.Dialector.Name() == "mysql" {
		// A static, read-only server limit complements cancellation on the
		// client. Other supported dialects retain the five-second context limit.
		columns = "/*+ MAX_EXECUTION_TIME(4000) */ " + columns
	}
	query := db.Table("logs").Select(columns).
		Where("type = ?", LogTypeConsume).Where("token_id IN ?", tokenIDs)
	// Avoid adding an unnecessary primary-key range to ordinary calendar
	// queries; it can alter MySQL's plan despite matching the same rows.
	if request.AfterLogID > 0 {
		query = query.Where("id > ?", request.AfterLogID)
	}
	if request.ThroughLogID != nil {
		query = query.Where("id <= ?", *request.ThroughLogID)
	}
	if request.FromUnix != nil {
		query = query.Where("created_at >= ? AND created_at < ?", *request.FromUnix, *request.ToUnix)
	}
	return query.Order("id DESC").Limit(maxRows + 1)
}

func readUsageAggregate(ctx context.Context, db *gorm.DB, request UsageAggregateRequest, limits usageAggregateLimits) (*UsageAggregateResult, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := &UsageAggregateResult{Version: UsageAggregateVersion, Scope: request, Items: make([]UsageAggregateItem, 0)}
	buckets := make(map[string]*UsageAggregateItem)
	tokenBuckets := make(map[int64]*UsageAggregateItem, len(request.Tokens))
	tokenIDs := make([]int64, 0, len(request.Tokens))
	for _, token := range request.Tokens {
		item := buckets[token.Bucket]
		if item == nil {
			item = &UsageAggregateItem{Bucket: token.Bucket}
			buckets[token.Bucket] = item
		}
		tokenBuckets[token.TokenID] = item
		tokenIDs = append(tokenIDs, token.TokenID)
	}
	empty := len(tokenIDs) == 0 || request.ThroughLogID != nil && *request.ThroughLogID <= request.AfterLogID
	if !empty {
		if db == nil {
			return nil, errors.New("usage aggregate storage unavailable")
		}
		rows, err := usageAggregateQuery(db.WithContext(ctx), request, tokenIDs, limits.rows).Rows()
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var count int
		var otherBytes int64
		var previousID int64
		for rows.Next() {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			count++
			if count > limits.rows {
				return nil, ErrUsageAggregateLimit
			}
			var id, tokenID int64
			// The legacy GORM Log uses scalar ints; a SQL NULL becomes zero there.
			var quota, input, output sql.NullInt64
			var other sql.RawBytes
			if err := rows.Scan(&id, &tokenID, &quota, &input, &output, &other); err != nil {
				return nil, err
			}
			item := tokenBuckets[tokenID]
			if item == nil || id <= request.AfterLogID || previousID != 0 && id >= previousID || request.ThroughLogID != nil && id > *request.ThroughLogID {
				return nil, ErrUsageAggregateData
			}
			previousID = id
			if int64(len(other)) > limits.otherBytes-otherBytes {
				return nil, ErrUsageAggregateLimit
			}
			otherBytes += int64(len(other))
			if quota.Int64 < 0 || quota.Int64 > math.MaxInt64/2 {
				return nil, ErrUsageAggregateData
			}
			value := UsageAggregateTotals{Requests: 1, InputTokens: input.Int64, OutputTokens: output.Int64, CachedTokens: usageAggregateCachedTokens(other), AmountMicro: quota.Int64 * 2}
			if err := addUsageAggregateTotals(&item.UsageAggregateTotals, value); err != nil {
				return nil, err
			}
			if err := addUsageAggregateTotals(&result.Totals, value); err != nil {
				return nil, err
			}
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, item := range buckets {
		result.Items = append(result.Items, *item)
	}
	sort.Slice(result.Items, func(i, j int) bool { return result.Items[i].Bucket < result.Items[j].Bucket })
	result.Complete = true
	return result, nil
}

// Keep the exact two-stage parser, including numeric strings, last duplicate
// object member, and invalid-value zero fallback. SQL casts/regexes differ here.
func usageAggregateCachedTokens(raw []byte) int64 {
	if len(bytes.TrimSpace(raw)) == 0 {
		return 0
	}
	var values map[string]json.RawMessage
	if common.Unmarshal(raw, &values) != nil {
		return 0
	}
	value := values["cache_tokens"]
	if len(value) == 0 {
		return 0
	}
	var number json.Number
	if common.Unmarshal(value, &number) != nil {
		return 0
	}
	parsed, err := number.Int64()
	if err != nil || parsed < 0 {
		return 0
	}
	return parsed
}

func addUsageAggregateTotals(target *UsageAggregateTotals, value UsageAggregateTotals) error {
	pairs := []struct {
		target *int64
		value  int64
	}{
		{&target.Requests, value.Requests}, {&target.InputTokens, value.InputTokens},
		{&target.OutputTokens, value.OutputTokens}, {&target.CachedTokens, value.CachedTokens}, {&target.AmountMicro, value.AmountMicro},
	}
	for _, pair := range pairs {
		if pair.value > 0 && *pair.target > math.MaxInt64-pair.value || pair.value < 0 && *pair.target < math.MinInt64-pair.value {
			return ErrUsageAggregateData
		}
		*pair.target += pair.value
	}
	return nil
}
