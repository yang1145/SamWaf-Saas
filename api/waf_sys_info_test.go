package api

import (
	"SamWaf/global"
	"SamWaf/model"
	"SamWaf/wafsec"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"testing"
)

// 响应体包装：{"code":..,"data":..,"msg":..}
type WafCheckVersionResponse struct {
	Code int             `json:"code"`
	Data json.RawMessage `json:"data"`
	Msg  string          `json:"msg"`
}

// 测试用例 SysRuntimeInfoApi：系统信息探测必须永远有返回，不能因为取不到某项而失败
func TestSysRuntimeInfoApi(t *testing.T) {
	r := gin.Default()
	global.GWAF_RELEASE_VERSION = "v1.0.0"
	global.GWAF_RELEASE_VERSION_NAME = "20241028"
	r.GET("/api/v1/sysinfo/runtimeinfo", new(WafSysInfoApi).SysRuntimeInfoApi)

	req, err := http.NewRequest(http.MethodGet, "/api/v1/sysinfo/runtimeinfo", nil)
	if err != nil {
		t.Fatalf("无法创建请求：%v", err)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	t.Logf("响应体内容: %s", rec.Body.String())

	if rec.Code != http.StatusOK {
		t.Errorf("期望的状态码：%d，实际状态码：%d", http.StatusOK, rec.Code)
	}
	var response WafCheckVersionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("响应解析失败：%v", err)
	}
	if response.Code != 0 {
		t.Errorf("期望的响应码：0，实际：%v", response.Code)
	}
	// 响应体默认经通讯密钥加密，先解密再解析
	payload := response.Data
	var encrypted string
	if json.Unmarshal(response.Data, &encrypted) == nil {
		decrypted, err := wafsec.AesDecrypt(encrypted, global.GWAF_COMMUNICATION_KEY)
		if err != nil {
			t.Fatalf("响应解密失败：%v", err)
		}
		payload = decrypted
	}
	var info model.RuntimeSystemInfo
	if err := json.Unmarshal(payload, &info); err != nil {
		t.Fatalf("系统信息解析失败：%v", err)
	}
	if info.OS == "" || info.Arch == "" || info.GoVersion == "" {
		t.Errorf("编译相关信息不应为空：%+v", info)
	}
	if info.OSName == "" {
		t.Error("操作系统名称至少应有兜底值")
	}
	if info.Version != "v1.0.0" || info.VersionName != "20241028" {
		t.Errorf("软件版本信息不正确：%+v", info)
	}
	if info.ProcessUptimeSeconds < 0 {
		t.Errorf("程序运行时长不应为负：%v", info.ProcessUptimeSeconds)
	}
}
