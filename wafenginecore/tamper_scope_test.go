package wafenginecore

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"SamWaf/common/wafctx"
	"SamWaf/customtype"
	"SamWaf/global"
	"SamWaf/model"
	"SamWaf/model/baseorm"
	"SamWaf/model/spec"
	"SamWaf/wafdb"

	sqlitedriver "github.com/samwafgo/sqlitedriver"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// openTamperTestDB 独立临时 core 库 + 生产同款租户回调。
// 全局租户指向测试数据不存在的 tenantZ / userZ：租户 t-a 的行只有显式带上下文才够得着，
// 因此「回退全局」的裸库调用会稳定读到空集，正好用来锚定回归。
func openTamperTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlitedriver.Open(filepath.Join(t.TempDir(), "tamper_test.db")),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	wafdb.RegisterTenantScopeCallbacks(db)
	if err := db.AutoMigrate(&model.TamperRule{}); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	t.Cleanup(func() {
		if s, e := db.DB(); e == nil {
			_ = s.Close()
		}
	})
	return db
}

// seedTamperRule 在租户 t-a / userA 名下建一条未学习的规则
func seedTamperRule(t *testing.T, db *gorm.DB, id, hostCode string) *model.TamperRule {
	t.Helper()
	rule := &model.TamperRule{
		BaseOrm: baseorm.BaseOrm{
			Id: id, USER_CODE: "userA", Tenant_ID: "t-a",
			CREATE_TIME: customtype.JsonTime(time.Now()), UPDATE_TIME: customtype.JsonTime(time.Now()),
		},
		HostCode:       hostCode,
		Url:            "/index.html",
		IsEnable:       1,
		BaselineStatus: 0,
	}
	if err := db.WithContext(wafctx.WithTenant(context.Background(), "t-a", "userA")).Create(rule).Error; err != nil {
		t.Fatalf("插入防篡改规则失败: %v", err)
	}
	return rule
}

// TestNotifyTamperReloadTenantHost 热重载必须读到租户站点的规则。
// 不带 ctx 时查询回调回退全局租户常量，租户站点会推上来一张空表，
// 引擎侧 TamperRules 被清空 → 该站点的防篡改保护静默失效。
func TestNotifyTamperReloadTenantHost(t *testing.T) {
	oldDB, oldTenant, oldUser := global.GWAF_LOCAL_DB, global.GWAF_TENANT_ID, global.GWAF_USER_CODE
	oldChan := global.GWAF_CHAN_MSG
	defer func() {
		global.GWAF_LOCAL_DB, global.GWAF_TENANT_ID, global.GWAF_USER_CODE = oldDB, oldTenant, oldUser
		global.GWAF_CHAN_MSG = oldChan
	}()
	db := openTamperTestDB(t)
	global.GWAF_LOCAL_DB = db
	global.GWAF_TENANT_ID, global.GWAF_USER_CODE = "tenantZ", "userZ"
	chanMsg := make(chan spec.ChanCommonHost, 4)
	global.GWAF_CHAN_MSG = chanMsg

	seedTamperRule(t, db, "rule-reload-1", "h-tenant")
	seedTamperRule(t, db, "rule-reload-2", "h-tenant")

	waf := &WafEngine{}
	waf.notifyTamperReload("h-tenant")

	select {
	case msg := <-chanMsg:
		rules, ok := msg.Content.([]model.TamperRule)
		if !ok {
			t.Fatalf("热重载内容类型应为 []model.TamperRule，实际 %T", msg.Content)
		}
		if len(rules) != 2 {
			t.Fatalf("租户站点应读到 2 条规则，实际 %d 条（0 条即全局回退导致的保护静默失效）", len(rules))
		}
		if msg.HostCode != "h-tenant" {
			t.Fatalf("热重载站点码应原样传递，实际 %s", msg.HostCode)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("热重载消息未投递")
	}
}

// TestCaptureTamperBaselineTenantRow 被动学习写回要落在规则自身租户的行上，
// 且带租户上下文后仍按 id 命中（不能因过滤条件写空）。
func TestCaptureTamperBaselineTenantRow(t *testing.T) {
	oldDB, oldTenant, oldUser := global.GWAF_LOCAL_DB, global.GWAF_TENANT_ID, global.GWAF_USER_CODE
	defer func() {
		global.GWAF_LOCAL_DB, global.GWAF_TENANT_ID, global.GWAF_USER_CODE = oldDB, oldTenant, oldUser
	}()
	db := openTamperTestDB(t)
	global.GWAF_LOCAL_DB = db
	global.GWAF_TENANT_ID, global.GWAF_USER_CODE = "tenantZ", "userZ"
	// 学习完成后会触发热重载，给通道留缓冲避免 goroutine 卡住
	global.GWAF_CHAN_MSG = make(chan spec.ChanCommonHost, 8)

	rule := seedTamperRule(t, db, "rule-capture-1", "h-capture")
	content := []byte("<html>baseline</html>")

	waf := &WafEngine{}
	waf.captureTamperBaseline(rule, content, "text/html", 200, model.TamperConfig{IsEnable: 1, MaxSizeKB: 1024})

	var learned model.TamperRule
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		// 直查用平台作用域：测试要看到行真实归属，不受回调回退影响
		if err := db.WithContext(wafctx.PlatformScope(context.Background())).
			First(&learned, "id = ?", rule.Id).Error; err == nil && learned.BaselineStatus == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if learned.BaselineStatus != 1 {
		t.Fatalf("基线应学习成功，实际 status=%d msg=%s", learned.BaselineStatus, learned.BaselineMsg)
	}
	if learned.BaselineHash != sha256Hex(content) {
		t.Fatalf("基线哈希不匹配，实际 %s", learned.BaselineHash)
	}
	if learned.Tenant_ID != "t-a" || learned.USER_CODE != "userA" {
		t.Fatalf("学习写回不应改动行归属，实际 tenant=%s user=%s", learned.Tenant_ID, learned.USER_CODE)
	}
}
