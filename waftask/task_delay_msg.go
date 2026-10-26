package waftask

import (
	"SamWaf/common/uuid"
	"SamWaf/common/wsmsg"
	"SamWaf/common/zlog"
	"SamWaf/global"
	"SamWaf/model"
	"SamWaf/service/waf_service"
	"time"

	"github.com/gorilla/websocket"
)

// TaskDelayInfo 定时发送延迟信息
func TaskDelayInfo() {
	zlog.Debug("TaskDelayInfo")
	models, count, err := waf_service.WafDelayMsgServiceApp.GetAllList()
	if err == nil {
		if count > 0 {
			for i := 0; i < len(models); i++ {
				msg := models[i]

				cmdType := "Info"
				if msg.DelayType == "升级结果" {
					cmdType = "RELOAD_PAGE"
				}
				dataPacket := model.MsgDataPacket{
					MessageId:           uuid.GenUUID(),
					MessageType:         msg.DelayType,
					MessageData:         msg.DelayContent,
					MessageAttach:       nil,
					MessageDateTime:     time.Now().Format("2006-01-02 15:04:05"),
					MessageUnReadStatus: true,
				}
				//发送websocket：走会话管理器统一出口，内部按连接加锁 + 写超时；
				//报文按连接各自的会话密钥组装（v2 连接一份，旧连接回落 legacy）。
				//按延迟消息行的归属租户路由（fail-closed：平台行只达平台侧连接，
				//租户行只达该租户连接），杜绝全量广播跨租户投递
				tenantID := msg.BaseOrm.Tenant_ID
				if tenantID == global.GWAF_TENANT_ID {
					tenantID = "" // 平台租户归一化：连接注册侧把平台租户记为 ""（平台作用域）
				}
				sendSuccess := global.GWebSocket.BroadcastBuildToTenant(tenantID, websocket.TextMessage, func(keyID string) ([]byte, error) {
					msgBytes, err := wsmsg.Build(keyID, dataPacket, cmdType)
					if err != nil {
						zlog.Debug("组装延迟消息报文错误", err)
					}
					return msgBytes, err
				})

				if sendSuccess > 0 {
					waf_service.WafDelayMsgServiceApp.DelApi(msg.Id)
				}

			}
		}
	}
}
