package controller

import (
	"io"
	"regexp"
	"strconv"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

var adminQuotaClampPattern = regexp.MustCompile(`^\s*\{\s*"quota"\s*:\s*(0|[1-9][0-9]*)\s*\}\s*$`)

func AdminClampUserQuota(c *gin.Context) {
	user, ok := adminTarget(c)
	if !ok {
		return
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 257))
	if err != nil || len(raw) > 256 {
		adminResourceError(c, 400, "invalid_request")
		return
	}
	match := adminQuotaClampPattern.FindSubmatch(raw)
	if len(match) != 2 {
		adminResourceError(c, 400, "invalid_request")
		return
	}
	ceiling, err := strconv.ParseInt(string(match[1]), 10, 64)
	if err != nil {
		adminResourceError(c, 400, "invalid_request")
		return
	}
	quota, err := model.ClampUserQuota(user.Id, ceiling, c.GetInt("role"))
	if err != nil {
		adminResourceDBError(c, err)
		return
	}
	adminResourceResult(c, gin.H{"quota": quota})
}
