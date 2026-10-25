package model

import (
	"SamWaf/model/baseorm"
)

// Tenant 租户（平台注册表）。行本身不参与租户过滤（tenantExemptModels 平台豁免），
// 由平台侧（TokenOnly platform:admin 域）与启动任务管理；租户用户的请求不直接查本表。
//
// 生命周期：Add（provisioning：复制预置权限组 + 建租户管理员）→ Active；
// 到期由每日任务推进 Active→Expired；Renew 把 ExpireTime 顺延并恢复 Active；
// Suspended/Cancelled 为人工状态，禁登录禁请求。
type Tenant struct {
	baseorm.BaseOrm
	TenantID    string `gorm:"size:64" json:"tenant_id"`     //租户码，全局唯一（登录/数据行 tenant_id 的归属依据）
	TenantName  string `gorm:"size:128" json:"tenant_name"`  //名称
	Status      int    `json:"status"`                       //1正常 0停用 2已过期 -1已注销（enums.TenantStatus*）
	PlanCode    string `gorm:"size:64" json:"plan_code"`     //套餐码：驱动管理员组授权集、配额/宽限默认值与权限天花板（空=不绑套餐，行为同旧版）
	MaxHosts    int    `json:"max_hosts"`                    //站点数配额，0=不限
	MaxAccounts int    `json:"max_accounts"`                 //账号数配额，0=不限
	ExpireTime  string `gorm:"size:32" json:"expire_time"`   //到期时间 2006-01-02 15:04:05，空=永不过期
	GraceDays   int    `json:"grace_days"`                   //到期后宽限天数（宽限期内登录只读），0=到期即禁登录
	Remarks     string `gorm:"size:500" json:"remarks"`      //备注
}

func (Tenant) TableName() string {
	return "tenants"
}
