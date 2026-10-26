package api

import (
	"context"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"SamWaf/common/wafctx"
	"SamWaf/customtype"
	"SamWaf/global"
	"SamWaf/model"
	"SamWaf/model/baseorm"
	"SamWaf/wafdb"

	sqlite "github.com/samwafgo/sqlitedriver"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// openImportTestDB 独立临时库 + 生产同款租户回调，替换全局 core 库。
// 全局租户指向测试数据里不存在的 tenantZ：命中只能来自请求上下文。
func openImportTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "import_test.db")),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	wafdb.RegisterTenantScopeCallbacks(db)
	if err := db.AutoMigrate(&model.Tenant{}, &model.Hosts{}); err != nil {
		t.Fatalf("建表失败: %v", err)
	}

	oldDB, oldTenant, oldUser := global.GWAF_LOCAL_DB, global.GWAF_TENANT_ID, global.GWAF_USER_CODE
	t.Cleanup(func() {
		global.GWAF_LOCAL_DB = oldDB
		global.GWAF_TENANT_ID, global.GWAF_USER_CODE = oldTenant, oldUser
		if sqlDB, e := db.DB(); e == nil {
			_ = sqlDB.Close()
		}
	})
	global.GWAF_LOCAL_DB = db
	global.GWAF_TENANT_ID, global.GWAF_USER_CODE = "tenantZ", "userZ"
	return db
}

func seedImportTenant(t *testing.T, db *gorm.DB, tenantID string, maxHosts int) {
	t.Helper()
	err := db.Create(&model.Tenant{
		BaseOrm:    baseorm.BaseOrm{Id: tenantID + "-row", CREATE_TIME: customtype.JsonTime(time.Now()), UPDATE_TIME: customtype.JsonTime(time.Now())},
		TenantID:   tenantID,
		TenantName: tenantID,
		MaxHosts:   maxHosts,
	}).Error
	if err != nil {
		t.Fatalf("建租户行失败: %v", err)
	}
}

// buildHostImportRows 按导出格式（首列 BaseOrm 为 " - "，其余列取 json 标签）构造
// n 行站点导入数据，host 逐行不同避免同租户内 host+port 重复。
func buildHostImportRows(n int) [][]string {
	typ := reflect.TypeOf(model.Hosts{})
	header := []string{" - "}
	kinds := map[string]reflect.Kind{}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.Name == "BaseOrm" {
			continue
		}
		tag := field.Tag.Get("json")
		header = append(header, tag)
		kinds[tag] = field.Type.Kind()
	}

	rows := [][]string{header}
	for i := 0; i < n; i++ {
		line := []string{" - "}
		for _, tag := range header[1:] {
			switch tag {
			case "host":
				line = append(line, "import-"+strconv.Itoa(i)+".example.com")
			case "port":
				line = append(line, "8080")
			default:
				switch kinds[tag] {
				case reflect.Int, reflect.Int64:
					line = append(line, "0")
				default:
					line = append(line, "")
				}
			}
		}
		rows = append(rows, line)
	}
	return rows
}

func countImportHosts(t *testing.T, db *gorm.DB, tenantID, userCode string) int64 {
	t.Helper()
	var cnt int64
	err := db.WithContext(wafctx.PlatformScope(context.Background())).Model(&model.Hosts{}).
		Where("tenant_id = ? and user_code = ?", tenantID, userCode).Count(&cnt).Error
	if err != nil {
		t.Fatalf("统计 host 失败: %v", err)
	}
	return cnt
}

// TestImportExcelHostQuota 导入按行受 tenants.max_hosts 约束：批量导入不得绕过配额。
func TestImportExcelHostQuota(t *testing.T) {
	db := openImportTestDB(t)
	seedImportTenant(t, db, "t-imp", 2)
	ctxImp := wafctx.WithTenant(context.Background(), "t-imp", "userImp")

	ret := saveDataToDatabase(ctxImp, "hosts", buildHostImportRows(4), "0")
	if ret.SuccessInt != 2 || ret.FailInt != 2 {
		t.Fatalf("max_hosts=2 导入 4 行应成 2 败 2，实际 成%d 败%d msg=%s", ret.SuccessInt, ret.FailInt, ret.Msg)
	}
	if !strings.Contains(ret.Msg, "配额上限") {
		t.Fatalf("失败原因应为配额上限，实际 msg=%s", ret.Msg)
	}
	if cnt := countImportHosts(t, db, "t-imp", "userImp"); cnt != 2 {
		t.Fatalf("落库行数应为 2，实际 %d", cnt)
	}
	// 导入行归属发起者租户
	if cnt := countImportHosts(t, db, "tenantZ", "userZ"); cnt != 0 {
		t.Fatalf("导入行不应落到平台租户，实际 %d", cnt)
	}
}

// TestImportExcelHostQuotaUnlimited max_hosts=0（不限）时全量落库——上一用例的
// 失败必须来自配额判定本身，而不是导入链路跑不通（阳性对照）。
func TestImportExcelHostQuotaUnlimited(t *testing.T) {
	db := openImportTestDB(t)
	seedImportTenant(t, db, "t-free", 0)
	ctxFree := wafctx.WithTenant(context.Background(), "t-free", "userFree")

	ret := saveDataToDatabase(ctxFree, "hosts", buildHostImportRows(4), "0")
	if ret.SuccessInt != 4 || ret.FailInt != 0 {
		t.Fatalf("不限配额应 4 行全成，实际 成%d 败%d msg=%s", ret.SuccessInt, ret.FailInt, ret.Msg)
	}
	if cnt := countImportHosts(t, db, "t-free", "userFree"); cnt != 4 {
		t.Fatalf("落库行数应为 4，实际 %d", cnt)
	}
}

// TestImportExcelHostQuotaPlatform 平台自身（租户码无 tenants 行）导入不受配额约束。
func TestImportExcelHostQuotaPlatform(t *testing.T) {
	db := openImportTestDB(t)
	seedImportTenant(t, db, "t-imp", 1)
	ctxPlat := wafctx.WithTenant(context.Background(), "tenantZ", "userZ")

	ret := saveDataToDatabase(ctxPlat, "hosts", buildHostImportRows(3), "0")
	if ret.SuccessInt != 3 || ret.FailInt != 0 {
		t.Fatalf("平台导入不应受限，实际 成%d 败%d msg=%s", ret.SuccessInt, ret.FailInt, ret.Msg)
	}
}
