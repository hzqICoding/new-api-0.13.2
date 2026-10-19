package controller

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

const usageAggregateMaxBodyBytes = 128 << 10

// Admission is process-wide and non-blocking. Disabled by default; reading the
// environment per request permits tests to opt in without mutable global flags.
var usageAggregateSlots = make(chan struct{}, 2)

func AggregateUsage(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("CARPOOL_USAGE_AGGREGATE_ENABLED")), "true") {
		usageAggregateError(c, http.StatusServiceUnavailable, "usage_aggregate_disabled")
		return
	}
	reader := http.MaxBytesReader(c.Writer, c.Request.Body, usageAggregateMaxBodyBytes)
	defer reader.Close()
	body, err := io.ReadAll(reader)
	if err != nil {
		var oversized *http.MaxBytesError
		if errors.As(err, &oversized) {
			usageAggregateError(c, http.StatusRequestEntityTooLarge, "usage_aggregate_request_too_large")
		} else {
			usageAggregateError(c, http.StatusBadRequest, "invalid_usage_aggregate_request")
		}
		return
	}
	var request model.UsageAggregateRequest
	if !utf8.Valid(body) || common.Unmarshal(body, &request) != nil || request.Validate() != nil {
		usageAggregateError(c, http.StatusBadRequest, "invalid_usage_aggregate_request")
		return
	}
	select {
	case usageAggregateSlots <- struct{}{}:
		defer func() { <-usageAggregateSlots }()
	default:
		usageAggregateError(c, http.StatusServiceUnavailable, "usage_aggregate_busy")
		return
	}
	result, err := model.ReadUsageAggregate(c.Request.Context(), request)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			usageAggregateError(c, http.StatusGatewayTimeout, "usage_aggregate_timeout")
		case errors.Is(err, context.Canceled):
			usageAggregateError(c, http.StatusServiceUnavailable, "usage_aggregate_cancelled")
		case errors.Is(err, model.ErrUsageAggregateLimit):
			usageAggregateError(c, http.StatusUnprocessableEntity, "usage_aggregate_limit_exceeded")
		case errors.Is(err, model.ErrUsageAggregateData):
			usageAggregateError(c, http.StatusUnprocessableEntity, "usage_aggregate_invalid_data")
		default:
			usageAggregateError(c, http.StatusInternalServerError, "usage_aggregate_unavailable")
		}
		return
	}
	common.ApiSuccess(c, result)
}

func usageAggregateError(c *gin.Context, status int, message string) {
	// Never expose driver errors, SQL text, environment, or raw log metadata.
	c.JSON(status, gin.H{"success": false, "message": message})
}
