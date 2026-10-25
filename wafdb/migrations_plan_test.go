package wafdb

import (
	"path/filepath"
	"testing"
	"time"

	"SamWaf/common/uuid"
	"SamWaf/customtype"
	"SamWaf/model"
	"SamWaf/model/baseorm"

	sqlite "github.com/samwafgo/sqlitedriver"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestCoreMigrationCreatesPlans 迁移 202610260002：全新库上跑完整 core 迁移链，
// plans 表存在、plan_code 唯一索引生效（同码第二行必须失败）。
// 生产初始化路径正是这条链（localdb.go 在迁移后才注册租户回调），这里直接验证之。
func TestCoreMigrationCreatesPlans(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "migrate_plan.db")),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, e := db.DB(); e == nil {
			_ = sqlDB.Close() // Windows 下不关连接会让 TempDir 清理失败
		}
	})
	if err := RunCoreDBMigrations(db); err != nil {
		t.Fatalf("core 迁移失败: %v", err)
	}
	if !db.Migrator().HasTable(&model.Plan{}) {
		t.Fatal("迁移后 plans 表应存在")
	}
	if !db.Migrator().HasIndex(&model.Plan{}, "uni_plan_code") {
		t.Fatal("plans.plan_code 唯一索引应存在")
	}

	now := customtype.JsonTime(time.Now())
	first := &model.Plan{
		BaseOrm: baseorm.BaseOrm{Id: uuid.GenUUID(), CREATE_TIME: now, UPDATE_TIME: now},
		PlanCode: "mig-test", PlanName: "迁移验证", Status: 1,
		Permissions: "dashboard:view",
	}
	if err := db.Create(first).Error; err != nil {
		t.Fatalf("写入首个套餐失败: %v", err)
	}
	dup := &model.Plan{
		BaseOrm: baseorm.BaseOrm{Id: uuid.GenUUID(), CREATE_TIME: now, UPDATE_TIME: now},
		PlanCode: "mig-test", PlanName: "重复码", Status: 1,
		Permissions: "dashboard:view",
	}
	if err := db.Create(dup).Error; err == nil {
		t.Fatal("plan_code 唯一索引失效：同码第二行写入成功")
	}
}
