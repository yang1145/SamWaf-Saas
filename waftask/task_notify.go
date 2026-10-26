package waftask

import (
	"SamWaf/common/wafctx"
	"SamWaf/common/zlog"
	"SamWaf/global"
	"SamWaf/innerbean"
	"SamWaf/service/waf_service"
	"context"
	"fmt"
)

// TaskStatusNotify 汇总通知
func TaskStatusNotify() {
	zlog.Debug("TaskStatusNotify")
	// 平台级定时任务：汇总全部租户的量，用平台作用域跳过租户过滤
	statHomeInfo, err := waf_service.WafStatServiceApp.StatHomeSumDayApi(wafctx.PlatformScope(context.Background()))
	if err == nil {
		noticeStr := fmt.Sprintf("今日访问量：%d 今天恶意访问量:%d 昨日恶意访问量:%d", statHomeInfo.VisitCountOfToday, statHomeInfo.AttackCountOfToday, statHomeInfo.AttackCountOfYesterday)

		serverName := global.GWAF_CUSTOM_SERVER_NAME
		if serverName == "" {
			serverName = "未命名服务器"
		}
		global.GQEQUE_MESSAGE_DB.Enqueue(innerbean.OperatorMessageInfo{
			BaseMessageInfo: innerbean.BaseMessageInfo{
				OperaType: "汇总通知",
				Server:    serverName,
			},
			OperaCnt: noticeStr,
		})
	} else {
		zlog.Error("TaskStatusNotifyerror", err)
	}

}
