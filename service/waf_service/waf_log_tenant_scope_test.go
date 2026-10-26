package waf_service

// 日志查询链路的租户隔离（共享库多租户）：
// GetListApiWithMeta / GetDetailApi / GetListByHostCodeApi 的分区解析、
// 扇出与逐层点查都要随请求 ctx（wafctx 声明的租户）过滤——Table() 查询同样被
// before_query 回调过滤（锚定于 wafdb/tenant_bypass_channels_test.go）。
// 这里用生产同款 wafdb.RegisterTenantScopeCallbacks 回调（而非手写 Where），
// 保证测试锚定的是真实回调行为，而不是测试自己复刻的一份。
//
// 实时列表实际读的表：ResolveTierTables(live) 返回 access_log（访问视图默认）/
// security_event（事件视图），web_logs 只作详情兜底与按站点查询通道的存量表。

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"SamWaf/common/wafctx"
	"SamWaf/global"
	"SamWaf/innerbean"
	"SamWaf/model"
	commonrequest "SamWaf/model/common/request"
	"SamWaf/model/request"
	"SamWaf/wafdb"
	"SamWaf/wafdb/dialect"

	sqlite "github.com/samwafgo/sqlitedriver"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// setupLogTenantScope 独立临时 core/log 库 + 生产租户回调 + 全局兜底租户指向不存在租户。
// 任何命中都只能来自请求上下文，杜绝"全局常量恰好等于种子租户"造成的假绿。
func setupLogTenantScope(t *testing.T) {
	t.Helper()
	// 日志分区解析（LiveLogName/ResolveTierTables）依赖当前驱动；未注册会 panic，先落 SQLite
	priorDialect := func() (d dialect.DBDialect) {
		defer func() { _ = recover() }()
		return dialect.Get()
	}()
	dialect.Register(&dialect.SQLiteDialect{})

	open := func(name string) *gorm.DB {
		db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), name)),
			&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		if err != nil {
			t.Fatalf("打开测试库失败: %v", err)
		}
		wafdb.RegisterTenantScopeCallbacks(db)
		t.Cleanup(func() {
			if sqlDB, e := db.DB(); e == nil {
				_ = sqlDB.Close()
			}
		})
		return db
	}

	core, logDB := open("log_tenant_core.db"), open("log_tenant_log.db")
	if err := core.AutoMigrate(&model.ShareDb{}); err != nil {
		t.Fatalf("建 share_dbs 失败: %v", err)
	}
	if err := logDB.AutoMigrate(&model.AccessLog{}, &model.SecurityEvent{},
		&model.EventPayload{}, &innerbean.WebLog{}); err != nil {
		t.Fatalf("建日志表失败: %v", err)
	}

	oldCore, oldLog := global.GWAF_LOCAL_DB, global.GWAF_LOCAL_LOG_DB
	oldTenant, oldUser := global.GWAF_TENANT_ID, global.GWAF_USER_CODE
	global.GWAF_LOCAL_DB, global.GWAF_LOCAL_LOG_DB = core, logDB
	global.GWAF_TENANT_ID, global.GWAF_USER_CODE = "tenantZ", "userZ"
	InvalidateShardCounts()
	t.Cleanup(func() {
		global.GWAF_LOCAL_DB, global.GWAF_LOCAL_LOG_DB = oldCore, oldLog
		global.GWAF_TENANT_ID, global.GWAF_USER_CODE = oldTenant, oldUser
		InvalidateShardCounts()
		dialect.Register(priorDialect)
	})
}

func TestLogQueryTenantScope(t *testing.T) {
	setupLogTenantScope(t)
	logDB := global.GWAF_LOCAL_LOG_DB

	now := time.Now()
	mkAccess := func(reqUuid, tenantID, userCode string, at time.Time) model.AccessLog {
		return model.AccessLog{LogNarrow: model.LogNarrow{
			ReqUUID: reqUuid, TenantId: tenantID, UserCode: userCode,
			HostCode: "scope-host", Host: "scope.test.com", URL: "/x", Method: "GET",
			ACTION: "放行", Day: at.Year()*10000 + int(at.Month())*100 + at.Day(),
			UNIX_ADD_TIME: at.UnixMilli(), CREATE_TIME: at.Format("2006-01-02 15:04:05"),
		}}
	}
	accessSeeds := []model.AccessLog{
		mkAccess("scope-a1", "tenantA", "userA", now.Add(-2*time.Minute)),
		mkAccess("scope-a2", "tenantA", "userA", now.Add(-3*time.Minute)),
		mkAccess("scope-b1", "tenantB", "userB", now.Add(-4*time.Minute)),
	}
	for i := range accessSeeds {
		if err := logDB.Create(&accessSeeds[i]).Error; err != nil {
			t.Fatalf("写入 access_log 种子数据失败: %v", err)
		}
	}
	// web_logs：按站点查询通道（GetListByHostCodeApi）与详情兜底层
	for _, w := range []innerbean.WebLog{
		{REQ_UUID: "scope-wa", TenantId: "tenantA", USER_CODE: "userA",
			HOST_CODE: "scope-host", CREATE_TIME: now.Format("2006-01-02 15:04:05")},
		{REQ_UUID: "scope-wb", TenantId: "tenantB", USER_CODE: "userB",
			HOST_CODE: "scope-host", CREATE_TIME: now.Format("2006-01-02 15:04:05")},
	} {
		if err := logDB.Create(&w).Error; err != nil {
			t.Fatalf("写入 web_logs 种子数据失败: %v", err)
		}
	}

	ctxA := wafctx.WithTenant(context.Background(), "tenantA", "userA")
	ctxB := wafctx.WithTenant(context.Background(), "tenantB", "userB")

	baseReq := func() request.WafAttackLogSearch {
		r := request.WafAttackLogSearch{}
		r.PageIndex = 1
		r.PageSize = 10
		r.SortBy = "unix_add_time"
		r.SortDescending = "desc"
		r.UnixAddTimeBegin = strconv.FormatInt(now.Add(-10*time.Minute).UnixMilli(), 10)
		r.UnixAddTimeEnd = strconv.FormatInt(now.Add(10*time.Minute).UnixMilli(), 10)
		return r
	}

	t.Run("列表只返回上下文租户的行", func(t *testing.T) {
		rows, total, meta, err := WafLogServiceApp.GetListApiWithMeta(ctxA, baseReq())
		if err != nil {
			t.Fatalf("GetListApiWithMeta 失败: %v", err)
		}
		if total != 2 || len(rows) != 2 {
			t.Fatalf("tenantA 期望 2 行，实际 len=%d total=%d", len(rows), total)
		}
		for _, r := range rows {
			if r.TenantId != "tenantA" {
				t.Errorf("泄漏其他租户数据：req_uuid=%s tenant=%s", r.REQ_UUID, r.TenantId)
			}
		}
		rows, total, _, err = WafLogServiceApp.GetListApiWithMeta(ctxB, baseReq())
		if err != nil {
			t.Fatalf("GetListApiWithMeta(tenantB) 失败: %v", err)
		}
		if total != 1 || len(rows) != 1 || rows[0].REQ_UUID != "scope-b1" {
			t.Fatalf("tenantB 期望仅命中 scope-b1，实际 len=%d total=%d", len(rows), total)
		}
		if len(meta.Shards) == 0 {
			t.Fatalf("实时分区应记入 meta.Shards")
		}
	})

	t.Run("识别码直查跨租户不可见", func(t *testing.T) {
		req := baseReq()
		req.ReqUuid = "scope-b1" // tenantB 的识别码
		rows, total, meta, err := WafLogServiceApp.GetListApiWithMeta(ctxA, req)
		if err != nil {
			t.Fatalf("识别码直查失败: %v", err)
		}
		if !meta.UuidLookup || total != 0 || len(rows) != 0 {
			t.Fatalf("tenantA 直查 tenantB 的识别码应不可见，实际 len=%d total=%d uuid_lookup=%v",
				len(rows), total, meta.UuidLookup)
		}
		// 租户自己按识别码直查应命中
		rows, total, _, err = WafLogServiceApp.GetListApiWithMeta(ctxB, req)
		if err != nil {
			t.Fatalf("识别码直查(tenantB) 失败: %v", err)
		}
		if total != 1 || len(rows) != 1 {
			t.Fatalf("tenantB 直查自己的识别码应命中，实际 len=%d total=%d", len(rows), total)
		}
	})

	t.Run("详情按识别码跨租户不可见", func(t *testing.T) {
		got, err := WafLogServiceApp.GetDetailApi(ctxA, request.WafAttackLogDetailReq{REQ_UUID: "scope-a1"})
		if err != nil {
			t.Fatalf("tenantA 查自己详情失败: %v", err)
		}
		if got.REQ_UUID != "scope-a1" || got.TenantId != "tenantA" {
			t.Fatalf("tenantA 详情应命中 scope-a1，实际 req_uuid=%q tenant=%q", got.REQ_UUID, got.TenantId)
		}
		// tenantA 拿着 tenantB 的识别码点查：分区定位与逐层点查都必须不可见
		if _, err := WafLogServiceApp.GetDetailApi(ctxA, request.WafAttackLogDetailReq{REQ_UUID: "scope-b1"}); !errors.Is(err, ErrLogNotFound) {
			t.Fatalf("tenantA 点查 tenantB 识别码应返回未找到，实际 err=%v", err)
		}
		// ctxB 查询 web_logs 存量行（详情兜底层）也按租户过滤
		got, err = WafLogServiceApp.GetDetailApi(ctxB, request.WafAttackLogDetailReq{REQ_UUID: "scope-wb"})
		if err != nil {
			t.Fatalf("tenantB 查自己 web_logs 详情失败: %v", err)
		}
		if got.REQ_UUID != "scope-wb" {
			t.Fatalf("tenantB 应命中 scope-wb，实际 %q", got.REQ_UUID)
		}
		if _, err := WafLogServiceApp.GetDetailApi(ctxA, request.WafAttackLogDetailReq{REQ_UUID: "scope-wb"}); !errors.Is(err, ErrLogNotFound) {
			t.Fatalf("tenantA 点查 tenantB 的 web_logs 行应返回未找到，实际 err=%v", err)
		}
	})

	t.Run("按站点查询通道按上下文过滤", func(t *testing.T) {
		rows, total, err := WafLogServiceApp.GetListByHostCodeApi(ctxA, request.WafAttackLogSearch{
			HostCode: "scope-host", PageInfo: commonrequest.PageInfo{PageIndex: 1, PageSize: 10}})
		if err != nil {
			t.Fatalf("GetListByHostCodeApi 失败: %v", err)
		}
		if total != 1 || len(rows) != 1 || rows[0].REQ_UUID != "scope-wa" {
			t.Fatalf("tenantA 按站点查询应仅命中 scope-wa，实际 len=%d total=%d", len(rows), total)
		}
		rows, total, _ = WafLogServiceApp.GetListByHostCodeApi(ctxB, request.WafAttackLogSearch{
			HostCode: "scope-host", PageInfo: commonrequest.PageInfo{PageIndex: 1, PageSize: 10}})
		if total != 1 || len(rows) != 1 || rows[0].REQ_UUID != "scope-wb" {
			t.Fatalf("tenantB 按站点查询应仅命中 scope-wb，实际 len=%d total=%d", len(rows), total)
		}
	})

	t.Run("无上下文回退全局常量", func(t *testing.T) {
		rows, total, _, err := WafLogServiceApp.GetListApiWithMeta(context.Background(), baseReq())
		if err != nil {
			t.Fatalf("GetListApiWithMeta 失败: %v", err)
		}
		if total != 0 || len(rows) != 0 {
			t.Fatalf("无 ctx 应回退全局租户(tenantZ)期望 0 行，实际 %d 行", len(rows))
		}
		// 全局切到 tenantA 后回退路径仍生效（单租户时代行为）
		global.GWAF_TENANT_ID, global.GWAF_USER_CODE = "tenantA", "userA"
		defer func() { global.GWAF_TENANT_ID, global.GWAF_USER_CODE = "tenantZ", "userZ" }()
		rows, total, _, err = WafLogServiceApp.GetListApiWithMeta(context.Background(), baseReq())
		if err != nil {
			t.Fatalf("GetListApiWithMeta 失败: %v", err)
		}
		if total != 2 || len(rows) != 2 {
			t.Fatalf("无 ctx 回退全局(tenantA)失败：期望 2 行，实际 %d 行", len(rows))
		}
	})

	t.Run("平台作用域可见全部租户", func(t *testing.T) {
		// 引擎数据面用 PlatformScope 读全部租户的日志（如导出/巡检）；
		// 平台标记须穿过扇出派生的 WithTimeout 仍然生效
		rows, total, _, err := WafLogServiceApp.GetListApiWithMeta(wafctx.PlatformScope(context.Background()), baseReq())
		if err != nil {
			t.Fatalf("GetListApiWithMeta(PlatformScope) 失败: %v", err)
		}
		if total != 3 || len(rows) != 3 {
			t.Fatalf("平台作用域期望 3 行（全部租户），实际 len=%d total=%d", len(rows), total)
		}
	})
}
