package model

import (
	"SamWaf/model/baseorm"
)

// PermissionGroup 权限组：租户级自定义角色，permissions 是 enums 权限码的逗号分隔列表。
//
// 与三权分立四角色的关系：账号未绑定权限组（Account.GroupId 为空）时走旧角色判定；
// 绑定后完全按权限码判定（superAdmin 恒通过）。组随租户隔离（BaseOrm），
// 未来租户开通流程应为每个新租户复制一份预置组。
type PermissionGroup struct {
	baseorm.BaseOrm
	GroupName   string `gorm:"size:100" json:"group_name"`   //组名，租户内唯一
	Remarks     string `gorm:"size:500" json:"remarks"`      //备注
	Permissions string `gorm:"type:text" json:"permissions"` //权限码，英文逗号分隔（enums.AllPermissionCodes 子集）
	Status      int    `json:"status"`                       //1启用 0禁用
	BuiltIn     int    `json:"built_in"`                     //1系统预置组，不允许删除
}

func (PermissionGroup) TableName() string {
	return "permission_groups"
}
