package model

import (
	"SamWaf/model/baseorm"
)

// Plan 套餐（平台注册表）。行本身不参与租户过滤（tenantExemptModels 平台豁免），
// 由平台侧（TokenOnly platform:admin 域）管理与启动播种。
//
// 套餐是租户差异化的驱动源：开通租户时 plan_code 决定租户管理员权限组的授权集，
// 并给出站点/账号配额与宽限天数的默认值；套餐权限集同时作为请求期的「租户权限
// 天花板」（RequirePermission 强制），即使权限组被改大也无法越出套餐边界。
type Plan struct {
	baseorm.BaseOrm
	PlanCode    string `gorm:"size:64" json:"plan_code"`   //套餐码，全局唯一 ^[a-z0-9_-]{3,32}$
	PlanName    string `gorm:"size:128" json:"plan_name"`  //套餐名称
	Status      int    `json:"status"`                     //1启用 0停用（停用后不可用于新开通，存量租户维持最后授权）
	Permissions string `gorm:"size:1024" json:"permissions"` //该套餐租户可持有的权限码上限（逗号分隔，须 ⊆ 租户可分配集）
	MaxHosts    int    `json:"max_hosts"`                  //站点数配额默认值，0=不限
	MaxAccounts int    `json:"max_accounts"`               //账号数配额默认值，0=不限
	GraceDays   int    `json:"grace_days"`                 //到期后宽限天数默认值
	Remarks     string `gorm:"size:500" json:"remarks"`    //备注
}

func (Plan) TableName() string {
	return "plans"
}
