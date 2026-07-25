package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

type jobSnapshot struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Status    string    `json:"status"`
	Reference string    `json:"reference,omitempty"`
	Message   string    `json:"message"`
	Progress  int       `json:"progress"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type jobRegistry struct {
	mu   sync.RWMutex
	jobs map[string]jobSnapshot
}

func newJobRegistry() *jobRegistry {
	return &jobRegistry{jobs: make(map[string]jobSnapshot)}
}

func (registry *jobRegistry) create(kind, reference string) jobSnapshot {
	now := time.Now().UTC()
	job := jobSnapshot{ID: newJobID(), Type: kind, Status: "queued", Reference: reference, Message: "任务已进入队列", Progress: 0, CreatedAt: now, UpdatedAt: now}
	registry.mu.Lock()
	registry.jobs[job.ID] = job
	registry.mu.Unlock()
	return job
}

func (registry *jobRegistry) update(id, status, message, errorMessage string, progress int) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	job, exists := registry.jobs[id]
	if !exists {
		return
	}
	job.Status, job.Message, job.Error, job.Progress, job.UpdatedAt = status, message, errorMessage, progress, time.Now().UTC()
	registry.jobs[id] = job
}

func (registry *jobRegistry) get(id string) (jobSnapshot, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	job, exists := registry.jobs[id]
	return job, exists
}

func (api *handler) jobStatus(response http.ResponseWriter, request *http.Request) {
	job, exists := api.jobs.get(chi.URLParam(request, "jobID"))
	if !exists {
		api.writeError(response, request, http.StatusNotFound, "JOB_NOT_FOUND", "任务不存在或已过期。")
		return
	}
	writeJSON(response, http.StatusOK, job)
}

func (api *handler) jobEvents(response http.ResponseWriter, request *http.Request) {
	flusher, supported := response.(http.Flusher)
	if !supported {
		api.writeError(response, request, http.StatusNotImplemented, "JOB_STREAM_UNSUPPORTED", "当前连接不支持任务进度流。")
		return
	}
	response.Header().Set("Content-Type", "text/event-stream")
	response.Header().Set("Cache-Control", "no-cache")
	response.Header().Set("X-Accel-Buffering", "no")
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	var lastUpdated time.Time
	for {
		job, exists := api.jobs.get(chi.URLParam(request, "jobID"))
		if !exists {
			return
		}
		if !job.UpdatedAt.Equal(lastUpdated) {
			payload, _ := json.Marshal(job)
			_, _ = response.Write([]byte("event: progress\ndata: " + string(payload) + "\n\n"))
			flusher.Flush()
			lastUpdated = job.UpdatedAt
		}
		if job.Status == "completed" || job.Status == "failed" {
			return
		}
		select {
		case <-request.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

func newJobID() string {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return time.Now().UTC().Format("20060102150405.000000000")
	}
	return hex.EncodeToString(buffer)
}
