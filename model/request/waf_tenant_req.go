package request

import "SamWaf/model/common/request"

// ---------- 租户生命周期 ----------

// WafTenantSearchReq 分页查询租户（平台视角全量，tenants 表平台豁免、无租户过滤）。
type WafTenantSearchReq struct {
	TenantName string `json:"tenant_name"` //租户名模糊筛选
	Status     *int   `json:"status"`      //状态筛选（enums.TenantStatus*，nil=全部）
	request.PageInfo
}

// WafTenantDetailReq 租户详情（按记录ID）。
type WafTenantDetailReq struct {
	Id string `json:"id" form:"id"`
}

// WafTenantAddReq 开通租户（provisioning）：建租户行 + 复制预置权限组 + 建租户管理员账号。
// plan_code 非空时：管理员组授权集=套餐权限集，配额/宽限期未传（nil）取套餐默认值。
type WafTenantAddReq struct {
	TenantID    string `json:"tenant_id" binding:"required"`   //租户码，^[a-z0-9_-]{3,32}$，全局唯一
	TenantName  string `json:"tenant_name" binding:"required"` //租户名称
	PlanCode    string `json:"plan_code"`                      //套餐码（须存在且启用）
	MaxHosts    *int   `json:"max_hosts"`                      //站点数配额，nil=取套餐默认（无套餐则0=不限），显式0=不限
	MaxAccounts *int   `json:"max_accounts"`                   //账号数配额，nil=取套餐默认（无套餐则0=不限），显式0=不限
	ExpireTime  string `json:"expire_time"`                    //到期时间 2006-01-02 15:04:05，空=永久
	GraceDays   *int   `json:"grace_days"`                     //到期后宽限天数，nil=取套餐默认
	Remarks     string `json:"remarks"`                        //备注
	// 租户管理员初始账号（开通时自动创建，Role=tenantAdmin，绑定"租户管理员"权限组）
	AdminAccount  string `json:"admin_account" binding:"required"`
	AdminPassword string `json:"admin_password" binding:"required"`
}

// WafTenantRenewReq 续期：ExpireTime（绝对时间）与 AddDays（顺延天数）二选一，
// 都传时用绝对时间。续期成功后状态恢复 Active。
type WafTenantRenewReq struct {
	TenantID   string `json:"tenant_id" binding:"required"`
	ExpireTime string `json:"expire_time"` //绝对到期时间，优先于 AddDays
	AddDays    int    `json:"add_days"`    //顺延天数（未到期从当前到期时间顺延，已到期从当前时间起算）
}

// WafTenantEditReq 编辑租户：租户名/套餐/配额/宽限期/状态/到期时间可改。
// 指针字段 nil=不修改（避免部分提交把配额清成 0=不限）；ExpireTime 指向空串=永久。
type WafTenantEditReq struct {
	TenantID    string  `json:"tenant_id" binding:"required"`
	TenantName  string  `json:"tenant_name" binding:"required"`
	PlanCode    string  `json:"plan_code"`
	MaxHosts    *int    `json:"max_hosts"`
	MaxAccounts *int    `json:"max_accounts"`
	GraceDays   *int    `json:"grace_days"`
	Status      *int    `json:"status"`      //enums.TenantStatus*，nil=不修改
	ExpireTime  *string `json:"expire_time"` //nil=不修改；空串=永久
	Remarks     string  `json:"remarks"`
}

// WafTenantDelReq 注销租户（软删除：Status=Cancelled，保留数据）。
type WafTenantDelReq struct {
	TenantID string `json:"tenant_id" form:"tenant_id"`
}
