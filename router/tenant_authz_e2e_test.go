package router

// 租户越权端到端回归（HTTP 黑盒）：验证「登录 → 令牌 → Auth 中间件注入请求租户 →
// GORM 租户回调过滤/盖章」整条链在真实 HTTP 栈上隔离租户数据。
//
// 与白盒单测的区别：wafdb/waf_service 层的单测直接构造 ctx 验证回调语义；
// 这里不构造任何请求 ctx，全部走真实 HTTP 请求（真实 Auth 中间件、真实路由、真实服务层），
// 租户身份唯一来源是登录下发的令牌——这正是白盒覆盖不到的链路。
//
// 场景：平台开通 tenantA / tenantB 两个租户（各自管理员 admin_a / admin_b），
// 各自通过 HTTP 建站；断言 A 的令牌无法通过管理端 HTTP 端点读取/写入/删除 B 的数据
// （含对称方向），另覆盖权限组 RBAC（只读组 403）与 legacy 角色兼容（无组 securityAdmin 200）。
//
// 管理端响应 data 走 legacy AES 加密通道（model/common/response.Result），
// 断言前先用已知密钥解包——与 router/waf_app_router_test.go 同一套做法。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"SamWaf/api"
	"SamWaf/cache"
	"SamWaf/common/queue"
	"SamWaf/common/uuid"
	"SamWaf/common/wafctx"
	"SamWaf/customtype"
	"SamWaf/enums"
	"SamWaf/global"
	"SamWaf/globalobj"
	"SamWaf/middleware"
	"SamWaf/model"
	"SamWaf/model/baseorm"
	"SamWaf/model/request"
	"SamWaf/model/spec"
	"SamWaf/model/wafenginmodel"
	"SamWaf/service/waf_service"
	"SamWaf/utils"
	"SamWaf/wafdb"
	"SamWaf/wafenginecore"
	"SamWaf/wafsec"

	sqlite "github.com/samwafgo/sqlitedriver"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	e2ePlatformTenant = "samwafplat" // 平台自身租户码（global.GWAF_TENANT_ID）
	e2eUserCode       = "user-e2e-0001"
	e2eTenantA        = "tenant_a"
	e2eTenantB        = "tenant_b"
	e2eAdminA         = "admin_a"
	e2eAdminB         = "admin_b"
	e2eInitPwd        = "Init#Pass2026"
	e2eNewPwd         = "New#Pass2026x"

	e2eLoginPath  = "/api/v1/public/login"
	e2eChangePwd  = "/api/v1/account/changemypwd"
	e2eHostList   = "/api/v1/wafhost/host/list"
	e2eHostDetail = "/api/v1/wafhost/host/detail"
	e2eHostAdd    = "/api/v1/wafhost/host/add"
	e2eHostEdit   = "/api/v1/wafhost/host/edit"
	e2eHostDel    = "/api/v1/wafhost/host/del"

	e2eHostA    = "host-a.example.com"
	e2eHostB    = "host-b.example.com"
	e2ePortA    = 18101
	e2ePortB    = 18102
	e2eNickname = "E2E站点"
)

// e2eEnv 持有整个测试共享的环境（gin 引擎、令牌、站点 code），由 setupTenantAuthzE2E 构建。
type e2eEnv struct {
	g *gin.Engine

	hostACode string
	hostBCode string

	tokenA string // tenantA 管理员（真实登录链）
	tokenB string // tenantB 管理员（真实登录链）
}

// ---------- 环境搭建 ----------

// setupTenantAuthzE2E 一次性构建完整环境：
// 临时 core/stats sqlite + 生产同款租户回调 + 内存缓存 + 消息队列/通道 + 引擎壳 +
// 预置组/平台租户播种 + 开通两个租户 + 双管理员真实登录（含首登改密）+ 双方各建一站。
func setupTenantAuthzE2E(t *testing.T) *e2eEnv {
	t.Helper()
	gin.SetMode(gin.TestMode) // zlog 保持包默认 nop logger，不打日志不落盘

	// ---------- 全局配置（全部保存现场，测试后恢复） ----------
	restoreStr := func(getter func() string, setter func(string)) func() {
		old := getter()
		return func() { setter(old) }
	}
	restoreI64 := func(getter func() int64, setter func(int64)) func() {
		old := getter()
		return func() { setter(old) }
	}
	var restores []func()
	t.Cleanup(func() {
		for _, r := range restores {
			r()
		}
	})
	reg := func(r func()) { restores = append(restores, r) }

	reg(restoreStr(func() string { return global.GWAF_TENANT_ID }, func(v string) { global.GWAF_TENANT_ID = v }))
	global.GWAF_TENANT_ID = e2ePlatformTenant
	reg(restoreStr(func() string { return global.GWAF_USER_CODE }, func(v string) { global.GWAF_USER_CODE = v }))
	global.GWAF_USER_CODE = e2eUserCode
	reg(restoreI64(func() int64 { return global.GCONFIG_RECORD_TOKEN_EXPIRE_MINTUTES }, func(v int64) { global.GCONFIG_RECORD_TOKEN_EXPIRE_MINTUTES = v }))
	global.GCONFIG_RECORD_TOKEN_EXPIRE_MINTUTES = 30
	reg(restoreI64(func() int64 { return global.GCONFIG_RECORD_LOGIN_MAX_ERROR_TIME }, func(v int64) { global.GCONFIG_RECORD_LOGIN_MAX_ERROR_TIME = v }))
	global.GCONFIG_RECORD_LOGIN_MAX_ERROR_TIME = 10
	reg(restoreI64(func() int64 { return global.GCONFIG_RECORD_LOGIN_LIMIT_MINTUTES }, func(v int64) { global.GCONFIG_RECORD_LOGIN_LIMIT_MINTUTES = v }))
	global.GCONFIG_RECORD_LOGIN_LIMIT_MINTUTES = 1
	reg(restoreI64(func() int64 { return global.GCONFIG_RECORD_FORCE_BIND_2FA }, func(v int64) { global.GCONFIG_RECORD_FORCE_BIND_2FA = v }))
	global.GCONFIG_RECORD_FORCE_BIND_2FA = 0
	reg(restoreI64(func() int64 { return global.GCONFIG_ENABLE_STRICT_IP_BINDING }, func(v int64) { global.GCONFIG_ENABLE_STRICT_IP_BINDING = v }))
	global.GCONFIG_ENABLE_STRICT_IP_BINDING = 0
	reg(restoreI64(func() int64 { return global.GCONFIG_ENABLE_DEVICE_FINGERPRINT }, func(v int64) { global.GCONFIG_ENABLE_DEVICE_FINGERPRINT = v }))
	global.GCONFIG_ENABLE_DEVICE_FINGERPRINT = 0
	reg(restoreI64(func() int64 { return global.GCONFIG_PWD_EXPIRE_DAYS }, func(v int64) { global.GCONFIG_PWD_EXPIRE_DAYS = v }))
	global.GCONFIG_PWD_EXPIRE_DAYS = 0
	reg(restoreI64(func() int64 { return global.GCONFIG_PWD_HISTORY_COUNT }, func(v int64) { global.GCONFIG_PWD_HISTORY_COUNT = v }))
	global.GCONFIG_PWD_HISTORY_COUNT = 0
	// 关闭口令复杂度策略中的可选项之外的依赖：显式给一套确定性的复杂度策略
	reg(restoreI64(func() int64 { return global.GCONFIG_PWD_MIN_LENGTH }, func(v int64) { global.GCONFIG_PWD_MIN_LENGTH = v }))
	global.GCONFIG_PWD_MIN_LENGTH = 8
	reg(restoreI64(func() int64 { return global.GCONFIG_PWD_REQUIRE_UPPER }, func(v int64) { global.GCONFIG_PWD_REQUIRE_UPPER = v }))
	global.GCONFIG_PWD_REQUIRE_UPPER = 1
	reg(restoreI64(func() int64 { return global.GCONFIG_PWD_REQUIRE_LOWER }, func(v int64) { global.GCONFIG_PWD_REQUIRE_LOWER = v }))
	global.GCONFIG_PWD_REQUIRE_LOWER = 1
	reg(restoreI64(func() int64 { return global.GCONFIG_PWD_REQUIRE_DIGIT }, func(v int64) { global.GCONFIG_PWD_REQUIRE_DIGIT = v }))
	global.GCONFIG_PWD_REQUIRE_DIGIT = 1
	reg(restoreI64(func() int64 { return global.GCONFIG_PWD_REQUIRE_SPECIAL }, func(v int64) { global.GCONFIG_PWD_REQUIRE_SPECIAL = v }))
	global.GCONFIG_PWD_REQUIRE_SPECIAL = 1

	// 响应体走 legacy AES 通道（Result 函数）：固定一把已知密钥便于解包断言
	reg(restoreI64(func() int64 {
		if global.GCONFIG_COMM_LEGACY_KEY {
			return 1
		}
		return 0
	}, func(v int64) { global.GCONFIG_COMM_LEGACY_KEY = v == 1 }))
	global.GCONFIG_COMM_LEGACY_KEY = true
	oldCommKey := global.GWAF_COMMUNICATION_KEY
	global.GWAF_COMMUNICATION_KEY = []byte("0123456789abcdef0123456789abcdef")
	t.Cleanup(func() { global.GWAF_COMMUNICATION_KEY = oldCommKey })

	// ---------- 临时库（core + stats） ----------
	coreDB := openE2ESQLite(t, "core.db",
		&model.Tenant{}, &model.PermissionGroup{}, &model.Account{}, &model.TokenInfo{}, &model.Otp{},
		&model.Hosts{}, &model.AntiCCRule{})
	{
		old := global.GWAF_LOCAL_DB
		global.GWAF_LOCAL_DB = coreDB
		t.Cleanup(func() { global.GWAF_LOCAL_DB = old })
	}
	statsDB := openE2ESQLite(t, "stats.db", &model.StatsSiteDay{})
	{
		old := global.GWAF_LOCAL_STATS_DB
		global.GWAF_LOCAL_STATS_DB = statsDB
		t.Cleanup(func() { global.GWAF_LOCAL_STATS_DB = old })
	}
	// GWAF_LOCAL_LOG_DB 保持原值：登录历史服务对 nil 库自我跳过，日志库不在本测试范围

	// ---------- 缓存 / 队列 / 通道 / 引擎壳 ----------
	{
		old := global.GCACHE_WAFCACHE
		global.GCACHE_WAFCACHE = cache.InitWafCache()
		t.Cleanup(func() { global.GCACHE_WAFCACHE = old })
	}
	{
		oldLogQ, oldMsgQ := global.GQEQUE_LOG_DB, global.GQEQUE_MESSAGE_DB
		global.GQEQUE_LOG_DB = queue.NewQueue()
		global.GQEQUE_MESSAGE_DB = queue.NewQueue()
		t.Cleanup(func() { global.GQEQUE_LOG_DB, global.GQEQUE_MESSAGE_DB = oldLogQ, oldMsgQ })
	}
	{
		oldMsg, oldHost := global.GWAF_CHAN_MSG, global.GWAF_CHAN_HOST
		global.GWAF_CHAN_MSG = make(chan spec.ChanCommonHost, 100)
		global.GWAF_CHAN_HOST = make(chan model.Hosts, 100)
		t.Cleanup(func() { global.GWAF_CHAN_MSG, global.GWAF_CHAN_HOST = oldMsg, oldHost })
	}
	{
		old := globalobj.GWAF_RUNTIME_OBJ_WAF_ENGINE
		globalobj.GWAF_RUNTIME_OBJ_WAF_ENGINE = &wafenginecore.WafEngine{ServerOnline: wafenginmodel.NewSafeServerMap()}
		t.Cleanup(func() { globalobj.GWAF_RUNTIME_OBJ_WAF_ENGINE = old })
	}

	// ---------- 播种：预置权限组 + 平台租户 + 开通两个租户 ----------
	if err := waf_service.WafPermissionGroupServiceApp.InitDefaultPermissionGroups(); err != nil {
		t.Fatalf("播种预置权限组失败: %v", err)
	}
	if err := waf_service.WafTenantServiceApp.InitDefaultTenant(); err != nil {
		t.Fatalf("播种平台租户失败: %v", err)
	}
	for _, tc := range []struct{ id, name, admin string }{
		{e2eTenantA, "租户A", e2eAdminA},
		{e2eTenantB, "租户B", e2eAdminB},
	} {
		if _, err := waf_service.WafTenantServiceApp.AddTenantApi(context.Background(), request.WafTenantAddReq{
			TenantID:      tc.id,
			TenantName:    tc.name,
			AdminAccount:  tc.admin,
			AdminPassword: e2eInitPwd,
		}); err != nil {
			t.Fatalf("开通租户 %s 失败: %v", tc.id, err)
		}
	}

	// ---------- gin 引擎：公开登录 + 生产同款 Auth/权限组挂载 ----------
	g := gin.New()
	publicGroup := g.Group("")
	// 生产在此额外挂 SecApi/IPWhitelist/DomainWhitelist/ReplayProtect（传输加密与访问面控制），
	// 均不属于本测试的鉴权链，故不复挂；登录接口保持明文 JSON 可直接调用。
	PublicApiGroupApp.InitLoginRouter(publicGroup)

	authGroup := g.Group("")
	authGroup.Use(middleware.Auth(), middleware.GinGlobalExceptionMiddleWare())
	// 自助改密在生产挂在 localserver.go 的基础组（任意已登录角色，无权限码），此处同款挂载
	authGroup.POST("/api/v1/account/changemypwd", api.APIGroupAPP.WafAccountApi.ChangeMyPasswordApi)

	legacyAll := []string{enums.ROLE_SYSTEM_ADMIN, enums.ROLE_SECURITY_ADMIN, enums.ROLE_AUDIT_ADMIN}
	hostGroup := authGroup.Group("")
	hostGroup.Use(middleware.RequirePermission(enums.PERM_HOST_MANAGE, legacyAll...))
	ApiGroupApp.InitHostRouter(hostGroup)

	pgGroup := authGroup.Group("")
	pgGroup.Use(middleware.RequirePermission(enums.PERM_GROUP_MANAGE, enums.ROLE_SYSTEM_ADMIN))
	ApiGroupApp.InitPermissionGroupRouter(pgGroup)

	env := &e2eEnv{g: g}

	// ---------- 真实登录链：登录 → 首登改密（解除强制改密门） ----------
	env.tokenA = env.loginAndActivate(t, e2eAdminA)
	env.tokenB = env.loginAndActivate(t, e2eAdminB)

	// ---------- 各自通过 HTTP 建站 ----------
	env.hostACode = env.addHost(t, env.tokenA, e2eHostA, e2ePortA)
	env.hostBCode = env.addHost(t, env.tokenB, e2eHostB, e2ePortB)

	drainE2EChans()
	return env
}

// openE2ESQLite 打开临时 sqlite、迁移表并注册生产同款租户回调（迁移后注册，与生产一致）。
func openE2ESQLite(t *testing.T, name string, models ...interface{}) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), name)),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("打开临时库 %s 失败: %v", name, err)
	}
	if err := db.AutoMigrate(models...); err != nil {
		t.Fatalf("迁移 %s 失败: %v", name, err)
	}
	wafdb.RegisterTenantScopeCallbacks(db)
	t.Cleanup(func() {
		if sqlDB, e := db.DB(); e == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

// ---------- HTTP 助手（响应 data 走 AES 加密通道，先解包再断言） ----------

type e2eReply struct {
	Code int             `json:"code"`
	Data json.RawMessage `json:"data"`
	Msg  string          `json:"msg"`
}

// doE2ERequest 发请求并解密响应体，返回业务码与解密后的 data。
func doE2ERequest(t *testing.T, g *gin.Engine, method, path, token, body string) (int, e2eReply) {
	t.Helper()
	var req *http.Request
	var err error
	if body != "" {
		req, err = http.NewRequest(method, path, bytes.NewBufferString(body))
	} else {
		req, err = http.NewRequest(method, path, nil)
	}
	if err != nil {
		t.Fatalf("构造请求 %s %s 失败: %v", method, path, err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("X-Token", token)
	}
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)

	var wire struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
		Msg  string          `json:"msg"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &wire); err != nil {
		t.Fatalf("%s %s 响应不是合法 JSON: %v，原始响应: %s", method, path, err, rec.Body.String())
	}
	reply := e2eReply{Code: wire.Code, Data: json.RawMessage("null"), Msg: wire.Msg}

	trimmed := strings.TrimSpace(string(wire.Data))
	switch {
	case trimmed == "" || trimmed == "null":
		// 无数据
	case trimmed[0] == '{' || trimmed[0] == '[':
		reply.Data = wire.Data // 已是明文 JSON（理论上不会出现，兜底直通）
	default:
		// Result() 对非 OpenAPI 请求一律把 data AES 加密成字符串下发
		var enc string
		if err := json.Unmarshal(wire.Data, &enc); err != nil {
			t.Fatalf("%s %s 响应 data 不是加密字符串: %v，原始: %s", method, path, err, trimmed)
		}
		plain, derr := wafsec.AesDecrypt(enc, global.GWAF_COMMUNICATION_KEY)
		if derr != nil {
			t.Fatalf("%s %s 响应 data 解密失败: %v，原始 data: %s", method, path, derr, trimmed)
		}
		reply.Data = json.RawMessage(plain)
	}
	return rec.Code, reply
}

func postE2EJSON(t *testing.T, g *gin.Engine, path, token, body string) (int, e2eReply) {
	t.Helper()
	return doE2ERequest(t, g, http.MethodPost, path, token, body)
}

func getE2E(t *testing.T, g *gin.Engine, path, token string) (int, e2eReply) {
	t.Helper()
	return doE2ERequest(t, g, http.MethodGet, path, token, "")
}

// drainE2EChans 非阻塞排空引擎通知通道，避免缓冲打满阻塞请求处理。
func drainE2EChans() {
	for {
		select {
		case <-global.GWAF_CHAN_MSG:
		case <-global.GWAF_CHAN_HOST:
		default:
			return
		}
	}
}

// ---------- 登录 / 建站 / 列表助手 ----------

// loginAndActivate 真实登录（开通创建的租户管理员强制首登改密），
// 走 /changemypwd 清除强制改密门，返回可用令牌。
func (e *e2eEnv) loginAndActivate(t *testing.T, account string) string {
	t.Helper()
	status, reply := postE2EJSON(t, e.g, e2eLoginPath, "", fmt.Sprintf(
		`{"login_account":%q,"login_password":%q}`, account, e2eInitPwd))
	if status != http.StatusOK || reply.Code != 0 {
		t.Fatalf("登录 %s 失败: http=%d code=%d msg=%s", account, status, reply.Code, reply.Msg)
	}
	var rep struct {
		AccessToken        string `json:"access_token"`
		NeedChangePassword bool   `json:"need_change_password"`
	}
	if err := json.Unmarshal(reply.Data, &rep); err != nil {
		t.Fatalf("解析登录响应失败: %v，data=%s", err, string(reply.Data))
	}
	if rep.AccessToken == "" {
		t.Fatalf("登录 %s 未下发令牌，data=%s", account, string(reply.Data))
	}
	if rep.NeedChangePassword {
		st, chg := postE2EJSON(t, e.g, e2eChangePwd, rep.AccessToken, fmt.Sprintf(
			`{"old_password":%q,"new_password":%q,"new_password2":%q}`, e2eInitPwd, e2eNewPwd, e2eNewPwd))
		if st != http.StatusOK || chg.Code != 0 {
			t.Fatalf("账号 %s 首登改密失败: http=%d code=%d msg=%s", account, st, chg.Code, chg.Msg)
		}
	}
	return rep.AccessToken
}

// addHost 以给定令牌通过 HTTP 建站，返回站点 code。
func (e *e2eEnv) addHost(t *testing.T, token, host string, port int) string {
	t.Helper()
	body := fmt.Sprintf(`{"host":%q,"port":%d,"ssl":0,"remote_ip":"127.0.0.1","remote_port":%d,"nickname":%q,"remarks":"e2e"}`,
		host, port, port, e2eNickname)
	status, reply := postE2EJSON(t, e.g, e2eHostAdd, token, body)
	if status != http.StatusOK || reply.Code != 0 {
		t.Fatalf("建站 %s 失败: http=%d code=%d msg=%s", host, status, reply.Code, reply.Msg)
	}
	var code string
	if err := json.Unmarshal(reply.Data, &code); err != nil || code == "" {
		t.Fatalf("建站 %s 未返回 code: %v，data=%s", host, err, string(reply.Data))
	}
	drainE2EChans()
	return code
}

type e2eHostItem struct {
	Code     string `json:"code"`
	Host     string `json:"host"`
	Nickname string `json:"nickname"`
}

// hostList 拉取站点列表。
func (e *e2eEnv) hostList(t *testing.T, token string) []e2eHostItem {
	t.Helper()
	status, reply := postE2EJSON(t, e.g, e2eHostList, token, `{"pageIndex":1,"pageSize":50}`)
	if status != http.StatusOK || reply.Code != 0 {
		t.Fatalf("站点列表请求失败: http=%d code=%d msg=%s", status, reply.Code, reply.Msg)
	}
	var page struct {
		List []e2eHostItem `json:"list"`
	}
	if err := json.Unmarshal(reply.Data, &page); err != nil {
		t.Fatalf("解析站点列表失败: %v，data=%s", err, string(reply.Data))
	}
	return page.List
}

// hostListContains 列表里是否含指定域名。
func (e *e2eEnv) hostListContains(t *testing.T, token, host string) bool {
	t.Helper()
	for _, it := range e.hostList(t, token) {
		if it.Host == host {
			return true
		}
	}
	return false
}

// detailCode 请求站点详情，返回响应里的站点 code（被拒绝/不可见时为空串）。
func (e *e2eEnv) detailCode(t *testing.T, token, code string) string {
	t.Helper()
	status, reply := getE2E(t, e.g, e2eHostDetail+"?code="+code, token)
	if status != http.StatusOK || reply.Code != 0 {
		return "" // 鉴权/业务层拒绝都视为不可见
	}
	var detail struct {
		Code string `json:"code"`
		Host string `json:"host"`
	}
	if err := json.Unmarshal(reply.Data, &detail); err != nil {
		t.Fatalf("解析详情失败: %v，data=%s", err, string(reply.Data))
	}
	return detail.Code
}

// ---------- 附加账号构造 ----------

// provisionReadOnlyAccount 在租户A 内造一个「只读组」账号并走真实登录链取令牌：
// 组经服务层创建（等价 POST /permission/group/add，权限码仅 dashboard:view），
// 账号直接落库（Account 平台豁免、BaseOrm 三要素手工设置，与开通逻辑同口径）。
func provisionReadOnlyAccount(t *testing.T, e *e2eEnv) string {
	t.Helper()
	ctx := wafctx.WithTenant(context.Background(), e2eTenantA, e2eUserCode)
	status := 1
	group, err := waf_service.WafPermissionGroupServiceApp.AddApi(ctx, request.WafPermissionGroupAddReq{
		GroupName:   "E2E只读组",
		Permissions: enums.PERM_DASHBOARD,
		Status:      &status,
		Remarks:     "tenant_authz_e2e",
	})
	if err != nil {
		t.Fatalf("创建只读权限组失败: %v", err)
	}
	return createAccountAndLogin(t, e, "ro_a", e2eTenantA, enums.ROLE_TENANT_ADMIN, group.Id)
}

// provisionLegacyAccount 造一个无权限组、Role=securityAdmin、归属租户A 的存量账号并登录。
func provisionLegacyAccount(t *testing.T, e *e2eEnv) string {
	t.Helper()
	return createAccountAndLogin(t, e, "leg_a", e2eTenantA, enums.ROLE_SECURITY_ADMIN, "")
}

// createAccountAndLogin 直插账号行（NeedChangePassword=0 免首登改密）后走真实登录链。
func createAccountAndLogin(t *testing.T, e *e2eEnv, account, tenantID, role, groupID string) string {
	t.Helper()
	hash, err := utils.BcryptHash(e2eNewPwd)
	if err != nil {
		t.Fatalf("bcrypt 失败: %v", err)
	}
	acc := &model.Account{
		BaseOrm: baseorm.BaseOrm{
			Id:          uuid.GenUUID(),
			USER_CODE:   e2eUserCode,
			Tenant_ID:   tenantID,
			CREATE_TIME: customtype.JsonTime(time.Now()),
			UPDATE_TIME: customtype.JsonTime(time.Now()),
		},
		LoginAccount:  account,
		Role:          role,
		GroupId:       groupID,
		LoginPassword: hash,
		Status:        0,
	}
	if err := global.GWAF_LOCAL_DB.Create(acc).Error; err != nil {
		t.Fatalf("创建账号 %s 失败: %v", account, err)
	}
	status, reply := postE2EJSON(t, e.g, e2eLoginPath, "", fmt.Sprintf(
		`{"login_account":%q,"login_password":%q}`, account, e2eNewPwd))
	if status != http.StatusOK || reply.Code != 0 {
		t.Fatalf("登录 %s 失败: http=%d code=%d msg=%s", account, status, reply.Code, reply.Msg)
	}
	var rep struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(reply.Data, &rep); err != nil || rep.AccessToken == "" {
		t.Fatalf("解析 %s 登录响应失败: %v data=%s", account, err, string(reply.Data))
	}
	return rep.AccessToken
}

// ===========================================================================
// 用例
// ===========================================================================

// TestTenantAuthzE2E 租户越权端到端回归主用例。
func TestTenantAuthzE2E(t *testing.T) {
	env := setupTenantAuthzE2E(t)

	t.Run("登录链与建站自证", func(t *testing.T) {
		if env.tokenA == "" || env.tokenB == "" {
			t.Fatal("两个租户管理员的令牌都必须拿到")
		}
		if env.hostACode == "" || env.hostBCode == "" {
			t.Fatal("两个租户的站点都必须建成功")
		}
		// 正向对照：各自能看到自己的站点（这条挂了先怀疑环境/流程，不是越权断言）
		if !env.hostListContains(t, env.tokenA, e2eHostA) {
			t.Fatalf("租户A 管理员应能看到自己的站点，实际列表: %+v", env.hostList(t, env.tokenA))
		}
		if !env.hostListContains(t, env.tokenB, e2eHostB) {
			t.Fatalf("租户B 管理员应能看到自己的站点，实际列表: %+v", env.hostList(t, env.tokenB))
		}
	})

	t.Run("A的列表不得出现B的站点", func(t *testing.T) {
		list := env.hostList(t, env.tokenA)
		for _, it := range list {
			if it.Host == e2eHostB || it.Code == env.hostBCode {
				t.Errorf("越权读取：租户A 的站点列表出现了租户B 的站点 host=%s code=%s，完整列表=%+v",
					it.Host, it.Code, list)
			}
		}
	})

	t.Run("A请求B的详情必须为空", func(t *testing.T) {
		if code := env.detailCode(t, env.tokenA, env.hostBCode); code == env.hostBCode {
			t.Errorf("越权读取：租户A 拿到了租户B 站点 %s 的详情", env.hostBCode)
		}
	})

	t.Run("A编辑B的站点必须无效", func(t *testing.T) {
		body := fmt.Sprintf(`{"code":%q,"host":%q,"port":%d,"nickname":"被A篡改","remarks":"hacked"}`,
			env.hostBCode, e2eHostB, e2ePortB)
		status, reply := postE2EJSON(t, env.g, e2eHostEdit, env.tokenA, body)
		drainE2EChans()
		// 无论编辑接口返回成功还是失败，最终效果必须是 B 的数据原样
		for _, it := range env.hostList(t, env.tokenB) {
			if it.Code == env.hostBCode && it.Nickname == "被A篡改" {
				t.Errorf("越权写入：租户A 修改租户B 站点成功(edit http=%d code=%d msg=%s)，B 的数据被篡改",
					status, reply.Code, reply.Msg)
			}
		}
	})

	t.Run("A删除B的站点必须无效", func(t *testing.T) {
		status, reply := getE2E(t, env.g, e2eHostDel+"?code="+env.hostBCode, env.tokenA)
		drainE2EChans()
		if !env.hostListContains(t, env.tokenB, e2eHostB) {
			t.Errorf("越权删除：租户A 删除租户B 站点成功(del http=%d code=%d msg=%s)，B 的站点不复存在",
				status, reply.Code, reply.Msg)
		}
	})

	t.Run("对称：B对A同样不可见不可删", func(t *testing.T) {
		for _, it := range env.hostList(t, env.tokenB) {
			if it.Host == e2eHostA || it.Code == env.hostACode {
				t.Errorf("越权读取(反向)：租户B 的站点列表出现了租户A 的站点 host=%s code=%s", it.Host, it.Code)
			}
		}
		if code := env.detailCode(t, env.tokenB, env.hostACode); code == env.hostACode {
			t.Errorf("越权读取(反向)：租户B 拿到了租户A 站点 %s 的详情", env.hostACode)
		}
		status, reply := getE2E(t, env.g, e2eHostDel+"?code="+env.hostACode, env.tokenB)
		drainE2EChans()
		if !env.hostListContains(t, env.tokenA, e2eHostA) {
			t.Errorf("越权删除(反向)：租户B 删除租户A 站点成功(del http=%d code=%d msg=%s)",
				status, reply.Code, reply.Msg)
		}
	})

	t.Run("权限组RBAC：只读组账号访问站点管理端点必须被拒", func(t *testing.T) {
		token := provisionReadOnlyAccount(t, env)
		status, reply := postE2EJSON(t, env.g, e2eHostList, token, `{"pageIndex":1,"pageSize":10}`)
		if reply.Code == 0 {
			t.Errorf("权限组判定失效：只读组(仅 %s)账号访问站点管理接口返回成功 http=%d msg=%s",
				enums.PERM_DASHBOARD, status, reply.Msg)
		}
	})

	t.Run("legacy角色兼容：无组securityAdmin可访问站点管理", func(t *testing.T) {
		token := provisionLegacyAccount(t, env)
		status, reply := postE2EJSON(t, env.g, e2eHostList, token, `{"pageIndex":1,"pageSize":10}`)
		if status != http.StatusOK || reply.Code != 0 {
			t.Errorf("legacy 兼容回归：无权限组、Role=%s 的账号应能访问站点管理(旧四角色行为不变)，"+
				"实际 http=%d code=%d msg=%s", enums.ROLE_SECURITY_ADMIN, status, reply.Code, reply.Msg)
		}
	})
}
