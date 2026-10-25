package request

import "SamWaf/model/common/request"

// ---------- 套餐管理（平台域） ----------

// WafPlanSearchReq 分页查询套餐（plans 表平台豁免、无租户过滤）。
type WafPlanSearchReq struct {
	PlanName string `json:"plan_name"` //套餐名模糊筛选
	Status   *int   `json:"status"`    //状态筛选（1启用 0停用，nil=全部）
	request.PageInfo
}

// WafPlanDetailReq 套餐详情（按记录ID）。
type WafPlanDetailReq struct {
	Id string `json:"id" form:"id"`
}

// WafPlanAddReq 新增套餐。permissions 须 ⊆ 租户可分配权限码集合（fail-closed）。
type WafPlanAddReq struct {
	PlanCode    string `json:"plan_code" binding:"required"` //套餐码 ^[a-z0-9_-]{3,32}$，全局唯一
	PlanName    string `json:"plan_name" binding:"required"` //套餐名称
	Permissions string `json:"permissions" binding:"required"` //权限码逗号分隔（租户可分配集内）
	MaxHosts    int    `json:"max_hosts"`                     //站点数配额默认值，0=不限
	MaxAccounts int    `json:"max_accounts"`                  //账号数配额默认值，0=不限
	GraceDays   int    `json:"grace_days"`                    //到期后宽限天数默认值
	Status      *int   `json:"status"`                        //1启用 0停用，缺省启用
	Remarks     string `json:"remarks"`                       //备注
}

// WafPlanEditReq 编辑套餐：套餐码不可改；permissions 变化会同步到引用该套餐的
// 全部租户的管理员权限组（并失效缓存）；配额/宽限期变化不回写存量租户
// （存量租户的配额是开通时落定的快照，可经租户编辑单独调整）。
type WafPlanEditReq struct {
	Id          string `json:"id" binding:"required"` //记录ID
	PlanName    string `json:"plan_name" binding:"required"`
	Permissions string `json:"permissions" binding:"required"`
	MaxHosts    *int   `json:"max_hosts"`
	MaxAccounts *int   `json:"max_accounts"`
	GraceDays   *int   `json:"grace_days"`
	Status      *int   `json:"status"`
	Remarks     string `json:"remarks"`
}

// WafPlanDelReq 删除套餐：仍有租户引用（plan_code 命中）时拒绝。
type WafPlanDelReq struct {
	Id string `json:"id" form:"id"`
}
