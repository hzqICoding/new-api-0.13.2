package controller

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func aggregateControllerFixture(t *testing.T) (*gin.Engine, *gorm.DB) {
	t.Helper()
	oldDB, oldLogDB := model.DB, model.LOG_DB
	db := openTokenControllerTestDB(t)
	t.Cleanup(func() { model.DB, model.LOG_DB = oldDB, oldLogDB })
	if err := db.AutoMigrate(&model.Log{}); err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	r.POST("/aggregate", AggregateUsage)
	return r, db
}

func aggregateControllerCall(r http.Handler, body string, ctx context.Context) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/aggregate", strings.NewReader(body)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	r.ServeHTTP(response, request)
	return response
}

func TestUsageAggregateControllerFeatureGateAndErrors(t *testing.T) {
	r, db := aggregateControllerFixture(t)
	body := `{"version":1,"tokens":[{"token_id":11,"bucket":"seat"}],"from_unix":0,"to_unix":100,"after_log_id":0,"through_log_id":null}`
	for _, enabled := range []string{"", "false", "1", "invalid"} {
		t.Setenv("CARPOOL_USAGE_AGGREGATE_ENABLED", enabled)
		response := aggregateControllerCall(r, body, context.Background())
		if response.Code != 503 || response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Body.String(), "usage_aggregate_disabled") {
			t.Fatalf("gate=%s %d %s", enabled, response.Code, response.Body.String())
		}
	}
	t.Setenv("CARPOOL_USAGE_AGGREGATE_ENABLED", " true ")
	for _, invalid := range []string{
		``, `null`, `[]`, `{}`, `{"version":1.0}`, `{"version":2,"tokens":[]}`, `{"version":1,"tokens":[{"token_id":1,"bucket":"a"}]}`,
		`{"version":1,"tokens":[],"from_unix":0}`, `{"version":1,"tokens":[],"from_unix":0,"to_unix":0}`,
		`{"version":1,"tokens":[],"after_log_id":-1}`, `{"version":1,"tokens":[],"through_log_id":-1}`,
		`{"version":1,"tokens":[{"token_id":-1,"bucket":"a"}],"through_log_id":0}`,
		`{"version":1,"tokens":[{"token_id":1,"bucket":"a"},{"token_id":1,"bucket":"b"}],"through_log_id":0}`,
		`{"version":1,"tokens":[{"token_id":1,"bucket":""}],"through_log_id":0}`,
		`{"version":1,"tokens":[],"through_log_id":9223372036854775808}`, body + `{}`,
		`{"version":1,"tokens":[{"token_id":1,"bucket":"` + string([]byte{255}) + `"}],"through_log_id":0}`,
	} {
		response := aggregateControllerCall(r, invalid, context.Background())
		if response.Code != 400 || strings.Contains(response.Body.String(), `"data"`) {
			t.Fatalf("bad request=%q: %d %s", invalid, response.Code, response.Body.String())
		}
	}
	oversized := aggregateControllerCall(r, strings.Repeat(" ", usageAggregateMaxBodyBytes+1), context.Background())
	if oversized.Code != 413 {
		t.Fatalf("oversized=%d %s", oversized.Code, oversized.Body.String())
	}
	usageAggregateSlots <- struct{}{}
	usageAggregateSlots <- struct{}{}
	busy := aggregateControllerCall(r, body, context.Background())
	<-usageAggregateSlots
	<-usageAggregateSlots
	if busy.Code != 503 || !strings.Contains(busy.Body.String(), "usage_aggregate_busy") {
		t.Fatalf("busy=%d %s", busy.Code, busy.Body.String())
	}
	if err := db.Create(&model.Log{Id: 1, Type: 2, TokenId: 11, CreatedAt: 5, Quota: -1, Other: "private-other-value"}).Error; err != nil {
		t.Fatal(err)
	}
	invalidData := aggregateControllerCall(r, body, context.Background())
	if invalidData.Code != 422 || strings.Contains(invalidData.Body.String(), `"data"`) || strings.Contains(invalidData.Body.String(), "private-other-value") {
		t.Fatalf("invalid data=%d %s", invalidData.Code, invalidData.Body.String())
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	timedOut := aggregateControllerCall(r, body, ctx)
	if timedOut.Code != 504 {
		t.Fatalf("deadline=%d %s", timedOut.Code, timedOut.Body.String())
	}
	if len(usageAggregateSlots) != 0 {
		t.Fatal("error path leaked aggregate slot")
	}
	if err := db.Migrator().DropTable(&model.Log{}); err != nil {
		t.Fatal(err)
	}
	storageFailure := aggregateControllerCall(r, body, context.Background())
	if storageFailure.Code != 500 || storageFailure.Body.String() != `{"message":"usage_aggregate_unavailable","success":false}` {
		t.Fatalf("SQL error leaked=%d %s", storageFailure.Code, storageFailure.Body.String())
	}
}

func TestUsageAggregateControllerCompleteEchoAndZeroBuckets(t *testing.T) {
	r, db := aggregateControllerFixture(t)
	t.Setenv("CARPOOL_USAGE_AGGREGATE_ENABLED", "TRUE")
	if err := db.Create(&[]model.Log{{Id: 1, Type: 2, TokenId: 11, CreatedAt: 0, Quota: 7, PromptTokens: -2, CompletionTokens: 4, Other: `{"cache_tokens":"3"}`}, {Id: 2, Type: 5, TokenId: 11, CreatedAt: 0, Quota: 1000}}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.Log{}).Where("id IN ?", []int{1, 2}).Update("created_at", 0).Error; err != nil {
		t.Fatal(err)
	}
	body := `{"version":1,"tokens":[{"token_id":13,"bucket":"z"},{"token_id":11,"bucket":"A"},{"token_id":12,"bucket":"A"}],"from_unix":0,"to_unix":1,"after_log_id":0,"through_log_id":null}`
	response := aggregateControllerCall(r, body, context.Background())
	var envelope struct {
		Success bool                       `json:"success"`
		Data    model.UsageAggregateResult `json:"data"`
	}
	if err := common.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || !envelope.Success || !envelope.Data.Complete || envelope.Data.Version != 1 || envelope.Data.Scope.Tokens[0].TokenID != 13 || envelope.Data.Scope.FromUnix == nil || *envelope.Data.Scope.FromUnix != 0 || envelope.Data.Scope.ThroughLogID != nil {
		t.Fatalf("echo=%d %s", response.Code, response.Body.String())
	}
	if envelope.Data.Totals != (model.UsageAggregateTotals{Requests: 1, InputTokens: -2, OutputTokens: 4, CachedTokens: 3, AmountMicro: 14}) || len(envelope.Data.Items) != 2 || envelope.Data.Items[0].Bucket != "A" || envelope.Data.Items[1].Bucket != "z" {
		t.Fatalf("values=%+v", envelope.Data)
	}
	var raw map[string]interface{}
	if err := common.Unmarshal(response.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	data := raw["data"].(map[string]interface{})
	zero := data["items"].([]interface{})[1].(map[string]interface{})
	for _, key := range []string{"requests", "input_tokens", "output_tokens", "cached_tokens", "amount_micro"} {
		if value, exists := zero[key]; !exists || value != float64(0) {
			t.Fatalf("zero %s missing=%v", key, zero)
		}
	}
	for _, query := range []string{`{"version":1,"tokens":[]}`, `{"version":1,"tokens":[{"token_id":11,"bucket":"empty"}],"through_log_id":0}`} {
		w := aggregateControllerCall(r, query, context.Background())
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"complete":true`) || !strings.Contains(w.Body.String(), `"requests":0`) {
			t.Fatalf("empty=%d %s", w.Code, w.Body.String())
		}
	}
}

func TestUsageAggregateControllerActualConcurrentAdmission(t *testing.T) {
	r, db := aggregateControllerFixture(t)
	t.Setenv("CARPOOL_USAGE_AGGREGATE_ENABLED", "true")
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	if err := db.Callback().Row().Before("gorm:row").Register("hold_aggregate_queries", func(*gorm.DB) { started <- struct{}{}; <-release }); err != nil {
		t.Fatal(err)
	}
	defer db.Callback().Row().Remove("hold_aggregate_queries")
	body := `{"version":1,"tokens":[{"token_id":11,"bucket":"a"}],"from_unix":0,"to_unix":1}`
	done := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		go func() { done <- aggregateControllerCall(r, body, context.Background()) }()
	}
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			close(release)
			t.Fatal("workers did not reach SQL")
		}
	}
	third := aggregateControllerCall(r, body, context.Background())
	if third.Code != 503 {
		close(release)
		t.Fatalf("third aggregate not rejected: %d", third.Code)
	}
	close(release)
	for range 2 {
		select {
		case response := <-done:
			if response.Code != 200 {
				t.Fatal(fmt.Sprint(response.Code, response.Body.String()))
			}
		case <-time.After(time.Second):
			t.Fatal("inflight aggregate stuck")
		}
	}
	if len(usageAggregateSlots) != 0 {
		t.Fatal("successful requests leaked slots")
	}
}
