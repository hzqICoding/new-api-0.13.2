package controller

import (
	"encoding/json"
	"io"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func AdminResetUserQuota(c *gin.Context) {
	user, ok := adminTarget(c)
	if !ok {
		return
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 2049))
	var fields map[string]json.RawMessage
	if err != nil || len(raw) > 2048 || common.Unmarshal(raw, &fields) != nil || len(fields) != 5 {
		adminResourceError(c, 400, "invalid_request")
		return
	}
	in := model.AdminQuotaResetInput{ActorID: c.GetInt("id"), ActorRole: c.GetInt("role"), TargetID: user.Id}
	for field, out := range map[string]any{"operation_id": &in.OperationID, "delta_quota": &in.DeltaQuota, "ceiling_quota": &in.CeilingQuota, "expected_group": &in.ExpectedGroup, "expires_at": &in.ExpiresAt} {
		value, present := fields[field]
		if !present || strings.TrimSpace(string(value)) == "null" || common.Unmarshal(value, out) != nil {
			adminResourceError(c, 400, "invalid_request")
			return
		}
	}
	if !adminOperationPattern.MatchString(in.OperationID) || in.CeilingQuota < 0 || len(in.ExpectedGroup) == 0 || len(in.ExpectedGroup) > 64 || in.ExpiresAt <= 0 {
		adminResourceError(c, 400, "invalid_request")
		return
	}
	result, err := model.ApplyAdminQuotaReset(in)
	if err != nil {
		adminResourceDBError(c, err)
		return
	}
	adminResourceResult(c, result)
}

func AdminCancelUserQuotaReset(c *gin.Context) {
	// Cancellation must still fence a late request after a user was disabled.
	id, err := strconv.Atoi(c.Param("id"))
	op := c.Param("operation")
	if err != nil || id <= 0 || !adminOperationPattern.MatchString(op) {
		adminResourceError(c, 400, "invalid_request")
		return
	}
	result, err := model.CancelAdminQuotaReset(op, c.GetInt("id"), c.GetInt("role"), id)
	if err != nil {
		adminResourceDBError(c, err)
		return
	}
	adminResourceResult(c, result)
}

func AdminUserUsageWatermark(c *gin.Context) {
	user, ok := adminTarget(c)
	if !ok {
		return
	}
	id, err := model.UserUsageWatermark(user.Id)
	if err != nil {
		adminResourceDBError(c, err)
		return
	}
	adminResourceResult(c, gin.H{"log_id": id, "quota": user.Quota})
}
