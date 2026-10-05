package model

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"log"
	"strings"
	"testing"
	"time"
)

func TestAdminSensitiveWritesNeverLogSQLSecrets(t *testing.T) {
	var logs bytes.Buffer
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.New(log.New(&logs, "", 0), logger.Config{LogLevel: logger.Info, SlowThreshold: time.Second})})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	old := DB
	DB = db
	defer func() { DB = old }()
	if err := db.AutoMigrate(&User{}, &Token{}, &AdminResourceOperation{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&User{Id: 4, Username: "target", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AffCode: "target", Group: "default"}).Error; err != nil {
		t.Fatal(err)
	}
	logs.Reset()
	key := ""
	if err := db.Callback().Create().Before("gorm:create").Register("test:secret_error", func(tx *gorm.DB) {
		switch row := tx.Statement.Dest.(type) {
		case *Token:
			key = row.Key
			tx.AddError(fmt.Errorf("driver echoed secret %s", row.Key))
		case *User:
			tx.AddError(errors.New("driver echoed CANARY_PASSWORD"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := AdminCreateToken(strings.Repeat("a", 32), 1, common.RoleAdminUser, 4, "fingerprint", "token", "default"); err == nil {
		t.Fatal("expected insertion failure")
	}
	if key == "" {
		t.Fatal("test did not reach secret token insertion")
	}
	if _, err := AdminCreateUser(strings.Repeat("b", 32), 1, "fingerprint", "new-user", "CANARY_PASSWORD"); err == nil {
		t.Fatal("expected user insertion failure")
	}
	if logs.Len() != 0 {
		t.Fatalf("sensitive transaction invoked SQL logger: %s", logs.String())
	}
}
