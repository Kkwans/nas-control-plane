package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/monitoring"
)

const monitoringAlertsResponseLimit = 8 * 1024

// ResourceAlertSnapshots 只读既有采集 owner 的内存状态，禁止请求内采样或读历史。
type ResourceAlertSnapshots interface {
	LatestAlerts(time.Time) monitoring.AlertsSnapshot
	Done() <-chan struct{}
}

func (api *handler) monitoringAlerts(response http.ResponseWriter, request *http.Request) {
	unavailable := func() {
		api.writeError(response, request, http.StatusServiceUnavailable, "MONITORING_ALERTS_UNAVAILABLE", "当前资源提示暂不可用。")
	}
	if api.resourceAlerts == nil {
		unavailable()
		return
	}
	select {
	case <-api.resourceAlerts.Done():
		unavailable()
		return
	default:
	}
	snapshot := api.resourceAlerts.LatestAlerts(api.currentTime())
	// 读取期间 owner 停止也不能将旧状态伪装成仍在运行。
	select {
	case <-api.resourceAlerts.Done():
		unavailable()
		return
	default:
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		api.writeError(response, request, http.StatusInternalServerError, "MONITORING_ALERTS_RESPONSE_INVALID", "当前资源提示包含无效数据。")
		return
	}
	if len(encoded)+1 > monitoringAlertsResponseLimit {
		api.writeError(response, request, http.StatusInternalServerError, "MONITORING_ALERTS_LIMIT_EXCEEDED", "当前资源提示超过响应预算。")
		return
	}
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(append(encoded, '\n'))
}
