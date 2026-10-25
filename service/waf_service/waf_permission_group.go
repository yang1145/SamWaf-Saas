package waf_service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"SamWaf/common/uuid"
	"SamWaf/customtype"
	"SamWaf/enums"
	"SamWaf/global"
	"SamWaf/model"
	"SamWaf/model/baseorm"
	"SamWaf/model/request"

	"gorm.io/gorm"
)

type WafPermissionGroupService struct{}

var WafPermissionGroupServiceApp = new(WafPermissionGroupService)

const (
	permGroupNameMaxLen = 50
	// permGroupCacheTTL 组权限缓存的兜底 TTL：RBAC 中间件每个请求都可能读它，
	// 组增/改/删时由服务层主动失效缓存，TTL 只是防「失效遗漏」的最后兜底，别调大。
	permGroupCacheTTL = 5 * time.Minute
)

// permGroupCacheEntry 缓存里的组权限快照。
// 「组不存在/被禁用」也缓存（Found=false）：让禁用组的判定同样不打库；
// 组被重新启用或修改时 ModifyApi 会主动清键，不会留下过期负结果。
type permGroupCacheEntry struct {
	Found       bool   `json:"found"`
	Status      int    `json:"status"`
	Permissions string `json:"permissions"`
}

// GetListApi 分页查询权限组，按组名模糊筛选。
// 租户隔离由 GORM 租户回调（wafdb.before_query）按 ctx/全局回退追加，这里不手工拼租户条件。
func (receiver *WafPermissionGroupService) GetListApi(ctx context.Context, req request.WafPermissionGroupSearchReq) ([]model.PermissionGroup, int64, error) {
	var list []model.PermissionGroup
	var total int64 = 0

	var whereField = ""
	var whereValues []interface{}
	if len(req.GroupName) > 0 {
		whereField = " group_name like ? "
		whereValues = append(whereValues, "%"+req.GroupName+"%")
	}

	db := global.GWAF_LOCAL_DB.WithContext(ctx)
	if err := db.Model(&model.PermissionGroup{}).Where(whereField, whereValues...).
		Order("CREATE_TIME asc").
		Limit(req.PageSize).Offset(req.PageSize * (req.PageIndex - 1)).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	if err := db.Model(&model.PermissionGroup{}).Where(whereField, whereValues...).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// GetDetailByIdApi 按ID查权限组详情，未命中返回零值。
func (receiver *WafPermissionGroupService) GetDetailByIdApi(ctx context.Context, id string) model.PermissionGroup {
	var bean model.PermissionGroup
	global.GWAF_LOCAL_DB.WithContext(ctx).Where("id = ?", id).Find(&bean)
	return bean
}

// AddApi 新增权限组：组名租户内唯一 + 权限码逐个校验（非法码整单拒绝）。
func (receiver *WafPermissionGroupService) AddApi(ctx context.Context, req request.WafPermissionGroupAddReq) (model.PermissionGroup, error) {
	name, err := normalizePermissionGroupName(req.GroupName)
	if err != nil {
		return model.PermissionGroup{}, err
	}
	permissions, err := parsePermissionCodes(req.Permissions)
	if err != nil {
		return model.PermissionGroup{}, err
	}
	status, err := normalizePermissionGroupStatus(req.Status)
	if err != nil {
		return model.PermissionGroup{}, err
	}
	if err = receiver.checkNameFree(ctx, name, ""); err != nil {
		return model.PermissionGroup{}, err
	}

	var bean = &model.PermissionGroup{
		BaseOrm: baseorm.BaseOrm{
			Id:          uuid.GenUUID(),
			USER_CODE:   global.GWAF_USER_CODE,
			Tenant_ID:   global.GWAF_TENANT_ID,
			CREATE_TIME: customtype.JsonTime(time.Now()),
			UPDATE_TIME: customtype.JsonTime(time.Now()),
		},
		GroupName:   name,
		Remarks:     strings.TrimSpace(req.Remarks),
		Permissions: permissions,
		Status:      status,
		BuiltIn:     0,
	}
	// 租户标识以 ctx 为准（before_create 回调会覆盖上面的全局赋值）
	if err = global.GWAF_LOCAL_DB.WithContext(ctx).Create(bean).Error; err != nil {
		return model.PermissionGroup{}, err
	}
	return *bean, nil
}

// ModifyApi 编辑权限组。系统预置组(BuiltIn=1)不允许改组名，其余字段可改；
// 组的授权边界可能变化，改完必须失效缓存，让 RBAC 判定立即用新权限。
func (receiver *WafPermissionGroupService) ModifyApi(ctx context.Context, req request.WafPermissionGroupEditReq) (model.PermissionGroup, error) {
	db := global.GWAF_LOCAL_DB.WithContext(ctx)
	var bean model.PermissionGroup
	if err := db.Where("id = ?", req.Id).First(&bean).Error; err != nil {
		return model.PermissionGroup{}, errors.New("权限组不存在")
	}
	name, err := normalizePermissionGroupName(req.GroupName)
	if err != nil {
		return model.PermissionGroup{}, err
	}
	permissions, err := parsePermissionCodes(req.Permissions)
	if err != nil {
		return model.PermissionGroup{}, err
	}
	status, err := normalizePermissionGroupStatus(req.Status)
	if err != nil {
		return model.PermissionGroup{}, err
	}
	if bean.BuiltIn == 1 && name != bean.GroupName {
		return model.PermissionGroup{}, errors.New("系统预置组不允许修改组名")
	}
	if bean.BuiltIn != 1 {
		if err = receiver.checkNameFree(ctx, name, bean.Id); err != nil {
			return model.PermissionGroup{}, err
		}
	}

	updateMap := map[string]interface{}{
		"remarks":     strings.TrimSpace(req.Remarks),
		"permissions": permissions,
		"status":      status,
		"UPDATE_TIME": customtype.JsonTime(time.Now()),
	}
	if bean.BuiltIn != 1 {
		updateMap["group_name"] = name
	}
	if err = db.Model(&model.PermissionGroup{}).Where("id = ?", req.Id).Updates(updateMap).Error; err != nil {
		return model.PermissionGroup{}, err
	}
	receiver.invalidatePermGroupCache(bean.Id)

	bean.GroupName = name
	bean.Remarks = strings.TrimSpace(req.Remarks)
	bean.Permissions = permissions
	bean.Status = status
	return bean, nil
}

// DelApi 删除权限组：系统预置组拒删；仍有账号绑定（accounts.group_id 引用）拒删，
// 提示先解绑——静默解绑会让管理员以为账号权限没动过，实际已经回落旧角色。
func (receiver *WafPermissionGroupService) DelApi(ctx context.Context, req request.WafPermissionGroupDelReq) error {
	db := global.GWAF_LOCAL_DB.WithContext(ctx)
	var bean model.PermissionGroup
	if err := db.Where("id = ?", req.Id).First(&bean).Error; err != nil {
		return errors.New("权限组不存在")
	}
	if bean.BuiltIn == 1 {
		return errors.New("系统预置组不允许删除")
	}
	var refCount int64
	if err := db.Model(&model.Account{}).Where("group_id = ?", bean.Id).Count(&refCount).Error; err != nil {
		return err
	}
	if refCount > 0 {
		return fmt.Errorf("该权限组已绑定 %d 个账号，请先解绑后再删除", refCount)
	}
	if err := db.Where("id = ?", req.Id).Delete(&model.PermissionGroup{}).Error; err != nil {
		return err
	}
	receiver.invalidatePermGroupCache(bean.Id)
	return nil
}

// HasPermission 判定权限组是否拥有指定权限码——RBAC 中间件(RequirePermission)的依赖点，
// 签名不可随意变更。
//
// 只读 status=1 的组：组不存在或被禁用一律 false；数据库查询失败返回 error，
// 由中间件 fail-closed（拒绝优于放行）。结果带 5 分钟兜底 TTL 缓存，
// 组增/改/删时由服务层主动失效，正常路径下判定即时生效。
func (receiver *WafPermissionGroupService) HasPermission(ctx context.Context, groupId, code string) (bool, error) {
	if groupId == "" {
		return false, errors.New("权限组ID为空")
	}
	key := enums.CACHE_PERMISSION_GROUP + groupId
	var entry permGroupCacheEntry
	if err := global.GCACHE_WAFCACHE.GetAsEx(key, &entry, permGroupCacheTTL); err != nil {
		// 缓存未命中或后端抖动都落数据库（真相源）；数据库失败才向上报错 fail-closed
		var bean model.PermissionGroup
		dbErr := global.GWAF_LOCAL_DB.WithContext(ctx).Where("id = ? AND status = 1", groupId).First(&bean).Error
		if dbErr != nil {
			if errors.Is(dbErr, gorm.ErrRecordNotFound) {
				// 禁用/不存在的组缓存负结果，免得每个请求都打库；
				// 重新启用会走 ModifyApi 清键
				global.GCACHE_WAFCACHE.SetWithTTl(key, permGroupCacheEntry{Found: false}, permGroupCacheTTL)
				return false, nil
			}
			return false, dbErr
		}
		entry = permGroupCacheEntry{Found: true, Status: bean.Status, Permissions: bean.Permissions}
		global.GCACHE_WAFCACHE.SetWithTTl(key, entry, permGroupCacheTTL)
	}
	if !entry.Found {
		return false, nil
	}
	return permissionListContains(entry.Permissions, code), nil
}

// GetOptionsApi 全部启用中的权限组（id+group_name+permissions），供账号编辑的绑定下拉。
func (receiver *WafPermissionGroupService) GetOptionsApi(ctx context.Context) ([]model.PermissionGroup, error) {
	list := make([]model.PermissionGroup, 0)
	err := global.GWAF_LOCAL_DB.WithContext(ctx).Model(&model.PermissionGroup{}).
		Select("id, group_name, permissions").
		Where("status = 1").
		Order("CREATE_TIME asc").
		Find(&list).Error
	return list, err
}

// InitDefaultPermissionGroups 启动期幂等播种三个预置组（BuiltIn=1，按组名查重跳过）。
//
// 无 ctx：启动初始化不属于任何 HTTP 请求，租户回调自然回退全局标识，
// 与迁移注释（wafdb/migrations_core.go 202610250001）的约定一致——
// 全新安装与存量升级都会走到这里，幂等重跑安全。
func (receiver *WafPermissionGroupService) InitDefaultPermissionGroups() error {
	defaults := []struct {
		name        string
		permissions string
		remarks     string
	}{
		{
			name:        "只读巡检",
			permissions: strings.Join([]string{enums.PERM_DASHBOARD, enums.PERM_LOG_VIEW, enums.PERM_AUDIT}, ","),
			remarks:     "系统预置：仅可查看仪表盘、访问日志与审计日志",
		},
		{
			name:        "安全运维",
			permissions: strings.Join([]string{enums.PERM_DASHBOARD, enums.PERM_HOST_MANAGE, enums.PERM_SECURITY, enums.PERM_LOG_VIEW}, ","),
			remarks:     "系统预置：站点与防护策略的日常运维",
		},
		{
			name:        "运维管理",
			permissions: strings.Join([]string{enums.PERM_DASHBOARD, enums.PERM_HOST_MANAGE, enums.PERM_SYSTEM_OPS, enums.PERM_LOG_VIEW}, ","),
			remarks:     "系统预置：站点与系统配置运维",
		},
	}
	for _, d := range defaults {
		var cnt int64
		if err := global.GWAF_LOCAL_DB.Model(&model.PermissionGroup{}).Where("group_name = ?", d.name).Count(&cnt).Error; err != nil {
			return err
		}
		if cnt > 0 {
			continue //幂等：已有同名组跳过
		}
		bean := &model.PermissionGroup{
			BaseOrm: baseorm.BaseOrm{
				Id:          uuid.GenUUID(),
				USER_CODE:   global.GWAF_USER_CODE,
				Tenant_ID:   global.GWAF_TENANT_ID,
				CREATE_TIME: customtype.JsonTime(time.Now()),
				UPDATE_TIME: customtype.JsonTime(time.Now()),
			},
			GroupName:   d.name,
			Remarks:     d.remarks,
			Permissions: d.permissions,
			Status:      1,
			BuiltIn:     1,
		}
		if err := global.GWAF_LOCAL_DB.Create(bean).Error; err != nil {
			return err
		}
	}
	return nil
}

// invalidatePermGroupCache 组增改删后失效 RBAC 缓存，让权限变化立即生效。
func (receiver *WafPermissionGroupService) invalidatePermGroupCache(id string) {
	global.GCACHE_WAFCACHE.Remove(enums.CACHE_PERMISSION_GROUP + id)
}

// checkNameFree 组名租户内唯一。excludeId 非空时排除自身（编辑场景）。
func (receiver *WafPermissionGroupService) checkNameFree(ctx context.Context, name, excludeId string) error {
	var cnt int64
	q := global.GWAF_LOCAL_DB.WithContext(ctx).Model(&model.PermissionGroup{}).
		Where("group_name = ?", name)
	if excludeId != "" {
		q = q.Where("id <> ?", excludeId)
	}
	if err := q.Count(&cnt).Error; err != nil {
		return err
	}
	if cnt > 0 {
		return errors.New("权限组名称已存在: " + name)
	}
	return nil
}

// normalizePermissionGroupName 组名：去首尾空白、非空、限长。
func normalizePermissionGroupName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", errors.New("权限组名称不能为空")
	}
	if len([]rune(name)) > permGroupNameMaxLen {
		return "", errors.New("权限组名称长度不能超过50")
	}
	return name, nil
}

// parsePermissionCodes 拆分逗号分隔的权限码：去空白、去重，任何非法码整单拒绝（fail-closed）。
// 返回规范化的逗号串（供落库与前端回显）。
func parsePermissionCodes(raw string) (string, error) {
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

// normalizePermissionGroupStatus 状态只允许 0(禁用)/1(启用)，缺省启用。
func normalizePermissionGroupStatus(status *int) (int, error) {
	if status == nil {
		return 1, nil
	}
	if *status != 0 && *status != 1 {
		return 0, errors.New("状态只允许 0(禁用) 或 1(启用)")
	}
	return *status, nil
}

// permissionListContains 判断逗号分隔的权限码串是否包含指定码。
func permissionListContains(list, code string) bool {
	for _, part := range strings.Split(list, ",") {
		if strings.TrimSpace(part) == code {
			return true
		}
	}
	return false
}
