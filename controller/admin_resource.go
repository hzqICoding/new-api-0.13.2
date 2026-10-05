package controller

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"io"
	"regexp"
	"strconv"
	"strings"
)

var adminOperationPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

func auditResourceID(c *gin.Context, name string) int {
	id, _ := strconv.Atoi(c.Param(name))
	return id
}
func adminResourceError(c *gin.Context, status int, code string) {
	common.SysLog(fmt.Sprintf("admin_resource actor=%d target=%d token=%d action=%s result=%s", c.GetInt("id"), auditResourceID(c, "id"), auditResourceID(c, "token_id"), c.FullPath(), code))
	c.JSON(status, gin.H{"success": false, "message": code})
}
func adminResourceResult(c *gin.Context, data any) {
	c.Header("Cache-Control", "no-store")
	common.SysLog(fmt.Sprintf("admin_resource actor=%d target=%d token=%d action=%s result=success", c.GetInt("id"), auditResourceID(c, "id"), auditResourceID(c, "token_id"), c.FullPath()))
	common.ApiSuccess(c, data)
}
func adminResourceDBError(c *gin.Context, err error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		adminResourceError(c, 404, "resource_not_found")
	} else if errors.Is(err, model.ErrAdminResourceConflict) {
		adminResourceError(c, 409, "resource_conflict")
	} else {
		diagnostic := fmt.Sprintf("error_type=%T", err)
		var cacheError *model.AdminResourceCacheError
		if errors.As(err, &cacheError) {
			diagnostic = fmt.Sprintf("storage_kind=redis error_type=%T", cacheError.Cause)
		} else {
			diagnostic = "storage_kind=database " + diagnostic
		}
		if coded, ok := err.(interface{ Code() int }); ok {
			diagnostic += fmt.Sprintf(" database_code=%d", coded.Code())
		}
		var mysqlError *mysql.MySQLError
		if errors.As(err, &mysqlError) {
			diagnostic += fmt.Sprintf(" mysql_code=%d sqlstate=%s", mysqlError.Number, string(mysqlError.SQLState[:]))
		}
		common.SysLog(fmt.Sprintf("admin_resource actor=%d action=%s %s", c.GetInt("id"), c.FullPath(), diagnostic))
		adminResourceError(c, 500, "resource_storage_failed")
	}
}
func adminFields(c *gin.Context, allowed ...string) (map[string]string, bool) {
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 4097))
	if err != nil || len(raw) > 4096 {
		adminResourceError(c, 400, "invalid_request")
		return nil, false
	}
	var fields map[string]string
	if common.Unmarshal(raw, &fields) != nil || fields == nil {
		adminResourceError(c, 400, "invalid_request")
		return nil, false
	}
	for key := range fields {
		found := false
		for _, v := range allowed {
			if key == v {
				found = true
				break
			}
		}
		if !found {
			adminResourceError(c, 400, "unknown_field")
			return nil, false
		}
	}
	return fields, true
}
func adminTarget(c *gin.Context) (*model.User, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		adminResourceError(c, 400, "invalid_target")
		return nil, false
	}
	var user model.User
	if err := model.DB.First(&user, id).Error; err != nil {
		adminResourceDBError(c, err)
		return nil, false
	}
	if user.Role >= c.GetInt("role") {
		adminResourceError(c, 403, "target_role_forbidden")
		return nil, false
	}
	if user.Status != common.UserStatusEnabled {
		adminResourceError(c, 409, "target_disabled")
		return nil, false
	}
	return &user, true
}
func AdminProvisionUser(c *gin.Context) {
	fields, ok := adminFields(c, "username", "password")
	if !ok {
		return
	}
	op := c.Param("operation")
	username := strings.TrimSpace(fields["username"])
	password := fields["password"]
	if !adminOperationPattern.MatchString(op) || len(username) == 0 || len(username) > 20 || len(password) < 8 || len(password) > 20 {
		adminResourceError(c, 400, "invalid_request")
		return
	}
	fingerprint := fmt.Sprintf("%x", sha256.Sum256([]byte(username)))
	id, err := model.AdminCreateUser(op, c.GetInt("id"), fingerprint, username, password, c.GetInt("role"))
	if err != nil {
		adminResourceDBError(c, err)
		return
	}
	common.SysLog(fmt.Sprintf("admin_resource actor=%d target=%d operation=%s action=create_user result=success", c.GetInt("id"), id, op))
	adminResourceResult(c, gin.H{"id": id})
}
func AdminAddUserToken(c *gin.Context) {
	user, ok := adminTarget(c)
	if !ok {
		return
	}
	fields, ok := adminFields(c, "operation_id", "name", "group")
	if !ok {
		return
	}
	op := fields["operation_id"]
	name := strings.TrimSpace(fields["name"])
	group := strings.TrimSpace(fields["group"])
	if !adminOperationPattern.MatchString(op) || len(name) == 0 || len(name) > 50 || len(group) == 0 || len(group) > 64 {
		adminResourceError(c, 400, "invalid_request")
		return
	}
	if _, ok := service.GetUserUsableGroups(user.Group)[group]; !ok || !ratio_setting.ContainsGroupRatio(group) {
		adminResourceError(c, 400, "invalid_group")
		return
	}
	fingerprint := fmt.Sprintf("%x", sha256.Sum256([]byte(name+"\x00"+group)))
	token, err := model.AdminCreateToken(op, c.GetInt("id"), c.GetInt("role"), user.Id, fingerprint, name, group)
	if err != nil {
		adminResourceDBError(c, err)
		return
	}
	common.SysLog(fmt.Sprintf("admin_resource actor=%d target=%d token=%d operation=%s action=create_token result=success", c.GetInt("id"), user.Id, token.Id, op))
	adminResourceResult(c, gin.H{"id": token.Id, "key": "sk-" + token.Key, "name": token.Name, "group": token.Group, "created_time": token.CreatedTime})
}
func AdminListUserTokens(c *gin.Context) {
	user, ok := adminTarget(c)
	if !ok {
		return
	}
	p, err := strconv.Atoi(c.DefaultQuery("p", "1"))
	size, e := strconv.Atoi(c.DefaultQuery("page_size", "100"))
	if err != nil || e != nil || p < 1 || p > 1000000 || size < 1 || size > 100 {
		adminResourceError(c, 400, "invalid_pagination")
		return
	}
	items := make([]gin.H, 0)
	query := model.DB.Model(&model.Token{}).Where("user_id = ?", user.Id)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		adminResourceDBError(c, err)
		return
	}
	var tokens []model.Token
	if err := query.Select("id", "name", "group", "created_time", "status", "key").Order("id ASC").Offset((p - 1) * size).Limit(size).Find(&tokens).Error; err != nil {
		adminResourceDBError(c, err)
		return
	}
	for _, token := range tokens {
		items = append(items, gin.H{"id": token.Id, "name": token.Name, "group": token.Group, "created_time": token.CreatedTime, "status": token.Status, "key_masked": "sk-" + model.MaskTokenKey(strings.TrimPrefix(token.Key, "sk-"))})
	}
	adminResourceResult(c, gin.H{"items": items, "total": total})
}

func AdminGetUserTokenKey(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	user, ok := adminTarget(c)
	if !ok {
		return
	}
	id, err := strconv.Atoi(c.Param("token_id"))
	if err != nil || id <= 0 {
		adminResourceError(c, 400, "invalid_token")
		return
	}
	if _, ok := adminFields(c); !ok {
		return
	}
	token, err := model.AdminReadTokenAs(user.Id, id, c.GetInt("role"))
	if err != nil {
		adminResourceDBError(c, err)
		return
	}
	adminResourceResult(c, gin.H{"key": "sk-" + strings.TrimPrefix(token.Key, "sk-")})
}
func AdminDeleteUserToken(c *gin.Context) {
	user, ok := adminTarget(c)
	if !ok {
		return
	}
	id, err := strconv.Atoi(c.Param("token_id"))
	if err != nil || id <= 0 {
		adminResourceError(c, 400, "invalid_token")
		return
	}
	if err := model.AdminRevokeTokenAs(user.Id, id, c.GetInt("role")); err != nil {
		adminResourceDBError(c, err)
		return
	}
	adminResourceResult(c, nil)
}
func AdminCleanupResource(c *gin.Context) {
	op := c.Param("operation")
	if !adminOperationPattern.MatchString(op) {
		adminResourceError(c, 400, "invalid_operation")
		return
	}
	if err := model.AdminCleanupOperation(op, c.GetInt("id"), c.GetInt("role")); err != nil {
		adminResourceDBError(c, err)
		return
	}
	var record model.AdminResourceOperation
	if err := model.DB.First(&record, "id = ?", op).Error; err == nil {
		common.SysLog(fmt.Sprintf("admin_resource actor=%d target=%d token=%d operation=%s action=cleanup result=success", c.GetInt("id"), record.TargetID, record.TokenID, op))
	}
	adminResourceResult(c, nil)
}
