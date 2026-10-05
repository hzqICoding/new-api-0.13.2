package controller

import (
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestLogTokenScopeFiltersBeforePagination(t *testing.T) {
	db := openTokenControllerTestDB(t)
	if err := db.AutoMigrate(&model.Log{}); err != nil {
		t.Fatal(err)
	}
	rows := []model.Log{{Id: 1, Type: 2, TokenId: 11, TokenName: "same-name", Quota: 7}, {Id: 2, Type: 2, TokenId: 12, TokenName: "same-name", Quota: 9}}
	for i := 3; i < 253; i++ {
		rows = append(rows, model.Log{Id: i, Type: 2, TokenId: 99, TokenName: "same-name", Quota: 100})
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	r.GET("/logs", GetAllLogs)
	for page := 1; page <= 2; page++ {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", fmt.Sprintf("/logs?type=2&token_ids=11,12,11&p=%d&page_size=1", page), nil))
		var result struct {
			Success bool `json:"success"`
			Data    struct {
				Total    int         `json:"total"`
				Filtered bool        `json:"token_filter_applied"`
				Items    []model.Log `json:"items"`
			} `json:"data"`
		}
		if err := common.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || !result.Success || !result.Data.Filtered || result.Data.Total != 2 || len(result.Data.Items) != 1 || result.Data.Items[0].TokenId == 99 {
			t.Fatalf("filter/pagination failed: %s", w.Body.String())
		}
	}
	for _, invalid := range []string{"", "0", "-1", "11,,12", "x", strings.Repeat("1,", 1000) + "1"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/logs?token_ids="+url.QueryEscape(invalid), nil))
		if w.Code != 400 {
			t.Fatalf("invalid filter accepted %d", w.Code)
		}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/logs?type=2", nil))
	var legacy struct {
		Data struct {
			Total int `json:"total"`
		} `json:"data"`
	}
	common.Unmarshal(w.Body.Bytes(), &legacy)
	if w.Code != 200 || legacy.Data.Total != 252 {
		t.Fatal("legacy unfiltered contract changed")
	}
}

func TestLogWatermarkFiltersBeforePagination(t *testing.T) {
	db := openTokenControllerTestDB(t)
	if err := db.AutoMigrate(&model.Log{}); err != nil {
		t.Fatal(err)
	}
	rows := []model.Log{{Id: 1, Type: 2, TokenId: 11, CreatedAt: 100}, {Id: 2, Type: 2, TokenId: 11, CreatedAt: 100}, {Id: 3, Type: 2, TokenId: 99, CreatedAt: 100}, {Id: 4, Type: 2, TokenId: 11, CreatedAt: 100}}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	r.GET("/logs", GetAllLogs)
	for _, tc := range []struct {
		query string
		total int
		id    int
	}{
		{"after_log_id=1&through_log_id=3", 1, 2},
		{"through_log_id=0", 0, 0},
		{"after_log_id=0&through_log_id=4", 3, 4},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/logs?type=2&token_ids=11&page_size=1&"+tc.query, nil))
		var result struct {
			Data struct {
				Total   int         `json:"total"`
				Items   []model.Log `json:"items"`
				Through bool        `json:"through_log_filter_applied"`
				Token   bool        `json:"token_filter_applied"`
			} `json:"data"`
		}
		if err := common.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || result.Data.Total != tc.total || !result.Data.Through || !result.Data.Token {
			t.Fatalf("wrong bounded snapshot: %s", w.Body.String())
		}
		if tc.total > 0 && (len(result.Data.Items) != 1 || result.Data.Items[0].Id != tc.id) {
			t.Fatalf("wrong ID boundary: %s", w.Body.String())
		}
	}
	for _, invalid := range []string{"after_log_id=-1", "through_log_id=", "through_log_id=x", "after_log_id=1&after_log_id=2", "through_log_id=9223372036854775808"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/logs?"+invalid, nil))
		if w.Code != 400 {
			t.Fatalf("accepted invalid watermark %s", invalid)
		}
	}
}
