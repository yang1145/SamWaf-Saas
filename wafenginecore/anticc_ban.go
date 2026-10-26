package wafenginecore

import (
	"SamWaf/common/wafctx"
	"SamWaf/global"
	"SamWaf/innerbean"
	"SamWaf/model"
	"context"
	"strings"
	"time"
)

// ccBanNotifyThrottle 同一封禁键的提醒推送间隔。
// 被封 IP 会持续重试，逐请求推送会把消息队列刷爆（反向代理环路告警用的是同一套节流思路）。
const ccBanNotifyThrottle = 5 * time.Minute

// matchCCBan 判断该站点的这个客户端当前是否处于 CC 封禁期。
// 依次查全局作用域、本站点作用域，以及升级前写入的旧格式键；命中返回具体的键，便于取时长与做节流。
func matchCCBan(hostCode, netSrcIP, srcIP, ipMode string) (hitKey string, clientIP string) {
	if global.GCACHE_WAFCACHE == nil {
		return "", ""
	}
	clientIP = model.GetClientIPByMode(ipMode, netSrcIP, srcIP)
	if clientIP == "" {
		return "", ""
	}
	keys := append(model.CCBanLookupKeys(hostCode, clientIP), model.LegacyCCBanKey(clientIP))
	for _, key := range keys {
		if global.GCACHE_WAFCACHE.IsKeyExist(key) {
			return key, clientIP
		}
	}
	return "", clientIP
}

func ccBanLog(log *innerbean.WebLog, responseCode int) *innerbean.WebLog {
	recorded := *log
	recorded.SkipPayloadStore = true
	recorded.HEADER = ""
	recorded.BODY = ""
	recorded.RES_BODY = ""
	recorded.COOKIES = ""
	recorded.POST_FORM = ""
	recorded.ResHeader = ""
	recorded.SrcByteBody = nil
	recorded.SrcByteResBody = nil
	recorded.SrcURL = nil
	recorded.BodyDecoded = ""
	recorded.CookiesDecoded = ""
	recorded.BodyValues = nil
	recorded.BodyFields = nil
	recorded.BodyHash = ""
	recorded.RULE = "CC封禁"
	recorded.ACTION = "阻止"
	recorded.STATUS = "阻止访问"
	if responseCode == 444 {
		recorded.STATUS = "连接关闭"
	}
	recorded.STATUS_CODE = responseCode
	recorded.TASK_FLAG = 1
	recorded.RISK_LEVEL = 1
	recorded.GUEST_IDENTIFICATION = "可疑用户"
	recorded.TimeSpent = time.Now().UnixMilli() - recorded.UNIX_ADD_TIME
	return &recorded
}

// ccBanTenantID 从封禁键反查该封禁事件所属租户，供 WebSocket 按租户投递。
// 站点作用域的键带有站点码，可反查站点归属租户；全局作用域（hostCode 为空）查不到归属租户，
// 按失败关闭原则返回空串——消息只会到达平台侧连接，绝不误投给某个租户。
// notifyCCBanOnce 对同一封禁键 5 分钟节流一次，这里的一次轻量查询代价可以接受。
func ccBanTenantID(hitKey string) string {
	_, hostCode, _, ok := model.ParseCCBanKey(hitKey)
	if !ok || hostCode == "" || global.GWAF_LOCAL_DB == nil {
		return ""
	}
	var tenantID string
	// 数据面加载语义（PlatformScope）：站点码本身不含租户信息，需跨租户检索
	if err := global.GWAF_LOCAL_DB.WithContext(wafctx.PlatformScope(context.Background())).
		Model(&model.Hosts{}).Select("tenant_id").Where("code = ?", hostCode).
		Scan(&tenantID).Error; err != nil {
		return ""
	}
	return tenantID
}

// notifyCCBanOnce 推送 CC 封禁提醒，同一封禁键在节流窗口内只推一条。
func notifyCCBanOnce(hitKey, clientIP string, region []string) {
	throttleKey := "cc_ban_notify_" + hitKey
	if global.GCACHE_WAFCACHE.IsKeyExist(throttleKey) {
		return
	}
	global.GCACHE_WAFCACHE.SetWithTTl(throttleKey, "1", ccBanNotifyThrottle)

	serverName := global.GWAF_CUSTOM_SERVER_NAME
	if serverName == "" {
		serverName = "未命名服务器"
	}
	banDuration, _ := global.GCACHE_WAFCACHE.GetInt(hitKey)
	remainingSeconds := 0
	if expireTime, err := global.GCACHE_WAFCACHE.GetExpireTime(hitKey); err == nil {
		if r := int(time.Until(expireTime).Seconds()); r > 0 {
			remainingSeconds = r
		}
	}
	global.GQEQUE_MESSAGE_DB.Enqueue(innerbean.IPBanMessageInfo{
		BaseMessageInfo: innerbean.BaseMessageInfo{
			OperaType: "CC封禁提醒",
			Server:    serverName,
			TenantId:  ccBanTenantID(hitKey),
		},
		Ip:               clientIP + " (" + strings.Join(region, ",") + ")",
		Reason:           "CC攻击，访问频次过高",
		Duration:         banDuration,
		RemainingSeconds: remainingSeconds,
		Time:             time.Now().Format("2006-01-02 15:04:05"),
	})
}
