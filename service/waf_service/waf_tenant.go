package waf_service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"SamWaf/common/uuid"
	"SamWaf/common/wafctx"
	"SamWaf/common/zlog"
	"SamWaf/customtype"
	"SamWaf/enums"
	"SamWaf/global"
	"SamWaf/model"
	"SamWaf/model/baseorm"
	"SamWaf/model/request"
	"SamWaf/utils"

	"gorm.io/gorm"
)

type WafTenantService struct{}

var WafTenantServiceApp = new(WafTenantService)

// tenantExpireTimeLayout 租户到期时间的落库/解析格式（tenants.expire_time，空=永久）
const tenantExpireTimeLayout = "2006-01-02 15:04:05"

// tenantPolicyCacheTTL 租户策略缓存 TTL：请求期每个请求都会读，
// 增改删由服务层主动失效，TTL 只是防「失效遗漏」的最后兜底，别调大。
// 策略变化（停用/续期/注销）最迟 5 分钟在请求期生效——可接受的延迟，
// 登录路径不走缓存、始终实时校验（见 CheckTenantLoginable）。
const tenantPolicyCacheTTL = 5 * time.Minute

// tenantIDPattern 租户码规则：小写字母/数字/下划线/中划线，3-32 位
var tenantIDPattern = regexp.MustCompile(`^[a-z0-9_-]{3,32}$`)

// tenantAdminGroupName 租户开通时自动创建的管理员权限组名
const tenantAdminGroupName = "租户管理员"

// tenantAdminPermissionCodes 租户管理员组的权限码：日常运营全套，
// 刻意不含 PERM_PLATFORM_ADMIN（平台域）与 PERM_GROUP_MANAGE（权限组管理本身）
var tenantAdminPermissionCodes = []string{
	enums.PERM_DASHBOARD,
	enums.PERM_HOST_MANAGE,
	enums.PERM_LOG_VIEW,
	enums.PERM_SECURITY,
	enums.PERM_AUDIT,
	enums.PERM_SYSTEM_OPS,
	enums.PERM_ACCOUNT_MANAGE,
}

// tenantPolicySnapshot 缓存中的租户策略快照（RequestPolicy 的判定输入）。
// 「租户不存在」也缓存（Found=false）：平台自身/存量实例没有租户行是常态，
// 不缓存会让每个请求都打一次库。
type tenantPolicySnapshot struct {
	Found      bool   `json:"found"`
	Status     int    `json:"status"`
	ExpireTime string `json:"expire_time"`
	GraceDays  int    `json:"grace_days"`
}

// CheckTenantLoginable 登录期租户校验（无 ctx，登录路径实时查库不走缓存）。
//   - 租户不存在 → nil（向后兼容：存量实例/平台自身没有租户行）
//   - Suspended → 停用；Cancelled → 注销；均拒绝登录
//   - Expired → 比较 now 与 ExpireTime+GraceDays 天：超宽限拒绝，宽限内放行
//     （宽限期内可登录，但请求期由 Auth 中间件按 RequestPolicy 限为只读）
//   - Active → nil
func (receiver *WafTenantService) CheckTenantLoginable(tenantID string) error {
	if tenantID == "" {
		return nil
	}
	// tenants 表本身平台豁免（tenantExemptModels），直接读也可，
	// 这里统一走 PlatformScope 语义，明确「登录校验是平台视角的跨租户读」
	bean, err := receiver.getTenantByCode(wafctx.PlatformScope(context.Background()), tenantID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	switch bean.Status {
	case enums.TenantStatusSuspended:
		return errors.New("租户已停用，请联系平台管理员")
	case enums.TenantStatusCancelled:
		return errors.New("租户已注销")
	case enums.TenantStatusExpired:
		if tenantExpiredBeyondGrace(bean.ExpireTime, bean.GraceDays, time.Now()) {
			return errors.New("租户已过期且超出宽限期，请联系平台管理员续期")
		}
		return nil
	default:
		// Active 及未知状态放行（未知状态向后兼容，不因枚举扩充锁死登录）
		return nil
	}
}

// RequestPolicy 请求期租户策略（Auth 中间件每个请求调用，签名不可随意变更）：
//   - 租户不存在 → (false,false)
//   - Suspended/Cancelled → (true,false) 拒绝一切请求
//   - Expired → 超出宽限期 (true,false)；宽限期内 (false,true) 只读
//   - Active → (false,false)
//
// 判定输入（状态/到期时间）走 5 分钟缓存，到期推算用「当前时间」实时比较——
// 到期过渡本身是即时的，只有状态/到期时间的修改最迟 5 分钟生效（可接受）。
// 缓存后端抖动按放行处理：登录期已实时校验过，这里 fail-open 保可用性。
func (receiver *WafTenantService) RequestPolicy(tenantID string) (deny bool, readOnly bool) {
	if tenantID == "" {
		return false, false
	}
	key := enums.CACHE_TENANT_STATUS + tenantID
	var snap tenantPolicySnapshot
	if err := global.GCACHE_WAFCACHE.GetAsEx(key, &snap, tenantPolicyCacheTTL); err != nil {
		// 未命中或后端异常 → 查库回填
		var tenant model.Tenant
		dbErr := global.GWAF_LOCAL_DB.WithContext(wafctx.PlatformScope(context.Background())).
			Where("tenant_id = ?", tenantID).First(&tenant).Error
		switch {
		case dbErr == nil:
			snap = tenantPolicySnapshot{
				Found:      true,
				Status:     tenant.Status,
				ExpireTime: tenant.ExpireTime,
				GraceDays:  tenant.GraceDays,
			}
			global.GCACHE_WAFCACHE.SetWithTTl(key, snap, tenantPolicyCacheTTL)
		case errors.Is(dbErr, gorm.ErrRecordNotFound):
			snap = tenantPolicySnapshot{Found: false}
			global.GCACHE_WAFCACHE.SetWithTTl(key, snap, tenantPolicyCacheTTL)
		default:
			// 数据库故障：登录态已在登录期校验过，这里按放行处理保可用
			zlog.Warn("租户策略查询失败，本次按放行处理", "tenant", tenantID, "error", dbErr.Error())
			return false, false
		}
	}
	if !snap.Found {
		return false, false
	}
	return evaluateTenantPolicy(snap.Status, snap.ExpireTime, snap.GraceDays)
}

// tenantPermCeilingSnapshot 租户权限天花板的缓存快照。
// Found=false 表示「无天花板」（无租户行/未绑套餐/套餐行缺失）——同样缓存，
// 避免平台自身与存量租户的每个请求都打一次库。
type tenantPermCeilingSnapshot struct {
	Found bool            `json:"found"`
	Perms map[string]bool `json:"perms"`
}

// TenantPermissionCeiling 租户权限天花板：该租户任何账号（含权限组授权）可持有的
// 权限码上限 = 所绑套餐的权限集。返回 nil 表示无天花板（平台自身/未绑套餐/无租户行，
// 行为与套餐机制引入前一致）。RequirePermission 据此强制——即使租户的权限组被
// 手工改大，也无法越出套餐边界。
//
// 快照走 5 分钟缓存（与租户策略缓存同口径），套餐/租户编辑时由服务层主动失效。
// 数据库故障按无天花板处理（fail-open）：天花板是差异化控制与纵深防御，
// 不是租户数据隔离的边界（那由 GORM 租户回调保证），故障时放行保可用。
func (receiver *WafTenantService) TenantPermissionCeiling(tenantID string) map[string]bool {
	if tenantID == "" {
		return nil
	}
	key := enums.CACHE_TENANT_PERM_CEILING + tenantID
	var snap tenantPermCeilingSnapshot
	if err := global.GCACHE_WAFCACHE.GetAsEx(key, &snap, tenantPolicyCacheTTL); err != nil {
		pctx := wafctx.PlatformScope(context.Background())
		var tenant model.Tenant
		dbErr := global.GWAF_LOCAL_DB.WithContext(pctx).
			Where("tenant_id = ?", tenantID).First(&tenant).Error
		switch {
		case dbErr == nil && strings.TrimSpace(tenant.PlanCode) != "":
			plan := WafPlanServiceApp.GetByCodeApi(pctx, tenant.PlanCode)
			if plan.Id == "" {
				// 套餐被引用期间不可删（DelApi 校验），这里是异常数据的兜底：不设天花板
				snap = tenantPermCeilingSnapshot{Found: false}
			} else {
				snap = tenantPermCeilingSnapshot{Found: true, Perms: enums.ParsePermissionSet(plan.Permissions)}
			}
			global.GCACHE_WAFCACHE.SetWithTTl(key, snap, tenantPolicyCacheTTL)
		case dbErr == nil, errors.Is(dbErr, gorm.ErrRecordNotFound):
			snap = tenantPermCeilingSnapshot{Found: false}
			global.GCACHE_WAFCACHE.SetWithTTl(key, snap, tenantPolicyCacheTTL)
		default:
			zlog.Warn("租户权限天花板查询失败，本次按无天花板处理", "tenant", tenantID, "error", dbErr.Error())
			return nil
		}
	}
	if !snap.Found {
		return nil
	}
	return snap.Perms
}

// CheckTenantQuota 租户配额校验（AddApi 挂钩，host/account 两个维度，0=不限）。
// 租户码取请求 ctx，未声明时回退全局（=平台自身名下的行数，平台自身通常无租户行 → 不受限）。
func (receiver *WafTenantService) CheckTenantQuota(ctx context.Context, kind string) error {
	scopeCtx := ctx
	if scopeCtx == nil {
		scopeCtx = context.Background()
	}
	tenantID, userCode, hasCtx := wafctx.TenantFrom(scopeCtx)
	if !hasCtx {
		tenantID, userCode = global.GWAF_TENANT_ID, global.GWAF_USER_CODE
	}
	if userCode == "" {
		userCode = global.GWAF_USER_CODE
	}
	if tenantID == "" {
		return nil
	}

	var tenant model.Tenant
	err := global.GWAF_LOCAL_DB.WithContext(wafctx.PlatformScope(scopeCtx)).
		Where("tenant_id = ?", tenantID).First(&tenant).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil // 无租户行 = 平台自身，不受限
		}
		return err
	}

	var maxQuota int
	switch kind {
	case enums.TenantQuotaKindHost:
		maxQuota = tenant.MaxHosts
	case enums.TenantQuotaKindAccount:
		maxQuota = tenant.MaxAccounts
	default:
		return nil
	}
	if maxQuota <= 0 {
		return nil // 0=不限
	}

	// 统计该租户现有行数。说明：model.Account 在 tenantExemptModels 平台豁免清单里，
	// 租户回调不会给它追加过滤条件，所以这里对两个维度都显式带上租户条件，
	// 与回调语义（ctx 带租户按租户过滤、无 ctx 回退全局）保持一致——
	// 对 hosts 而言与回调追加的条件等价（重复 AND 同条件无副作用）。
	var cnt int64
	var countErr error
	where := "tenant_id = ? and user_code = ?"
	if kind == enums.TenantQuotaKindHost {
		countErr = global.GWAF_LOCAL_DB.WithContext(scopeCtx).Model(&model.Hosts{}).
			Where(where, tenantID, userCode).Count(&cnt).Error
	} else {
		countErr = global.GWAF_LOCAL_DB.WithContext(scopeCtx).Model(&model.Account{}).
			Where(where, tenantID, userCode).Count(&cnt).Error
	}
	if countErr != nil {
		return countErr
	}
	if cnt >= int64(maxQuota) {
		if kind == enums.TenantQuotaKindHost {
			return fmt.Errorf("租户站点数已达配额上限(%d)，请联系平台管理员升级", maxQuota)
		}
		return fmt.Errorf("租户账号数已达配额上限(%d)，请联系平台管理员升级", maxQuota)
	}
	return nil
}

// GetListApi 分页查询租户（tenants 表平台豁免，列表即全平台），tenant_name 模糊 + status 筛选。
func (receiver *WafTenantService) GetListApi(ctx context.Context, req request.WafTenantSearchReq) ([]model.Tenant, int64, error) {
	var list []model.Tenant
	var total int64 = 0

	db := global.GWAF_LOCAL_DB.WithContext(ctx).Model(&model.Tenant{})
	db = applyTenantFilters(db, req)
	if err := db.Order("CREATE_TIME desc").
		Limit(req.PageSize).Offset(req.PageSize * (req.PageIndex - 1)).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	cntDb := global.GWAF_LOCAL_DB.WithContext(ctx).Model(&model.Tenant{})
	cntDb = applyTenantFilters(cntDb, req)
	if err := cntDb.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// GetDetailByIdApi 按记录ID查租户详情，未命中返回零值。
func (receiver *WafTenantService) GetDetailByIdApi(ctx context.Context, id string) model.Tenant {
	var bean model.Tenant
	global.GWAF_LOCAL_DB.WithContext(ctx).Where("id = ?", id).Find(&bean)
	return bean
}

// AddTenantApi 开通租户（provisioning）。用 PlatformScope(ctx) 依次落库：
//  1. 租户行（BaseOrm 三要素手工设置：Tenant_ID=新租户码）
//  2. 复制平台自身的 BuiltIn 预置组到新租户（组名/权限相同，新 Id、新租户码）
//  3. 「租户管理员」组（授权集取套餐 permissions；未指定套餐回落默认全集）
//  4. 租户管理员账号（不复用 createAccount——它盖全局租户戳；这里手工设置 BaseOrm 三要素）
//
// 套餐（plan_code 非空时）：必须存在且启用；管理员组授权集=套餐权限集，
// 配额/宽限期未显式传值（nil）时取套餐默认值，显式传值（含 0=不限）优先。
func (receiver *WafTenantService) AddTenantApi(ctx context.Context, req request.WafTenantAddReq) (model.Tenant, error) {
	tenantID := strings.TrimSpace(req.TenantID)
	if !tenantIDPattern.MatchString(tenantID) {
		return model.Tenant{}, errors.New("租户码只允许小写字母/数字/下划线/中划线，长度3-32")
	}
	tenantName := strings.TrimSpace(req.TenantName)
	if tenantName == "" {
		return model.Tenant{}, errors.New("租户名称不能为空")
	}
	maxHosts, maxAccounts, graceDays, err := resolveTenantQuotas(ctx, req.MaxHosts, req.MaxAccounts, req.GraceDays, req.PlanCode)
	if err != nil {
		return model.Tenant{}, err
	}
	expireTime := strings.TrimSpace(req.ExpireTime)
	if expireTime != "" {
		if _, err = time.ParseInLocation(tenantExpireTimeLayout, expireTime, time.Local); err != nil {
			return model.Tenant{}, errors.New("到期时间格式应为 2006-01-02 15:04:05")
		}
	}
	adminAccount := strings.TrimSpace(req.AdminAccount)
	if adminAccount == "" {
		return model.Tenant{}, errors.New("租户管理员登录名不能为空")
	}
	if req.AdminPassword == "" {
		return model.Tenant{}, errors.New("租户管理员初始密码不能为空")
	}

	// 平台作用域：写新租户的行不受请求租户过滤/盖章影响，BaseOrm 手工赋值原样落库
	pctx := wafctx.PlatformScope(ctx)
	db := global.GWAF_LOCAL_DB.WithContext(pctx)

	var cnt int64
	if err = db.Model(&model.Tenant{}).Where("tenant_id = ?", tenantID).Count(&cnt).Error; err != nil {
		return model.Tenant{}, err
	}
	if cnt > 0 {
		return model.Tenant{}, errors.New("租户码已存在: " + tenantID)
	}
	var accCnt int64
	if err = db.Model(&model.Account{}).Where("login_account = ?", adminAccount).Count(&accCnt).Error; err != nil {
		return model.Tenant{}, err
	}
	if accCnt > 0 {
		return model.Tenant{}, errors.New("登录名已存在: " + adminAccount)
	}

	hash, err := utils.BcryptHash(req.AdminPassword)
	if err != nil {
		return model.Tenant{}, err
	}
	now := customtype.JsonTime(time.Now())

	// 1. 租户行
	bean := &model.Tenant{
		BaseOrm: baseorm.BaseOrm{
			Id:          uuid.GenUUID(),
			USER_CODE:   global.GWAF_USER_CODE,
			Tenant_ID:   tenantID,
			CREATE_TIME: now,
			UPDATE_TIME: now,
		},
		TenantID:    tenantID,
		TenantName:  tenantName,
		Status:      enums.TenantStatusActive,
		PlanCode:    strings.TrimSpace(req.PlanCode),
		MaxHosts:    maxHosts,
		MaxAccounts: maxAccounts,
		ExpireTime:  expireTime,
		GraceDays:   graceDays,
		Remarks:     strings.TrimSpace(req.Remarks),
	}
	if err = db.Create(bean).Error; err != nil {
		return model.Tenant{}, err
	}

	// 2. 复制平台自身的 BuiltIn 预置组到新租户。
	//    必须限定平台租户（global 标识）：各租户的开箱副本也是 BuiltIn=1，
	//    不限定的话会把别的租户的副本层层复制进来。
	var builtinGroups []model.PermissionGroup
	if err = db.Model(&model.PermissionGroup{}).
		Where("tenant_id = ? and user_code = ? and built_in = 1", global.GWAF_TENANT_ID, global.GWAF_USER_CODE).
		Order("CREATE_TIME asc").Find(&builtinGroups).Error; err != nil {
		return model.Tenant{}, err
	}
	for _, g := range builtinGroups {
		ng := &model.PermissionGroup{
			BaseOrm: baseorm.BaseOrm{
				Id:          uuid.GenUUID(),
				USER_CODE:   global.GWAF_USER_CODE,
				Tenant_ID:   tenantID,
				CREATE_TIME: now,
				UPDATE_TIME: now,
			},
			GroupName:   g.GroupName,
			Remarks:     g.Remarks,
			Permissions: g.Permissions,
			Status:      g.Status,
			BuiltIn:     1,
		}
		if err = db.Create(ng).Error; err != nil {
			return model.Tenant{}, err
		}
	}

	// 3. 租户管理员组：授权集取套餐（有套餐时），否则默认全集
	adminPerms := strings.Join(tenantAdminPermissionCodes, ",")
	if plan := WafPlanServiceApp.GetByCodeApi(pctx, strings.TrimSpace(req.PlanCode)); plan.Id != "" {
		adminPerms = plan.Permissions
	}
	adminGroup := &model.PermissionGroup{
		BaseOrm: baseorm.BaseOrm{
			Id:          uuid.GenUUID(),
			USER_CODE:   global.GWAF_USER_CODE,
			Tenant_ID:   tenantID,
			CREATE_TIME: now,
			UPDATE_TIME: now,
		},
		GroupName:   tenantAdminGroupName,
		Remarks:     "租户开通自动创建：租户管理员",
		Permissions: adminPerms,
		Status:      1,
		BuiltIn:     1,
	}
	if err = db.Create(adminGroup).Error; err != nil {
		return model.Tenant{}, err
	}

	// 4. 租户管理员账号。Account 是平台豁免模型（回调不盖章），行归属必须靠
	//    BaseOrm.Tenant_ID 手工设置；不复用 createAccount（它写全局租户戳）。
	//    初始密码强制首登改密（NeedChangePassword=1），与平台默认管理员同口径。
	account := &model.Account{
		BaseOrm: baseorm.BaseOrm{
			Id:          uuid.GenUUID(),
			USER_CODE:   global.GWAF_USER_CODE,
			Tenant_ID:   tenantID,
			CREATE_TIME: now,
			UPDATE_TIME: now,
		},
		LoginAccount:       adminAccount,
		Role:               enums.ROLE_TENANT_ADMIN,
		GroupId:            adminGroup.Id,
		LoginPassword:      hash,
		Status:             0,
		NeedChangePassword: 1,
		PwdUpdateTime:      time.Now().Format(tenantExpireTimeLayout),
		Remarks:            "租户开通自动创建",
	}
	if err = db.Create(account).Error; err != nil {
		return model.Tenant{}, err
	}

	// 新租户码此前若被当成「不存在的租户」缓存过负结果，开通后立即清掉
	receiver.invalidateTenantPolicyCache(tenantID)
	return *bean, nil
}

// resolveTenantQuota 开通租户的配额/宽限期取值：显式传值（含 0=不限）优先，
// 未传（nil）回落套餐默认值。负数整单拒绝。
func resolveTenantQuotas(ctx context.Context, maxHosts, maxAccounts, graceDays *int, planCode string) (int, int, int, error) {
	resolve := func(v *int, planVal int, negMsg string) (int, error) {
		if v != nil {
			if *v < 0 {
				return 0, errors.New(negMsg)
			}
			return *v, nil
		}
		return planVal, nil
	}
	var plan model.Plan
	code := strings.TrimSpace(planCode)
	if code != "" {
		plan = WafPlanServiceApp.GetByCodeApi(wafctx.PlatformScope(ctx), code)
		if plan.Id == "" {
			return 0, 0, 0, errors.New("套餐不存在: " + code)
		}
		if plan.Status != 1 {
			return 0, 0, 0, errors.New("套餐已停用: " + code)
		}
	}
	h, err := resolve(maxHosts, plan.MaxHosts, "站点配额不能为负数")
	if err != nil {
		return 0, 0, 0, err
	}
	a, err := resolve(maxAccounts, plan.MaxAccounts, "账号配额不能为负数")
	if err != nil {
		return 0, 0, 0, err
	}
	g, err := resolve(graceDays, plan.GraceDays, "宽限天数不能为负数")
	if err != nil {
		return 0, 0, 0, err
	}
	return h, a, g, nil
}

// syncTenantAdminGroupPermissions 重写指定租户「租户管理员」BuiltIn 组的授权集并
// 失效组缓存（RBAC 立即按新权限判定；组 ID 不变，令牌无需重签）。
// db 必须是平台作用域（跨租户写），供开通/换套餐/套餐编辑传播共用。
func syncTenantAdminGroupPermissions(db *gorm.DB, tenantID, permissions string) error {
	var group model.PermissionGroup
	if err := db.Model(&model.PermissionGroup{}).
		Where("tenant_id = ? and user_code = ? and group_name = ? and built_in = 1",
			tenantID, global.GWAF_USER_CODE, tenantAdminGroupName).
		First(&group).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 租户行存在但管理员组缺失（异常数据）：跳过同步不阻断，天花板仍会兜底
			zlog.Warn("租户管理员组缺失，跳过权限同步", "tenant", tenantID)
			return nil
		}
		return err
	}
	if group.Permissions == permissions {
		return nil
	}
	if err := db.Model(&model.PermissionGroup{}).Where("id = ?", group.Id).Updates(map[string]interface{}{
		"permissions": permissions,
		"UPDATE_TIME": customtype.JsonTime(time.Now()),
	}).Error; err != nil {
		return err
	}
	global.GCACHE_WAFCACHE.Remove(enums.CACHE_PERMISSION_GROUP + group.Id)
	return nil
}

// RenewTenantApi 续期：ExpireTime（绝对时间）与 AddDays（顺延天数）二选一，都给了用绝对时间。
// 顺延口径：未到期从当前到期时间顺延，已到期从当前时间起算（否则给已过期租户 +30 天仍是过期）。
// 续期成功后状态恢复 Active 并失效策略缓存。
func (receiver *WafTenantService) RenewTenantApi(ctx context.Context, req request.WafTenantRenewReq) (model.Tenant, error) {
	tenantID := strings.TrimSpace(req.TenantID)
	if tenantID == "" {
		return model.Tenant{}, errors.New("租户码不能为空")
	}
	db := global.GWAF_LOCAL_DB.WithContext(wafctx.PlatformScope(ctx))
	var bean model.Tenant
	if err := db.Where("tenant_id = ?", tenantID).First(&bean).Error; err != nil {
		return model.Tenant{}, errors.New("租户不存在")
	}

	var newExpire string
	if abs := strings.TrimSpace(req.ExpireTime); abs != "" {
		t, err := time.ParseInLocation(tenantExpireTimeLayout, abs, time.Local)
		if err != nil {
			return model.Tenant{}, errors.New("到期时间格式应为 2006-01-02 15:04:05")
		}
		newExpire = t.Format(tenantExpireTimeLayout)
	} else if req.AddDays > 0 {
		base := time.Now()
		if cur, perr := time.ParseInLocation(tenantExpireTimeLayout, bean.ExpireTime, time.Local); perr == nil && cur.After(base) {
			base = cur
		}
		newExpire = base.AddDate(0, 0, req.AddDays).Format(tenantExpireTimeLayout)
	} else {
		return model.Tenant{}, errors.New("请提供到期时间(expire_time)或顺延天数(add_days)")
	}

	if err := db.Model(&model.Tenant{}).Where("id = ?", bean.Id).Updates(map[string]interface{}{
		"expire_time": newExpire,
		"status":      enums.TenantStatusActive,
		"UPDATE_TIME": customtype.JsonTime(time.Now()),
	}).Error; err != nil {
		return model.Tenant{}, err
	}
	receiver.invalidateTenantPolicyCache(tenantID)
	bean.ExpireTime = newExpire
	bean.Status = enums.TenantStatusActive
	return bean, nil
}

// EditTenantApi 编辑租户：租户名/套餐/配额/宽限期/状态/到期时间可改，失效策略缓存。
// Status/ExpireTime/PlanCode 的 nil 表示不修改；ExpireTime 指向空串 = 永不过期，
// PlanCode 指向空串 = 显式清除套餐。
//
// 换套餐（plan_code 实际变化）时：新套餐必须存在且启用；租户管理员组授权集重写为新套餐
// 权限集（清除套餐则回落默认全集）；配额/宽限期未显式传值（nil）时重置为新套餐
// 默认值（清除套餐重置为 0=不限），显式传值（含 0）优先；天花板缓存失效立即生效。
func (receiver *WafTenantService) EditTenantApi(ctx context.Context, req request.WafTenantEditReq) (model.Tenant, error) {
	tenantID := strings.TrimSpace(req.TenantID)
	if tenantID == "" {
		return model.Tenant{}, errors.New("租户码不能为空")
	}
	tenantName := strings.TrimSpace(req.TenantName)
	if tenantName == "" {
		return model.Tenant{}, errors.New("租户名称不能为空")
	}
	if req.MaxHosts != nil && *req.MaxHosts < 0 {
		return model.Tenant{}, errors.New("站点配额不能为负数")
	}
	if req.MaxAccounts != nil && *req.MaxAccounts < 0 {
		return model.Tenant{}, errors.New("账号配额不能为负数")
	}
	if req.GraceDays != nil && *req.GraceDays < 0 {
		return model.Tenant{}, errors.New("宽限天数不能为负数")
	}
	db := global.GWAF_LOCAL_DB.WithContext(wafctx.PlatformScope(ctx))
	var bean model.Tenant
	if err := db.Where("tenant_id = ?", tenantID).First(&bean).Error; err != nil {
		return model.Tenant{}, errors.New("租户不存在")
	}

	// plan_code 指针语义：nil=保持现有套餐。清除套餐会把管理员组回落默认授权集
	// 并解除天花板，缺省值绝不能当空串处理（否则只改状态的部分提交等于给租户提权）。
	planChanged := false
	newPlanCode := bean.PlanCode
	var newPlan model.Plan
	if req.PlanCode != nil {
		newPlanCode = strings.TrimSpace(*req.PlanCode)
		planChanged = newPlanCode != bean.PlanCode
		if planChanged && newPlanCode != "" {
			newPlan = WafPlanServiceApp.GetByCodeApi(wafctx.PlatformScope(ctx), newPlanCode)
			if newPlan.Id == "" {
				return model.Tenant{}, errors.New("套餐不存在: " + newPlanCode)
			}
			if newPlan.Status != 1 {
				return model.Tenant{}, errors.New("套餐已停用: " + newPlanCode)
			}
		}
	}

	updates := map[string]interface{}{
		"tenant_name": tenantName,
		"remarks":     strings.TrimSpace(req.Remarks),
		"UPDATE_TIME": customtype.JsonTime(time.Now()),
	}
	if req.PlanCode != nil {
		updates["plan_code"] = newPlanCode
	}
	if req.MaxHosts != nil {
		updates["max_hosts"] = *req.MaxHosts
	} else if planChanged {
		updates["max_hosts"] = newPlan.MaxHosts
	}
	if req.MaxAccounts != nil {
		updates["max_accounts"] = *req.MaxAccounts
	} else if planChanged {
		updates["max_accounts"] = newPlan.MaxAccounts
	}
	if req.GraceDays != nil {
		updates["grace_days"] = *req.GraceDays
	} else if planChanged {
		updates["grace_days"] = newPlan.GraceDays
	}
	if req.Status != nil {
		switch *req.Status {
		case enums.TenantStatusActive, enums.TenantStatusSuspended, enums.TenantStatusExpired, enums.TenantStatusCancelled:
			updates["status"] = *req.Status
		default:
			return model.Tenant{}, errors.New("非法的租户状态")
		}
	}
	if req.ExpireTime != nil {
		v := strings.TrimSpace(*req.ExpireTime)
		if v != "" {
			if _, err := time.ParseInLocation(tenantExpireTimeLayout, v, time.Local); err != nil {
				return model.Tenant{}, errors.New("到期时间格式应为 2006-01-02 15:04:05")
			}
		}
		updates["expire_time"] = v // 空串=永久
	}
	if err := db.Model(&model.Tenant{}).Where("id = ?", bean.Id).Updates(updates).Error; err != nil {
		return model.Tenant{}, err
	}
	receiver.invalidateTenantPolicyCache(tenantID)

	// 换套餐：同步租户管理员组授权集（新套餐集；清除套餐回落默认全集）+ 天花板失效
	if planChanged {
		adminPerms := strings.Join(tenantAdminPermissionCodes, ",")
		if newPlanCode != "" {
			adminPerms = newPlan.Permissions
		}
		if err := syncTenantAdminGroupPermissions(db, tenantID, adminPerms); err != nil {
			return model.Tenant{}, errors.New("套餐已切换，但同步租户管理员权限组失败: " + err.Error())
		}
		global.GCACHE_WAFCACHE.Remove(enums.CACHE_TENANT_PERM_CEILING + tenantID)
	}

	var fresh model.Tenant
	db.Where("id = ?", bean.Id).First(&fresh)
	return fresh, nil
}

// DelTenantApi 注销租户：软删除（Status=Cancelled，保留数据），失效策略缓存。
// 禁止注销平台自身租户（global.GWAF_TENANT_ID）。
func (receiver *WafTenantService) DelTenantApi(ctx context.Context, req request.WafTenantDelReq) error {
	tenantID := strings.TrimSpace(req.TenantID)
	if tenantID == "" {
		return errors.New("租户码不能为空")
	}
	if tenantID == global.GWAF_TENANT_ID {
		return errors.New("不能注销平台自身租户")
	}
	db := global.GWAF_LOCAL_DB.WithContext(wafctx.PlatformScope(ctx))
	var bean model.Tenant
	if err := db.Where("tenant_id = ?", tenantID).First(&bean).Error; err != nil {
		return errors.New("租户不存在")
	}
	if err := db.Model(&model.Tenant{}).Where("id = ?", bean.Id).Updates(map[string]interface{}{
		"status":      enums.TenantStatusCancelled,
		"UPDATE_TIME": customtype.JsonTime(time.Now()),
	}).Error; err != nil {
		return err
	}
	receiver.invalidateTenantPolicyCache(tenantID)
	return nil
}

// InitDefaultTenant 启动期幂等播种平台自身租户行（名称「平台」、不限配额、永不过期）。
//
// 无 ctx：启动初始化不属于任何 HTTP 请求，tenants 表本身平台豁免，
// BaseOrm 手工赋值原样落库；幂等重跑安全。
func (receiver *WafTenantService) InitDefaultTenant() error {
	if global.GWAF_LOCAL_DB == nil {
		return errors.New("本地数据库未初始化")
	}
	db := global.GWAF_LOCAL_DB.WithContext(wafctx.PlatformScope(context.Background()))
	var cnt int64
	if err := db.Model(&model.Tenant{}).Where("tenant_id = ?", global.GWAF_TENANT_ID).Count(&cnt).Error; err != nil {
		return err
	}
	if cnt > 0 {
		return nil //幂等：平台租户行已存在跳过
	}
	now := customtype.JsonTime(time.Now())
	bean := &model.Tenant{
		BaseOrm: baseorm.BaseOrm{
			Id:          uuid.GenUUID(),
			USER_CODE:   global.GWAF_USER_CODE,
			Tenant_ID:   global.GWAF_TENANT_ID,
			CREATE_TIME: now,
			UPDATE_TIME: now,
		},
		TenantID:   global.GWAF_TENANT_ID,
		TenantName: "平台",
		Status:     enums.TenantStatusActive,
	}
	return db.Create(bean).Error
}

// getTenantByCode 按租户码读租户行。ctx 决定读取语义：平台视角传 PlatformScope。
func (receiver *WafTenantService) getTenantByCode(ctx context.Context, tenantID string) (model.Tenant, error) {
	var bean model.Tenant
	err := global.GWAF_LOCAL_DB.WithContext(ctx).Where("tenant_id = ?", tenantID).First(&bean).Error
	return bean, err
}

// evaluateTenantPolicy 按租户快照 + 当前时间计算请求期策略。
func evaluateTenantPolicy(status int, expireTime string, graceDays int) (deny bool, readOnly bool) {
	switch status {
	case enums.TenantStatusSuspended, enums.TenantStatusCancelled:
		return true, false
	case enums.TenantStatusExpired:
		if tenantExpiredBeyondGrace(expireTime, graceDays, time.Now()) {
			return true, false
		}
		return false, true
	default:
		return false, false
	}
}

// tenantExpiredBeyondGrace 到期租户是否已超出宽限期（deadline = ExpireTime + GraceDays 天）。
// ExpireTime 解析失败（脏数据）按超出宽限处理：停用语义宁严勿松，续期会重写 ExpireTime 恢复正常。
func tenantExpiredBeyondGrace(expireTime string, graceDays int, now time.Time) bool {
	expire, err := time.ParseInLocation(tenantExpireTimeLayout, expireTime, time.Local)
	if err != nil {
		return true
	}
	return now.After(expire.AddDate(0, 0, graceDays))
}

// invalidateTenantPolicyCache 租户状态/到期时间变化后失效策略缓存，让请求期立即按新策略判定。
func (receiver *WafTenantService) invalidateTenantPolicyCache(tenantID string) {
	global.GCACHE_WAFCACHE.Remove(enums.CACHE_TENANT_STATUS + tenantID)
}

// applyTenantFilters 列表筛选条件：租户名模糊 + 状态精确。
func applyTenantFilters(db *gorm.DB, req request.WafTenantSearchReq) *gorm.DB {
	if req.TenantName != "" {
		db = db.Where("tenant_name like ?", "%"+req.TenantName+"%")
	}
	if req.Status != nil {
		db = db.Where("status = ?", *req.Status)
	}
	return db
}
