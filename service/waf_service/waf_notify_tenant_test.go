package waf_service

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"SamWaf/common/wafctx"
	"SamWaf/customtype"
	"SamWaf/global"
	"SamWaf/innerbean"
	"SamWaf/model"
	"SamWaf/model/baseorm"
	"SamWaf/wafdb"
	"SamWaf/wafdb/dialect"

	sqlite "github.com/samwafgo/sqlitedriver"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// openNotifyTestDB 独立临时库 + 生产同款租户回调；全局租户指向 tenantZ，
// 租户订阅只有显式带租户上下文才读得到。
func openNotifyTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	// 订阅查询用 dialect.Q 处理列名，未注册直接 panic；先落 SQLite
	priorDialect := func() (d dialect.DBDialect) {
		defer func() { _ = recover() }()
		return dialect.Get()
	}()
	dialect.Register(&dialect.SQLiteDialect{})

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "notify_test.db")),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	wafdb.RegisterTenantScopeCallbacks(db)
	if err := db.AutoMigrate(&model.NotifySubscription{}); err != nil {
		t.Fatalf("建表失败: %v", err)
	}

	oldDB, oldTenant, oldUser := global.GWAF_LOCAL_DB, global.GWAF_TENANT_ID, global.GWAF_USER_CODE
	t.Cleanup(func() {
		global.GWAF_LOCAL_DB = oldDB
		global.GWAF_TENANT_ID, global.GWAF_USER_CODE = oldTenant, oldUser
		dialect.Register(priorDialect)
		if sqlDB, e := db.DB(); e == nil {
			_ = sqlDB.Close()
		}
	})
	global.GWAF_LOCAL_DB = db
	global.GWAF_TENANT_ID, global.GWAF_USER_CODE = "tenantZ", "userZ"
	return db
}

// seedSubscription 按指定归属建一条订阅；传 ctx 决定行归属租户。
func seedSubscription(t *testing.T, db *gorm.DB, ctx context.Context, id, messageType string, status int) {
	t.Helper()
	bean := &model.NotifySubscription{
		BaseOrm:     baseorm.BaseOrm{Id: id, CREATE_TIME: customtype.JsonTime(time.Now()), UPDATE_TIME: customtype.JsonTime(time.Now())},
		ChannelId:   "ch-" + id,
		MessageType: messageType,
		Status:      status,
	}
	if err := db.WithContext(ctx).Create(bean).Error; err != nil {
		t.Fatalf("插入订阅 %s 失败: %v", id, err)
	}
}

// TestSubscriptionsForEventScope 事件订阅取数口径：
// 租户事件拿「本租户桶 + 平台桶」，平台/无归属事件只拿平台桶（失败关闭），
// 且其它租户的订阅必须不可见。
//
// 行归属按生产口径：tenant_id 区分租户，user_code 恒为实例码（登录令牌写的就是全局值）。
func TestSubscriptionsForEventScope(t *testing.T) {
	db := openNotifyTestDB(t)
	tenantCtx := func(tenantID string) context.Context {
		return wafctx.WithTenant(context.Background(), tenantID, global.GWAF_USER_CODE)
	}
	seedSubscription(t, db, context.Background(), "sub-platform", "access", 1)
	seedSubscription(t, db, tenantCtx("t-a"), "sub-ta", "access", 1)
	seedSubscription(t, db, tenantCtx("t-a"), "sub-ta-off", "access", 0)
	seedSubscription(t, db, tenantCtx("t-b"), "sub-tb", "access", 1)
	seedSubscription(t, db, tenantCtx("t-a"), "sub-ta-other", "login", 1)

	ids := func(subs []model.NotifySubscription) map[string]bool {
		m := map[string]bool{}
		for _, s := range subs {
			m[s.Id] = true
		}
		return m
	}

	got := ids(WafNotifySubscriptionServiceApp.GetSubscriptionsForEvent("t-a", "access"))
	if len(got) != 2 || !got["sub-platform"] || !got["sub-ta"] {
		t.Fatalf("租户事件应命中「本租户启用订阅+平台订阅」共 2 条，实际 %v", got)
	}

	for _, tenantID := range []string{"", "tenantZ"} {
		got = ids(WafNotifySubscriptionServiceApp.GetSubscriptionsForEvent(tenantID, "access"))
		if len(got) != 1 || !got["sub-platform"] {
			t.Fatalf("平台/无归属事件(%q)应只命中平台桶，实际 %v", tenantID, got)
		}
	}

	// 无租户行的租户（尚未配置订阅）仍应收到平台侧监管订阅
	got = ids(WafNotifySubscriptionServiceApp.GetSubscriptionsForEvent("t-none", "access"))
	if len(got) != 1 || !got["sub-platform"] {
		t.Fatalf("未配置订阅的租户应只命中平台桶，实际 %v", got)
	}
}

// TestMessageInfoTenantCarrier 所有消息结构内嵌 BaseMessageInfo 后提升出 Tenant()，
// 发送侧据此取归属；该字段不参与 JSON，前端报文协议保持不变。
func TestMessageInfoTenantCarrier(t *testing.T) {
	mi := innerbean.AccessMessageInfo{
		BaseMessageInfo: innerbean.BaseMessageInfo{TenantId: "t-a"},
		Event:           "auth_ok",
	}
	carrier, ok := interface{}(mi).(interface{ Tenant() string })
	if !ok {
		t.Fatal("AccessMessageInfo 应通过内嵌 BaseMessageInfo 实现 Tenant()")
	}
	if tid := carrier.Tenant(); tid != "t-a" {
		t.Fatalf("Tenant() 应返回 t-a，实际 %q", tid)
	}

	raw, err := json.Marshal(mi)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	if strings.Contains(string(raw), "t-a") || strings.Contains(string(raw), "tenant") {
		t.Fatalf("租户标记不应出现在通知报文中，实际 %s", raw)
	}
}
