package waf_service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
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

	"gorm.io/gorm"
)

type WafPlanService struct{}

var WafPlanServiceApp = new(WafPlanService)

// planCodePattern 套餐码规则：与租户码同口径，小写字母/数字/下划线/中划线，3-32 位
var planCodePattern = regexp.MustCompile(`^[a-z0-9_-]{3,32}$`)

// GetListApi 分页查询套餐（plans 表平台豁免，平台视角全量），plan_name 模糊 + status 筛选。
func (receiver *WafPlanService) GetListApi(ctx context.Context, req request.WafPlanSearchReq) ([]model.Plan, int64, error) {
	var list []model.Plan
	var total int64 = 0

	db := global.GWAF_LOCAL_DB.WithContext(ctx).Model(&model.Plan{})
	db = applyPlanFilters(db, req)
	if err := db.Order("CREATE_TIME asc").
		Limit(req.PageSize).Offset(req.PageSize * (req.PageIndex - 1)).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	cntDb := global.GWAF_LOCAL_DB.WithContext(ctx).Model(&model.Plan{})
	cntDb = applyPlanFilters(cntDb, req)
	if err := cntDb.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// GetDetailByIdApi 按记录ID查套餐，未命中返回零值。
func (receiver *WafPlanService) GetDetailByIdApi(ctx context.Context, id string) model.Plan {
	var bean model.Plan
	global.GWAF_LOCAL_DB.WithContext(ctx).Where("id = ?", id).Find(&bean)
	return bean
}

// GetByCodeApi 按套餐码查套餐（租户开通/天花板判定用），未命中返回零值。
func (receiver *WafPlanService) GetByCodeApi(ctx context.Context, planCode string) model.Plan {
	var bean model.Plan
	if planCode == "" {
		return bean
	}
	global.GWAF_LOCAL_DB.WithContext(ctx).Where("plan_code = ?", planCode).Find(&bean)
	return bean
}

// AddApi 新增套餐：套餐码规则/唯一性 + 权限码逐个校验（须 ⊆ 租户可分配集，fail-closed）。
func (receiver *WafPlanService) AddApi(ctx context.Context, req request.WafPlanAddReq) (model.Plan, error) {
	planCode := strings.TrimSpace(req.PlanCode)
	if !planCodePattern.MatchString(planCode) {
		return model.Plan{}, errors.New("套餐码只允许小写字母/数字/下划线/中划线，长度3-32")
	}
	planName := strings.TrimSpace(req.PlanName)
	if planName == "" {
		return model.Plan{}, errors.New("套餐名称不能为空")
	}
	permissions, err := parseTenantAssignableCodes(req.Permissions)
	if err != nil {
		return model.Plan{}, err
	}
	if req.MaxHosts < 0 || req.MaxAccounts < 0 {
		return model.Plan{}, errors.New("配额不能为负数")
	}
	if req.GraceDays < 0 {
		return model.Plan{}, errors.New("宽限天数不能为负数")
	}
	status, err := normalizePlanStatus(req.Status)
	if err != nil {
		return model.Plan{}, err
	}

	db := global.GWAF_LOCAL_DB.WithContext(wafctx.PlatformScope(ctx))
	var cnt int64
	if err = db.Model(&model.Plan{}).Where("plan_code = ?", planCode).Count(&cnt).Error; err != nil {
		return model.Plan{}, err
	}
	if cnt > 0 {
		return model.Plan{}, errors.New("套餐码已存在: " + planCode)
	}

	now := customtype.JsonTime(time.Now())
	bean := &model.Plan{
		BaseOrm: baseorm.BaseOrm{
			Id:          uuid.GenUUID(),
			USER_CODE:   global.GWAF_USER_CODE,
			Tenant_ID:   global.GWAF_TENANT_ID,
			CREATE_TIME: now,
			UPDATE_TIME: now,
		},
		PlanCode:    planCode,
		PlanName:    planName,
		Status:      status,
		Permissions: permissions,
		MaxHosts:    req.MaxHosts,
		MaxAccounts: req.MaxAccounts,
		GraceDays:   req.GraceDays,
		Remarks:     strings.TrimSpace(req.Remarks),
	}
	if err = db.Create(bean).Error; err != nil {
		return model.Plan{}, err
	}
	return *bean, nil
}

// ModifyApi 编辑套餐：套餐码不可改；permissions 变化时同步刷新引用该套餐的全部
// 租户的管理员权限组（并失效组缓存与天花板缓存），配额/宽限期变化不回写存量租户。
func (receiver *WafPlanService) ModifyApi(ctx context.Context, req request.WafPlanEditReq) (model.Plan, error) {
	db := global.GWAF_LOCAL_DB.WithContext(wafctx.PlatformScope(ctx))
	var bean model.Plan
	if err := db.Where("id = ?", req.Id).First(&bean).Error; err != nil {
		return model.Plan{}, errors.New("套餐不存在")
	}
	planName := strings.TrimSpace(req.PlanName)
	if planName == "" {
		return model.Plan{}, errors.New("套餐名称不能为空")
	}
	permissions, err := parseTenantAssignableCodes(req.Permissions)
	if err != nil {
		return model.Plan{}, err
	}
	if req.MaxHosts != nil && *req.MaxHosts < 0 {
		return model.Plan{}, errors.New("站点配额不能为负数")
	}
	if req.MaxAccounts != nil && *req.MaxAccounts < 0 {
		return model.Plan{}, errors.New("账号配额不能为负数")
	}
	if req.GraceDays != nil && *req.GraceDays < 0 {
		return model.Plan{}, errors.New("宽限天数不能为负数")
	}
	status, err := normalizePlanStatus(req.Status)
	if err != nil {
		return model.Plan{}, err
	}

	updates := map[string]interface{}{
		"plan_name":   planName,
		"permissions": permissions,
		"remarks":     strings.TrimSpace(req.Remarks),
		"UPDATE_TIME": customtype.JsonTime(time.Now()),
	}
	if req.MaxHosts != nil {
		updates["max_hosts"] = *req.MaxHosts
	}
	if req.MaxAccounts != nil {
		updates["max_accounts"] = *req.MaxAccounts
	}
	if req.GraceDays != nil {
		updates["grace_days"] = *req.GraceDays
	}
	if req.Status != nil {
		updates["status"] = status
	}
	if err = db.Model(&model.Plan{}).Where("id = ?", req.Id).Updates(updates).Error; err != nil {
		return model.Plan{}, err
	}

	// 权限集变化 → 同步引用该套餐的租户（管理员组重写 + 两类缓存失效）
	if permissions != bean.Permissions {
		if syncErr := receiver.propagatePlanPermissions(ctx, bean.PlanCode, permissions); syncErr != nil {
			// 同步失败不回滚套餐本身（平台可重试保存），但要如实报错让平台管理员知晓
			zlog.Error("套餐权限集同步到租户失败", "plan_code", bean.PlanCode, "error", syncErr.Error())
			return model.Plan{}, errors.New("套餐已保存，但同步到存量租户失败，请重试编辑: " + syncErr.Error())
		}
	}

	var fresh model.Plan
	db.Where("id = ?", req.Id).First(&fresh)
	return fresh, nil
}

// DelApi 删除套餐：仍有租户引用（tenants.plan_code 命中）时拒绝，提示先改租户套餐。
func (receiver *WafPlanService) DelApi(ctx context.Context, req request.WafPlanDelReq) error {
	db := global.GWAF_LOCAL_DB.WithContext(wafctx.PlatformScope(ctx))
	var bean model.Plan
	if err := db.Where("id = ?", req.Id).First(&bean).Error; err != nil {
		return errors.New("套餐不存在")
	}
	var refCnt int64
	if err := db.Model(&model.Tenant{}).Where("plan_code = ?", bean.PlanCode).Count(&refCnt).Error; err != nil {
		return err
	}
	if refCnt > 0 {
		return errors.New("该套餐仍有 " + strconv.FormatInt(refCnt, 10) + " 个租户在用，请先为这些租户更换套餐")
	}
	if err := db.Where("id = ?", req.Id).Delete(&model.Plan{}).Error; err != nil {
		return err
	}
	return nil
}

// InitDefaultPlans 启动期幂等播种预置套餐（按套餐码查重跳过）。
// 无 ctx：启动初始化不属于任何 HTTP 请求；plans 表平台豁免，BaseOrm 手工赋值原样落库。
// 预置套餐只做「不存在则补种」：平台管理员后续的编辑/删除不被覆盖。
func (receiver *WafPlanService) InitDefaultPlans() error {
	if global.GWAF_LOCAL_DB == nil {
		return errors.New("本地数据库未初始化")
	}
	db := global.GWAF_LOCAL_DB.WithContext(wafctx.PlatformScope(context.Background()))
	defaults := []struct {
		code        string
		name        string
		permissions string
		maxHosts    int
		maxAccounts int
		remarks     string
	}{
		{
			code: "free", name: "体验版",
			permissions: joinCodes(enums.PERM_DASHBOARD, enums.PERM_HOST_MANAGE, enums.PERM_LOG_VIEW),
			maxHosts:    3, maxAccounts: 2,
			remarks: "预置套餐：基础查看与建站，小配额体验",
		},
		{
			code: "basic", name: "基础版",
			permissions: joinCodes(enums.PERM_DASHBOARD, enums.PERM_HOST_MANAGE, enums.PERM_LOG_VIEW, enums.PERM_SECURITY, enums.PERM_AUDIT),
			maxHosts:    10, maxAccounts: 5,
			remarks: "预置套餐：含防护策略与审计日志",
		},
		{
			code: "pro", name: "专业版",
			permissions: joinCodes(enums.PERM_DASHBOARD, enums.PERM_HOST_MANAGE, enums.PERM_LOG_VIEW, enums.PERM_SECURITY, enums.PERM_AUDIT, enums.PERM_SYSTEM_OPS, enums.PERM_ACCOUNT_MANAGE),
			maxHosts:    50, maxAccounts: 20,
			remarks: "预置套餐：租户侧权限全集，可自助管理账号与运维配置",
		},
		{
			code: "enterprise", name: "旗舰版",
			permissions: strings.Join(enums.TenantAssignableCodes(), ","),
			remarks:     "预置套餐：租户可分配权限全集，配额不限",
		},
	}
	var errs []error
	for _, d := range defaults {
		var cnt int64
		if err := db.Model(&model.Plan{}).Where("plan_code = ?", d.code).Count(&cnt).Error; err != nil {
			// 单个套餐失败不阻断其余套餐：否则首条写失败会让套餐列表整体为空，
			// 平台管理员在页面上看到「没有任何套餐」却不知道是播种出错
			errs = append(errs, fmt.Errorf("检查预置套餐 %s 失败: %w", d.code, err))
			continue
		}
		if cnt > 0 {
			continue //幂等：已有同码套餐跳过
		}
		now := customtype.JsonTime(time.Now())
		bean := &model.Plan{
			BaseOrm: baseorm.BaseOrm{
				Id:          uuid.GenUUID(),
				USER_CODE:   global.GWAF_USER_CODE,
				Tenant_ID:   global.GWAF_TENANT_ID,
				CREATE_TIME: now,
				UPDATE_TIME: now,
			},
			PlanCode:    d.code,
			PlanName:    d.name,
			Status:      1,
			Permissions: d.permissions,
			MaxHosts:    d.maxHosts,
			MaxAccounts: d.maxAccounts,
			Remarks:     d.remarks,
		}
		if err := db.Create(bean).Error; err != nil {
			errs = append(errs, fmt.Errorf("播种预置套餐 %s 失败: %w", d.code, err))
		}
	}
	return errors.Join(errs...)
}

// propagatePlanPermissions 把套餐权限集同步到引用该套餐的全部租户：
// 重写各租户「租户管理员」BuiltIn 组的 permissions、失效组缓存与天花板缓存。
// PlatformScope 读写（跨租户的平台维护语义）。
func (receiver *WafPlanService) propagatePlanPermissions(ctx context.Context, planCode, permissions string) error {
	db := global.GWAF_LOCAL_DB.WithContext(wafctx.PlatformScope(ctx))
	var tenants []model.Tenant
	if err := db.Model(&model.Tenant{}).Where("plan_code = ?", planCode).Find(&tenants).Error; err != nil {
		return err
	}
	for _, t := range tenants {
		if t.TenantID == "" {
			continue
		}
		if err := syncTenantAdminGroupPermissions(db, t.TenantID, permissions); err != nil {
			return err
		}
		global.GCACHE_WAFCACHE.Remove(enums.CACHE_TENANT_PERM_CEILING + t.TenantID)
	}
	return nil
}

// parseTenantAssignableCodes 拆分校验套餐权限码：全部须在租户可分配集内
// （platform:admin / permission:manage / system:config 等平台码整单拒绝），
// 去空白去重，返回规范化的逗号串。
func parseTenantAssignableCodes(raw string) (string, error) {
	codes := make([]string, 0, len(enums.AllPermissionCodes))
	seen := make(map[string]struct{}, len(enums.AllPermissionCodes))
	for _, part := range strings.Split(raw, ",") {
		code := strings.TrimSpace(part)
		if code == "" {
			continue
		}
		if !enums.IsValidPermissionCode(code) {
			return "", errors.New("非法权限码: " + code)
		}
		if !enums.IsTenantAssignableCode(code) {
			return "", errors.New("套餐不可包含平台侧权限码: " + code)
		}
		if _, dup := seen[code]; dup {
			continue
		}
		seen[code] = struct{}{}
		codes = append(codes, code)
	}
	if len(codes) == 0 {
		return "", errors.New("请至少选择一个权限码")
	}
	return strings.Join(codes, ","), nil
}

// normalizePlanStatus 状态只允许 0(停用)/1(启用)，缺省启用。
func normalizePlanStatus(status *int) (int, error) {
	if status == nil {
		return 1, nil
	}
	if *status != 0 && *status != 1 {
		return 0, errors.New("状态只允许 0(停用) 或 1(启用)")
	}
	return *status, nil
}

// applyPlanFilters 列表筛选条件：套餐名模糊 + 状态精确。
func applyPlanFilters(db *gorm.DB, req request.WafPlanSearchReq) *gorm.DB {
	if req.PlanName != "" {
		db = db.Where("plan_name like ?", "%"+req.PlanName+"%")
	}
	if req.Status != nil {
		db = db.Where("status = ?", *req.Status)
	}
	return db
}

// joinCodes 便捷拼接权限码串
func joinCodes(codes ...string) string {
	return strings.Join(codes, ",")
}
