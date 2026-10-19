package model

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func aggregateInt(value int64) *int64 { return &value }

func aggregateTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.TempDir()+"/aggregate.db"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&Log{}); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func aggregateRequest() UsageAggregateRequest {
	return UsageAggregateRequest{Version: 1, Tokens: []UsageAggregateToken{{11, "A"}, {12, "A"}, {13, "a"}, {14, "zero"}}, FromUnix: aggregateInt(0), ToUnix: aggregateInt(101)}
}

func aggregateRead(t *testing.T, db *gorm.DB, request UsageAggregateRequest) *UsageAggregateResult {
	t.Helper()
	result, err := readUsageAggregate(context.Background(), db, request, usageAggregateLimits{UsageAggregateMaxRows, UsageAggregateMaxOtherBytes})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestUsageAggregateSingleReadExactScopeAndBuckets(t *testing.T) {
	db := aggregateTestDB(t)
	rows := []Log{
		{Id: 1, Type: 2, TokenId: 11, CreatedAt: 0, Quota: 0, PromptTokens: -2, CompletionTokens: -1, Other: `{"cache_tokens":"3"}`},
		{Id: 2, Type: 2, TokenId: 12, CreatedAt: 100, Quota: 5, PromptTokens: 10, CompletionTokens: 3, Other: `{"cache_tokens":1,"cache_tokens":9}`},
		{Id: 3, Type: 2, TokenId: 13, CreatedAt: 50, Quota: 7, PromptTokens: 20, CompletionTokens: 4, Other: `{"cache_tokens":"1\n"}`},
		{Id: 4, Type: 5, TokenId: 11, CreatedAt: 20, Quota: 999, PromptTokens: 999, Other: `{"cache_tokens":999}`},
		{Id: 5, Type: 6, TokenId: 11, CreatedAt: 20, Quota: 999},
		{Id: 6, Type: 2, TokenId: 99, CreatedAt: 20, Quota: -1},
		{Id: 7, Type: 2, TokenId: 11, CreatedAt: 101, Quota: -1},
		{Id: 8, Type: 2, TokenId: 11, CreatedAt: -1, Quota: -1},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	// GORM treats integer CreatedAt=0 as an unset auto-create timestamp.
	// Persist epoch zero explicitly so the half-open boundary is actually tested.
	if err := db.Model(&Log{}).Where("id = ?", 1).Update("created_at", 0).Error; err != nil {
		t.Fatal(err)
	}
	var calls int
	if err := db.Callback().Row().Before("gorm:row").Register("aggregate_single_select", func(*gorm.DB) { calls++ }); err != nil {
		t.Fatal(err)
	}
	request := aggregateRequest()
	result := aggregateRead(t, db, request)
	if calls != 1 || !result.Complete || result.Version != 1 || !reflect.DeepEqual(result.Scope, request) {
		t.Fatalf("scope/SQL count: calls=%d result=%+v", calls, result)
	}
	want := UsageAggregateTotals{Requests: 3, InputTokens: 28, OutputTokens: 6, CachedTokens: 12, AmountMicro: 24}
	if result.Totals != want {
		t.Fatalf("totals=%+v want=%+v", result.Totals, want)
	}
	if len(result.Items) != 3 || result.Items[0].Bucket != "A" || result.Items[1].Bucket != "a" || result.Items[2].Bucket != "zero" || result.Items[0].Requests != 2 || result.Items[1].Requests != 1 || result.Items[2].UsageAggregateTotals != (UsageAggregateTotals{}) {
		t.Fatalf("bucket mapping/collation/empty=%+v", result.Items)
	}
	request.AfterLogID, request.ThroughLogID = 1, aggregateInt(2)
	bounded := aggregateRead(t, db, request)
	if bounded.Totals.Requests != 1 || bounded.Totals.AmountMicro != 10 {
		t.Fatalf("watermarks=%+v", bounded)
	}
	request.FromUnix, request.ToUnix = nil, nil
	if got := aggregateRead(t, db, request); got.Totals != bounded.Totals {
		t.Fatal("bounded log-only scope changed")
	}
	request.ThroughLogID = aggregateInt(0)
	before := calls
	if got := aggregateRead(t, nil, request); got.Totals != (UsageAggregateTotals{}) || len(got.Items) != 3 || calls != before {
		t.Fatal("explicit zero upper bound did not return empty without SQL")
	}
	empty := UsageAggregateRequest{Version: 1, Tokens: []UsageAggregateToken{}}
	if got := aggregateRead(t, nil, empty); len(got.Items) != 0 || !got.Complete || got.Scope.Tokens == nil {
		t.Fatalf("empty-token scope=%+v", got)
	}
}

func TestUsageAggregateCachedTokensExactParser(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want int64
	}{
		{`{"cache_tokens":123}`, 123}, {`{"cache_tokens":"123"}`, 123}, {`{"cache_tokens":"01"}`, 0},
		{`{"cache_tokens":"+1"}`, 0}, {`{"cache_tokens":" 1"}`, 0}, {`{"cache_tokens":"1 "}`, 0},
		{`{"cache_tokens":"1\n"}`, 0}, {`{"cache_tokens":"1\r\n"}`, 0}, {`{"cache_tokens":"1e2"}`, 0},
		{`{"cache_tokens":1e2}`, 0}, {`{"cache_tokens":1.0}`, 0}, {`{"cache_tokens":1.5}`, 0},
		{`{"cache_tokens":"-0"}`, 0}, {`{"cache_tokens":-1}`, 0}, {`{"cache_tokens":true}`, 0},
		{`{"cache_tokens":null}`, 0}, {`{"cache_tokens":[1]}`, 0}, {`{"cache_tokens":{}}`, 0},
		{`{"cache_tokens":9223372036854775807}`, math.MaxInt64}, {`{"cache_tokens":"9223372036854775807"}`, math.MaxInt64},
		{`{"cache_tokens":9223372036854775808}`, 0}, {`{"cache_tokens":"9223372036854775808"}`, 0},
		{`{"cache_tokens":1,"cache_tokens":9}`, 9}, {`{"cache_tokens":9,"cache_tokens":"bad"}`, 0},
		{`{"cache_read_input_tokens":30,"prompt_tokens_details":{"cached_tokens":40}}`, 0},
		{`{"cache_tokens":2,"cache_read_input_tokens":30,"prompt_tokens_details":{"cached_tokens":40}}`, 2},
		{`bad`, 0}, {``, 0}, {`null`, 0}, {`[]`, 0}, {`{"cache_tokens":1} trailing`, 0},
	} {
		if got := usageAggregateCachedTokens([]byte(tc.raw)); got != tc.want {
			t.Errorf("%s => %d want %d", tc.raw, got, tc.want)
		}
	}
}

func TestUsageAggregateValidationAndLimits(t *testing.T) {
	base := aggregateRequest()
	for name, change := range map[string]func(*UsageAggregateRequest){
		"version":          func(r *UsageAggregateRequest) { r.Version = 2 },
		"negative token":   func(r *UsageAggregateRequest) { r.Tokens = []UsageAggregateToken{{-1, "a"}} },
		"duplicate token":  func(r *UsageAggregateRequest) { r.Tokens = []UsageAggregateToken{{11, "a"}, {11, "b"}} },
		"missing time end": func(r *UsageAggregateRequest) { r.ToUnix = nil },
		"equal time":       func(r *UsageAggregateRequest) { r.ToUnix = r.FromUnix },
		"unbounded":        func(r *UsageAggregateRequest) { r.FromUnix, r.ToUnix = nil, nil },
		"negative after":   func(r *UsageAggregateRequest) { r.AfterLogID = -1 },
		"negative through": func(r *UsageAggregateRequest) { r.ThroughLogID = aggregateInt(-1) },
		"empty bucket":     func(r *UsageAggregateRequest) { r.Tokens = []UsageAggregateToken{{11, " "}} },
		"invalid UTF8":     func(r *UsageAggregateRequest) { r.Tokens = []UsageAggregateToken{{11, string([]byte{255})}} },
		"large bucket":     func(r *UsageAggregateRequest) { r.Tokens = []UsageAggregateToken{{11, strings.Repeat("中", 43)}} },
		"many tokens": func(r *UsageAggregateRequest) {
			r.Tokens = make([]UsageAggregateToken, 1001)
			for i := range r.Tokens {
				r.Tokens[i] = UsageAggregateToken{int64(i + 1), "one"}
			}
		},
		"many buckets": func(r *UsageAggregateRequest) {
			r.Tokens = make([]UsageAggregateToken, 101)
			for i := range r.Tokens {
				r.Tokens[i] = UsageAggregateToken{int64(i + 1), fmt.Sprint(i)}
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			request := base
			change(&request)
			if !errors.Is(request.Validate(), ErrUsageAggregateRequest) {
				t.Fatal("invalid request accepted")
			}
		})
	}
	maximal := base
	maximal.Tokens = make([]UsageAggregateToken, 1000)
	for i := range maximal.Tokens {
		maximal.Tokens[i] = UsageAggregateToken{int64(i + 1), fmt.Sprintf("%d", i%100)}
	}
	if err := maximal.Validate(); err != nil {
		t.Fatal(err)
	}
	db := aggregateTestDB(t)
	rows := []Log{{Id: 1, Type: 2, TokenId: 11, CreatedAt: 1, Other: "12345"}, {Id: 2, Type: 2, TokenId: 11, CreatedAt: 1, Other: "12345"}, {Id: 3, Type: 2, TokenId: 11, CreatedAt: 1, Other: "1"}}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	for _, limits := range []usageAggregateLimits{{2, 100}, {100, 10}} {
		got, err := readUsageAggregate(context.Background(), db, base, limits)
		if !errors.Is(err, ErrUsageAggregateLimit) || got != nil {
			t.Fatalf("partial result leaked: got=%+v err=%v", got, err)
		}
	}
	if got, err := readUsageAggregate(context.Background(), db, base, usageAggregateLimits{3, 11}); err != nil || got.Totals.Requests != 3 {
		t.Fatalf("exact limits failed: %+v %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := readUsageAggregate(ctx, db, base, usageAggregateLimits{100, 100}); !errors.Is(err, context.Canceled) || got != nil {
		t.Fatalf("cancelled=%+v %v", got, err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	if err := db.Callback().Row().After("gorm:row").Register("cancel_aggregate_after_query", func(*gorm.DB) { cancel() }); err != nil {
		t.Fatal(err)
	}
	if got, err := readUsageAggregate(ctx, db, base, usageAggregateLimits{100, 100}); !errors.Is(err, context.Canceled) || got != nil {
		t.Fatalf("cancel during scan=%+v %v", got, err)
	}
}

func TestUsageAggregateRejectsBadQuotaAndOrderedOverflow(t *testing.T) {
	for _, tc := range []struct {
		name string
		rows []Log
	}{
		{"negative quota", []Log{{Id: 1, Quota: -1}}},
		{"single quota overflow", []Log{{Id: 1, Quota: int(math.MaxInt64/2 + 1)}}},
		{"total quota overflow", []Log{{Id: 1, Quota: 1}, {Id: 2, Quota: int(math.MaxInt64 / 2)}}},
		{"input prefix overflow despite final valid", []Log{{Id: 1, PromptTokens: -1}, {Id: 2, PromptTokens: 1}, {Id: 3, PromptTokens: math.MaxInt64}}},
		{"output underflow", []Log{{Id: 1, CompletionTokens: -1}, {Id: 2, CompletionTokens: math.MinInt64}}},
		{"cached overflow", []Log{{Id: 1, Other: `{"cache_tokens":1}`}, {Id: 2, Other: `{"cache_tokens":9223372036854775807}`}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := aggregateTestDB(t)
			for i := range tc.rows {
				tc.rows[i].Type = 2
				tc.rows[i].TokenId = 11
				tc.rows[i].CreatedAt = 1
			}
			if err := db.Create(&tc.rows).Error; err != nil {
				t.Fatal(err)
			}
			got, err := readUsageAggregate(context.Background(), db, aggregateRequest(), usageAggregateLimits{100, 1000})
			if !errors.Is(err, ErrUsageAggregateData) || got != nil {
				t.Fatalf("invalid result=%+v err=%v", got, err)
			}
		})
	}
}

func TestUsageAggregateSQLAcrossDialects(t *testing.T) {
	for _, tc := range []struct {
		name    string
		dialect gorm.Dialector
	}{
		{"sqlite", sqlite.Open(":memory:")},
		{"mysql", mysql.New(mysql.Config{DSN: "unused:unused@tcp(127.0.0.1:1)/unused", SkipInitializeWithVersion: true})},
		{"postgres", postgres.New(postgres.Config{DSN: "host=127.0.0.1 port=1 user=unused dbname=unused", PreferSimpleProtocol: true})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := gorm.Open(tc.dialect, &gorm.Config{DryRun: true, DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent)})
			if err != nil {
				t.Fatal(err)
			}
			if sqlDB, err := db.DB(); err == nil {
				defer sqlDB.Close()
			}
			r := aggregateRequest()
			r.AfterLogID = 10
			r.ThroughLogID = aggregateInt(20)
			sqlText := db.ToSQL(func(tx *gorm.DB) *gorm.DB {
				return usageAggregateQuery(tx, r, []int64{11, 12}, UsageAggregateMaxRows).Find(&[]Log{})
			})
			for _, part := range []string{"id, token_id, quota, prompt_tokens, completion_tokens, other", "type = 2", "token_id IN (11,12)", "id > 10", "id <= 20", "created_at >= 0 AND created_at < 101", "ORDER BY id DESC", "LIMIT 100001"} {
				if !strings.Contains(sqlText, part) {
					t.Errorf("missing %q in %s", part, sqlText)
				}
			}
			if strings.Contains(sqlText, "MAX_EXECUTION_TIME(4000)") != (tc.name == "mysql") {
				t.Fatalf("wrong dialect timeout: %s", sqlText)
			}
			r.AfterLogID = 0
			r.ThroughLogID = nil
			ordinary := db.ToSQL(func(tx *gorm.DB) *gorm.DB {
				return usageAggregateQuery(tx, r, []int64{11}, UsageAggregateMaxRows).Find(&[]Log{})
			})
			if strings.Contains(ordinary, "id >") || strings.Contains(ordinary, "id <=") {
				t.Fatalf("unbounded calendar query added primary-key range: %s", ordinary)
			}
			for _, forbidden := range []string{"COUNT(", "SUM(", "JSON_", "OFFSET", "FOR UPDATE"} {
				if strings.Contains(sqlText, forbidden) {
					t.Errorf("unexpected %s in %s", forbidden, sqlText)
				}
			}
		})
	}
}

func TestUsageAggregateTimeoutIsBounded(t *testing.T) {
	if UsageAggregateTimeout != 5*time.Second || UsageAggregateMaxRows != 100000 || UsageAggregateMaxOtherBytes != 64<<20 {
		t.Fatal("production bounds changed")
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if got, err := ReadUsageAggregate(ctx, aggregateRequest()); got != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired context=%+v %v", got, err)
	}
}
