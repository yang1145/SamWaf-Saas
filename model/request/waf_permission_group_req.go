package request

import "SamWaf/model/common/request"

// ---------- 权限组 ----------

// WafPermissionGroupAddReq 新增权限组。
// Permissions 是 enums.AllPermissionCodes 里权限码的英文逗号分隔列表，
// 由后端逐码校验（非法码整单拒绝，fail-closed），不信任前端拼串。
type WafPermissionGroupAddReq struct {
	GroupName   string `json:"group_name" binding:"required"` //组名，租户内唯一
	Remarks     string `json:"remarks"`                       //备注
	Permissions string `json:"permissions"`                   //权限码，英文逗号分隔
	Status      *int   `json:"status"`                        //1启用 0禁用；缺省启用
}

type WafPermissionGroupEditReq struct {
	Id          string `json:"id" binding:"required"`
	GroupName   string `json:"group_name" binding:"required"` //系统预置组(BuiltIn=1)不允许改组名，其余字段可改
	Remarks     string `json:"remarks"`
	Permissions string `json:"permissions"`
	Status      *int   `json:"status"`
}

type WafPermissionGroupDelReq struct {
	Id string `json:"id" form:"id"`
}

type WafPermissionGroupDetailReq struct {
	Id string `json:"id" form:"id"`
}

type WafPermissionGroupSearchReq struct {
	GroupName string `json:"group_name"` //组名模糊筛选
	request.PageInfo
}
