package model

import "SamWaf/model/baseorm"

// IPWatchlist 重点 IP 观察名单：名单内 IP 的请求全量留痕（窄行进 access_log、
// 报文进 event_payload kind=watch），到期自动失效。表在 core 库。
// IP 的唯一性是租户内的（多租户下不同租户可观察同一 IP）：唯一索引 uni_watch_ip
// 由迁移 202610260001 重建为 (tenant_id, user_code, ip) 复合索引；模型层只保留
// 普通索引，避免 AutoMigrate 重建出单列全局唯一索引。
type IPWatchlist struct {
	baseorm.BaseOrm
	IP       string `gorm:"size:64;index:idx_watch_ip" json:"ip"`
	ExpireAt int64  `gorm:"index" json:"expire_at"` // 到期时间（Unix 秒）
	Reason   string `gorm:"size:255" json:"reason"` // 加入原因（如触发规则名）
}
