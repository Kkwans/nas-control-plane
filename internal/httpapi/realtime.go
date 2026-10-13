package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/controlstore"
	"github.com/Kkwans/nas-control-plane/internal/monitoring"
	"github.com/Kkwans/nas-control-plane/internal/system"
)

const (
	summaryMaxAge             = 15 * time.Second
	summaryFutureSkew         = 5 * time.Second
	realtimeSubscriberLimit   = 64
	realtimeHeartbeatInterval = 15 * time.Second
)

// SummarySnapshots 只读服务 owner 管理的快照，不启动采集，也不回退到请求内 RPC。
type SummarySnapshots interface {
	Latest() monitoring.State
	Done() <-chan struct{}
}

type realtimeTicker interface {
	C() <-chan time.Time
	Stop()
}
type systemRealtimeTicker struct{ *time.Ticker }

func (ticker systemRealtimeTicker) C() <-chan time.Time { return ticker.Ticker.C }

func (api *handler) currentTime() time.Time {
	if api.now != nil {
		return api.now()
	}
	return time.Now()
}
func (api *handler) realtimeTicker(interval time.Duration) realtimeTicker {
	if api.newRealtimeTicker != nil {
		return api.newRealtimeTicker(interval)
	}
	return systemRealtimeTicker{time.NewTicker(interval)}
}

// 接收年龄保留单调时钟；数据自身也必须新鲜，不能被重复接收再次保鲜。
// 历史错误独立返回，warning 不等同于整个快照不可用。
func (api *handler) latestSystemSummary(now time.Time) (*system.Summary, string) {
	if api.summarySnapshots == nil {
		return nil, ""
	}
	state := api.summarySnapshots.Latest()
	select {
	case <-api.summarySnapshots.Done():
		return nil, state.PersistenceErrorCode
	default:
	}
	if state.Summary == nil || state.LastSuccessAt.IsZero() || state.LastErrorCode != "" ||
		now.Sub(state.LastSuccessAt) > summaryMaxAge || state.LastSuccessAt.After(now.Add(summaryFutureSkew)) ||
		state.Summary.CollectedAt.IsZero() || now.Sub(state.Summary.CollectedAt) > summaryMaxAge ||
		state.Summary.CollectedAt.After(now.Add(summaryFutureSkew)) {
		return nil, state.PersistenceErrorCode
	}
	return state.Summary, state.PersistenceErrorCode
}

func (api *handler) acquireRealtimeSubscriber() bool {
	for {
		count := api.realtimeSubscribers.Load()
		if count >= realtimeSubscriberLimit {
			return false
		}
		if api.realtimeSubscribers.CompareAndSwap(count, count+1) {
			return true
		}
	}
}

func (api *handler) systemEvents(response http.ResponseWriter, request *http.Request) {
	if request.Context().Err() != nil {
		return
	}
	if !api.acquireRealtimeSubscriber() {
		api.writeError(response, request, http.StatusServiceUnavailable, "REALTIME_SUBSCRIBER_LIMIT", "实时连接已达上限，请稍后重试。")
		return
	}
	defer api.realtimeSubscribers.Add(-1)
	if request.Context().Err() != nil {
		return
	}

	controller, err := api.prepareSSEStream(request.Context(), response)
	if err != nil {
		if request.Context().Err() != nil {
			return
		}
		api.writeError(response, request, http.StatusInternalServerError, "REALTIME_STREAM_UNSUPPORTED", "当前服务不支持有界实时数据流。")
		return
	}
	response.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	response.Header().Set("Cache-Control", "no-cache, no-transform")
	response.Header().Set("Connection", "keep-alive")
	response.Header().Set("X-Accel-Buffering", "no")
	interval := realtimeInterval(request)
	scope := realtimeScope(request)
	if err := api.writeSSEFrame(request.Context(), response, controller, fmt.Sprintf("retry: %d\n\n", interval.Milliseconds())); err != nil {
		return
	}

	var providerDone <-chan struct{}
	if scope.summary && api.summarySnapshots != nil {
		providerDone = api.summarySnapshots.Done()
	}
	sendSnapshot := func() bool {
		if request.Context().Err() != nil {
			return false
		}
		snapshot := RealtimeSnapshot{}
		var dockerError string
		// Docker 暂仍为每连接有超时 RPC；完成后再读取 summary，避免发送过期的旧读取。
		if scope.docker {
			inventory, err := api.collectDockerInventoryContext(request.Context())
			if err != nil {
				dockerError = "DOCKER_INVENTORY_UNAVAILABLE"
			} else {
				snapshot.Docker = &inventory
			}
		}
		if request.Context().Err() != nil {
			return false
		}
		now := api.currentTime()
		snapshot.CollectedAt = now.UTC()
		if scope.summary {
			summary, persistenceError := api.latestSystemSummary(now)
			if request.Context().Err() != nil {
				return false
			}
			snapshot.Summary = summary
			if summary == nil {
				snapshot.Errors = append(snapshot.Errors, "SYSTEM_SUMMARY_UNAVAILABLE")
			}
			// 只公开已定义的稳定历史状态，清理错误仍留在采集诊断中。
			switch persistenceError {
			case "MONITORING_WRITE_FAILED", "MONITORING_SAMPLE_INCOMPLETE", "MONITORING_SAMPLE_INVALID", "MONITORING_SAMPLE_LIMIT_EXCEEDED":
				snapshot.Errors = append(snapshot.Errors, persistenceError)
			}
		}
		if dockerError != "" {
			snapshot.Errors = append(snapshot.Errors, dockerError)
		}
		payload, err := json.Marshal(snapshot)
		if err != nil {
			return false
		}
		return api.writeSSEFrame(request.Context(), response, controller, fmt.Sprintf("event: snapshot\ndata: %s\n\n", payload)) == nil
	}
	if !sendSnapshot() {
		return
	}
	ticker := api.realtimeTicker(interval)
	defer ticker.Stop()
	heartbeat := api.realtimeTicker(realtimeHeartbeatInterval)
	defer heartbeat.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case <-providerDone:
			return
		case <-ticker.C():
			if !sendSnapshot() {
				return
			}
		case <-heartbeat.C():
			if err := api.writeSSEFrame(request.Context(), response, controller, ": heartbeat\n\n"); err != nil {
				return
			}
		}
	}
}

type realtimeStreamScope struct{ summary, docker bool }

func realtimeScope(request *http.Request) realtimeStreamScope {
	scope := realtimeStreamScope{}
	for _, name := range request.URL.Query()["scope"] {
		switch name {
		case "summary":
			scope.summary = true
		case "docker":
			scope.docker = true
		}
	}
	if !scope.summary && !scope.docker {
		scope.summary = true
	}
	return scope
}
func realtimeInterval(request *http.Request) time.Duration {
	seconds, err := strconv.Atoi(request.URL.Query().Get("interval"))
	if err != nil || seconds < controlstore.MinRefreshIntervalSeconds || seconds > controlstore.MaxRefreshIntervalSeconds {
		return defaultRealtimeInterval
	}
	return time.Duration(seconds) * time.Second
}
