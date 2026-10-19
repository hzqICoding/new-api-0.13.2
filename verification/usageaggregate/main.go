// A one-shot, read-only production-data probe. Does not invoke application main,
// migrations, jobs or relay endpoints. Credentials arrive only on stdin.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	driver "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type input struct {
	DSN        string   `json:"dsn"`
	LogDSN     string   `json:"log_dsn"`
	AdminToken string   `json:"admin_token"`
	AdminUser  string   `json:"admin_user"`
	Cases      []sample `json:"cases"`
}
type sample struct {
	Name     string                                `json:"name"`
	Request  model.UsageAggregateRequest           `json:"request"`
	Expected map[string]model.UsageAggregateTotals `json:"expected"`
}

func openReadOnly(raw string) (*gorm.DB, error) {
	cfg, err := driver.ParseDSN(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid local database configuration")
	}
	if cfg.Params == nil {
		cfg.Params = make(map[string]string)
	}
	cfg.Params["transaction_read_only"] = "1"
	cfg.Params["max_execution_time"] = "4000"
	cfg.Timeout = 3 * time.Second
	cfg.ReadTimeout = 6 * time.Second
	cfg.WriteTimeout = 2 * time.Second
	db, err := gorm.Open(mysql.New(mysql.Config{DSN: cfg.FormatDSN(), SkipInitializeWithVersion: true}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent), DisableAutomaticPing: true})
	if err != nil {
		return nil, fmt.Errorf("readonly database initialization failed")
	}
	pool, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("database handle unavailable")
	}
	pool.SetMaxOpenConns(1)
	pool.SetMaxIdleConns(1)
	return db, nil
}

func run() error {
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	if err != nil {
		return fmt.Errorf("stdin unavailable")
	}
	var in input
	if common.Unmarshal(raw, &in) != nil || len(in.Cases) == 0 || len(in.Cases) > 4 || in.AdminToken == "" {
		return fmt.Errorf("invalid probe input")
	}
	model.DB, err = openReadOnly(in.DSN)
	if err != nil {
		return err
	}
	if in.LogDSN == "" || in.LogDSN == in.DSN {
		model.LOG_DB = model.DB
	} else {
		model.LOG_DB, err = openReadOnly(in.LogDSN)
		if err != nil {
			return err
		}
	}
	defer func() {
		if p, e := model.DB.DB(); e == nil {
			_ = p.Close()
		}
		if model.LOG_DB != model.DB {
			if p, e := model.LOG_DB.DB(); e == nil {
				_ = p.Close()
			}
		}
	}()
	var readonly int
	if model.LOG_DB.Raw("SELECT @@session.transaction_read_only").Scan(&readonly).Error != nil || readonly != 1 {
		return fmt.Errorf("read-only session guard failed")
	}
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	secret := make([]byte, 32)
	if _, err = rand.Read(secret); err != nil {
		return fmt.Errorf("session key unavailable")
	}
	engine.Use(sessions.Sessions("probe", cookie.NewStore(secret)))
	engine.POST("/api/log/usage/aggregate", middleware.AdminAuth(), controller.AggregateUsage)
	server := httptest.NewServer(engine)
	defer server.Close()
	client := server.Client()
	client.Timeout = 7 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	invoke := func(request model.UsageAggregateRequest, auth bool) (int, []byte, float64, error) {
		body, e := common.Marshal(request)
		if e != nil {
			return 0, nil, 0, e
		}
		req, e := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/api/log/usage/aggregate", bytes.NewReader(body))
		if e != nil {
			return 0, nil, 0, e
		}
		req.Header.Set("Content-Type", "application/json")
		if auth {
			req.Header.Set("Authorization", in.AdminToken)
			req.Header.Set("New-Api-User", in.AdminUser)
		}
		start := time.Now()
		res, e := client.Do(req)
		if e != nil {
			return 0, nil, 0, fmt.Errorf("probe HTTP transport failed")
		}
		defer res.Body.Close()
		data, e := io.ReadAll(io.LimitReader(res.Body, 2<<20))
		return res.StatusCode, data, time.Since(start).Seconds(), e
	}
	_ = os.Setenv("CARPOOL_USAGE_AGGREGATE_ENABLED", "true")
	status, _, _, err := invoke(in.Cases[0].Request, false)
	if err != nil || status != 401 {
		return fmt.Errorf("anonymous guard did not reject request")
	}
	_ = os.Setenv("CARPOOL_USAGE_AGGREGATE_ENABLED", "false")
	status, _, _, err = invoke(in.Cases[0].Request, true)
	if err != nil || status != 503 {
		return fmt.Errorf("disabled guard did not reject request")
	}
	_ = os.Setenv("CARPOOL_USAGE_AGGREGATE_ENABLED", "true")
	results := make([]map[string]any, 0, len(in.Cases))
	for _, test := range in.Cases {
		timings := []float64{}
		payloadBytes := 0
		for repeat := 0; repeat < 3; repeat++ {
			status, data, seconds, err := invoke(test.Request, true)
			if err != nil || status != 200 {
				return fmt.Errorf("candidate HTTP query failed (case=%s status=%d)", test.Name, status)
			}
			var response struct {
				Success bool                       `json:"success"`
				Data    model.UsageAggregateResult `json:"data"`
			}
			if common.Unmarshal(data, &response) != nil || !response.Success || !response.Data.Complete {
				return fmt.Errorf("incomplete candidate response")
			}
			if len(response.Data.Items) != len(test.Expected) {
				return fmt.Errorf("candidate bucket count mismatch")
			}
			seen := map[string]bool{}
			for _, item := range response.Data.Items {
				expected, ok := test.Expected[item.Bucket]
				if !ok || seen[item.Bucket] || expected != item.UsageAggregateTotals {
					return fmt.Errorf("candidate sample mismatch (case=%s)", test.Name)
				}
				seen[item.Bucket] = true
			}
			timings = append(timings, seconds)
			payloadBytes = len(data)
			time.Sleep(100 * time.Millisecond)
		}
		results = append(results, map[string]any{"case": test.Name, "http_seconds": timings, "all_fields_equal": true, "payload_bytes": payloadBytes, "buckets": len(test.Expected)})
	}
	out, _ := common.Marshal(map[string]any{"read_only": true, "session_read_only": readonly == 1, "anonymous_status": 401, "disabled_status": 503, "handler": "controller.AggregateUsage + middleware.AdminAuth", "results": results})
	fmt.Println(string(out))
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Println("probe stopped: " + err.Error())
		os.Exit(1)
	}
}
