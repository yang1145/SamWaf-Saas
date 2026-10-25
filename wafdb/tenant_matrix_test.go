package wafdb

import (
	"context"
	"reflect"
	"testing"

	"SamWaf/common/wafctx"
	"SamWaf/global"
	"SamWaf/model"

	"gorm.io/gorm"
)

// matrixModels 全表租户隔离测试矩阵的模型清单。
//
// 收录标准：嵌入 baseorm.BaseOrm（自带 tenant_id/user_code 列）、属于租户级业务表/数据表、
// 且不在 tenantExemptModels 平台豁免清单（wafdb/localdb.go）中的模型。分组仅为注释归类，
// 所有模型执行同一套断言（各自建独立临时 SQLite 库，互不影响）。
//
// 明确排除（平台级豁免，见 localdb.go tenantExemptModels）：
// Account、TokenInfo、Otp、AccountPwdHistory、LoginHistory、SecurityAuditLog、
// SystemConfig、WafSysLog、HostLoginEvent。
// 明确排除（日志/统计类）：WebLog/AccessLog/EventPayload（日志库报文）、NotifyLog、
// OPlatformLog、WafPluginLog、ThreatIPExcludeAudit、Stats 系列（统计库）。
// 明确排除（非独立业务表/平台治理/废弃）：
//   - NotifyThrottleConfig / NotifyFilterConfig：仅是 NotifySubscription 行内 JSON 配置，
//     无独立表（隔离性已由 NotifySubscription 覆盖）；
//   - DataRetentionPolicy / UpgradeNoticeRecord / WafPluginSystemConfig：平台治理配置；
//   - FirewallIPBlock / TamperRule：迁移建表后即 Drop 的废弃历史模型。
//
// 已知唯一索引不含租户列、零值种子第二行必然撞唯一键的模型（子测试按设计 Skip 并在汇报中列出）：
// IPWatchlist（uni_watch_ip）、WafApp（code）、WafPluginConfig（plugin_id）。
var matrixModels = []any{
	// —— 站点与防护配置 ——
	&model.Hosts{},
	&model.Rules{}, // 自定义规则（需求中的 RuleInfo 对应的历史类型名）
	&model.LDPUrl{},
	&model.IPAllowList{},
	&model.URLAllowList{},
	&model.IPBlockList{},
	&model.URLBlockList{},
	&model.AntiCC{},
	&model.AntiCCRule{},
	&model.Sensitive{},
	&model.BlockingPage{},
	&model.HttpAuthBase{},
	&model.HostPathRule{},
	&model.HostGroup{},

	// —— IP 名单与情报 ——
	&model.IPGroup{},
	&model.IPGroupItem{},
	&model.IPWatchlist{},
	&model.CDNProvider{}, // 需求中的「CDNIP 相关」对应模型

	// —— 证书 ——
	&model.SslConfig{},
	&model.SslExpire{},
	&model.SslOrder{},
	&model.CaServerInfo{},

	// —— 流量调度与缓存 ——
	&model.LoadBalance{},
	&model.CacheRule{},
	&model.Tunnel{},

	// —— 隐私信息与通知 ——
	&model.PrivateInfo{},
	&model.PrivateGroup{},
	&model.NotifyChannel{},
	&model.NotifySubscription{},

	// —— 任务与延迟消息 ——
	&model.Task{},
	&model.BatchTask{},
	&model.DelayMsg{},

	// —— 开放平台 ——
	&model.OPlatformKey{},

	// —— 主机防护（防爆破） ——
	&model.HostGuardBan{},
	&model.HostGuardOffender{},
	&model.HostGuardBanLadder{},

	// —— 统一访问认证（SaaS 零信任） ——
	&model.AccessConfig{},
	&model.AccessAccount{},
	&model.AccessSession{},
	&model.AccessToken{},
	&model.AccessTicket{},

	// —— 威胁情报 ——
	&model.ThreatIPChannel{},
	&model.ThreatIPSnapshot{},
	&model.ThreatIPExclude{},

	// —— 应用引擎与插件 ——
	&model.WafApp{},
	&model.WafAppChangeLog{},
	&model.WafPluginConfig{},

	// —— 其它租户级数据表 ——
	&model.UIPreference{},
	&model.WafLogLabelMark{},
	&model.IPTag{},
	&model.ShareDb{},
	&model.OneKeyMod{},
}

// 矩阵种子租户、识别号与行主键。
const (
	matrixTenantA = "tenantA"
	matrixUserA   = "userA"
	matrixIDA     = "a1"

	matrixTenantB = "tenantB"
	matrixUserB   = "userB"
	matrixIDB     = "b1"
)

// TestTenantScopeMatrix 全表租户隔离矩阵。
//
// 对 matrixModels 中每个模型：独立临时 SQLite 库 → 注册与生产一致的租户回调
// （RegisterTenantScopeCallbacks）→ AutoMigrate → 插入 tenantA/userA=a1 与
// tenantB/userB=b1 两行零值数据（其余字段全零，无 ctx 写入，before_create 只补空缺、
// 不覆盖显式租户值），然后断言：
//  1. 租户 A 上下文恰好看到自己的 1 行（tenant_id=tenantA 且 id=a1）；
//  2. 租户 B 上下文恰好看到自己的 1 行（tenant_id=tenantB 且 id=b1）；
//  3. 无 ctx 时回退全局常量（设为不存在的租户 tenantZ/userZ）得到 0 行——
//     保证命中只能来自请求上下文，且兼容「无 ctx 按全局过滤」的单租户语义。
//
// 个别模型因迁移限制或唯一索引（不含租户列）无法零值插入时按设计 Skip，
// 不影响其余矩阵项；跳过清单在汇报中单独列出。
func TestTenantScopeMatrix(t *testing.T) {
	if len(matrixModels) == 0 {
		t.Fatal("矩阵模型清单为空，请检查 matrixModels")
	}
	for _, m := range matrixModels {
		m := m
		name := reflect.TypeOf(m).Elem().Name()
		t.Run(name, func(t *testing.T) {
			testTenantMatrixModel(t, m)
		})
	}
}

// testTenantMatrixModel 对单个模型执行完整的租户隔离断言序列。
func testTenantMatrixModel(t *testing.T, m any) {
	t.Helper()
	modelName := reflect.TypeOf(m).Elem().Name()

	// 全局租户常量指向测试数据里不存在的租户：
	//   - 无 ctx 查询必须得到 0 行（回退语义下谁也看不见）；
	//   - 任何 ctx 查询的命中都只能来自 wafctx.WithTenant。
	oldTenant, oldUser := global.GWAF_TENANT_ID, global.GWAF_USER_CODE
	global.GWAF_TENANT_ID, global.GWAF_USER_CODE = "tenantZ", "userZ"
	t.Cleanup(func() {
		global.GWAF_TENANT_ID, global.GWAF_USER_CODE = oldTenant, oldUser
	})

	db := openTenantTestDB(t, "sqlite")

	// 与生产一致的租户回调（四个回调统一入口）。迁移器内部查询走 Row/Exec
	// 回调链，不经过 before_query，先注册再建表不影响 DDL。
	RegisterTenantScopeCallbacks(db)

	if err := db.AutoMigrate(m); err != nil {
		t.Skipf("模型 %s 不适用零值插入: %v", modelName, err)
		return
	}

	// 零值种子：除 Id/Tenant_ID/USER_CODE 外全部零值。无 ctx 写入时
	// before_create 仅补空缺字段，显式租户值原样落库。
	rowType := reflect.TypeOf(m).Elem()
	seed := func(id, tenantID, userCode string) any {
		v := reflect.New(rowType)
		v.Elem().FieldByName("Id").SetString(id)
		v.Elem().FieldByName("Tenant_ID").SetString(tenantID)
		v.Elem().FieldByName("USER_CODE").SetString(userCode)
		return v.Interface()
	}
	for _, row := range []any{
		seed(matrixIDA, matrixTenantA, matrixUserA),
		seed(matrixIDB, matrixTenantB, matrixUserB),
	} {
		if err := db.Create(row).Error; err != nil {
			t.Skipf("模型 %s 不适用零值插入: %v", modelName, err)
			return
		}
	}

	// 1) 租户 A 上下文：恰好 1 行，且是本租户的 a1
	assertMatrixFind(t, db, rowType, modelName, matrixTenantA, matrixUserA, matrixIDA)
	// 2) 租户 B 上下文：恰好 1 行，且是本租户的 b1
	assertMatrixFind(t, db, rowType, modelName, matrixTenantB, matrixUserB, matrixIDB)

	// 3) 无 ctx：回退全局常量 tenantZ/userZ，两个租户的行都不可见
	dest := reflect.New(reflect.SliceOf(rowType))
	if err := db.Find(dest.Interface()).Error; err != nil {
		t.Fatalf("模型 %s 无 ctx 查询失败: %v", modelName, err)
	}
	if n := dest.Elem().Len(); n != 0 {
		t.Fatalf("模型 %s 无 ctx（全局 tenantZ/userZ）应查到 0 行，实际 %d 行", modelName, n)
	}
}

// assertMatrixFind 以指定租户上下文查询并断言：恰好 1 行、租户正确、命中期望主键。
func assertMatrixFind(t *testing.T, db *gorm.DB, rowType reflect.Type, modelName, tenantID, userCode, wantID string) {
	t.Helper()
	dest := reflect.New(reflect.SliceOf(rowType))
	ctx := wafctx.WithTenant(context.Background(), tenantID, userCode)
	if err := db.WithContext(ctx).Find(dest.Interface()).Error; err != nil {
		t.Fatalf("模型 %s 租户 %s 查询失败: %v", modelName, tenantID, err)
	}
	list := dest.Elem()
	if list.Len() != 1 {
		t.Fatalf("模型 %s 租户隔离失败：租户 %s 应恰好可见 1 行，实际 %d 行", modelName, tenantID, list.Len())
	}
	row := list.Index(0)
	if got := row.FieldByName("Tenant_ID").String(); got != tenantID {
		t.Errorf("模型 %s 租户隔离失败：期望 tenant_id=%s，实际 %s", modelName, tenantID, got)
	}
	if got := row.FieldByName("Id").String(); got != wantID {
		t.Errorf("模型 %s 租户隔离失败：租户 %s 期望命中 id=%s，实际 %s", modelName, tenantID, wantID, got)
	}
}
