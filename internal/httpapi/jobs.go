package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/controlstore"
	"github.com/Kkwans/nas-control-plane/internal/docker"
	"github.com/go-chi/chi/v5"
)

type jobSnapshot struct {
	ID              string              `json:"id"`
	Type            string              `json:"type"`
	Status          string              `json:"status"`
	Reference       string              `json:"reference,omitempty"`
	Message         string              `json:"message"`
	Progress        int                 `json:"progress"`
	Error           string              `json:"error,omitempty"`
	CreatedAt       time.Time           `json:"createdAt"`
	UpdatedAt       time.Time           `json:"updatedAt"`
	CompletedAt     *time.Time          `json:"completedAt,omitempty"`
	DownloadedBytes int64               `json:"downloadedBytes"`
	TotalBytes      int64               `json:"totalBytes"`
	SpeedBytes      int64               `json:"speedBytes"`
	Layers          map[string]jobLayer `json:"layers"`
	ArtifactState   string              `json:"artifactState"`
}

type jobLayer struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Current int64  `json:"current"`
	Total   int64  `json:"total"`
}

type jobPersistence interface {
	UpsertJob(context.Context, controlstore.JobRecord) error
	Jobs(context.Context, string, int) ([]controlstore.JobRecord, error)
	DeleteJob(context.Context, string) error
	MarkRunningJobsInterrupted(context.Context) error
}

type jobRegistry struct {
	mu        sync.RWMutex
	jobs      map[string]jobSnapshot
	cancels   map[string]context.CancelFunc
	store     jobPersistence
	pullSlots chan struct{}
}

func newJobRegistry(store any) *jobRegistry {
	registry := &jobRegistry{
		jobs:      make(map[string]jobSnapshot),
		cancels:   make(map[string]context.CancelFunc),
		pullSlots: make(chan struct{}, 3),
	}
	if persistence, ok := store.(jobPersistence); ok {
		registry.store = persistence
		_ = persistence.MarkRunningJobsInterrupted(context.Background())
		if records, err := persistence.Jobs(context.Background(), "", 500); err == nil {
			for _, record := range records {
				registry.jobs[record.ID] = snapshotFromRecord(record)
			}
		}
	}
	return registry
}

func (registry *jobRegistry) create(kind, reference string) jobSnapshot {
	now := time.Now().UTC()
	job := jobSnapshot{ID: newJobID(), Type: kind, Status: "queued", Reference: reference, Message: "任务已进入队列", Progress: 0, CreatedAt: now, UpdatedAt: now, Layers: make(map[string]jobLayer)}
	registry.mu.Lock()
	registry.jobs[job.ID] = job
	registry.mu.Unlock()
	registry.persist(job)
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
	if status == "completed" && job.Type == "docker-image-pull" {
		job.ArtifactState = "present"
	}
	if status == "completed" || status == "failed" || status == "interrupted" || status == "cancelled" {
		completedAt := job.UpdatedAt
		job.CompletedAt = &completedAt
	}
	registry.jobs[id] = job
	go registry.persist(job)
}

func (registry *jobRegistry) setCancel(id string, cancel context.CancelFunc) {
	registry.mu.Lock()
	registry.cancels[id] = cancel
	registry.mu.Unlock()
}

func (registry *jobRegistry) clearCancel(id string) {
	registry.mu.Lock()
	delete(registry.cancels, id)
	registry.mu.Unlock()
}

func (registry *jobRegistry) cancel(id string) (jobSnapshot, bool, error) {
	registry.mu.Lock()
	job, exists := registry.jobs[id]
	if !exists {
		registry.mu.Unlock()
		return jobSnapshot{}, false, nil
	}
	if job.Status != "queued" && job.Status != "running" {
		registry.mu.Unlock()
		return job, true, errors.New("job is not cancellable")
	}
	cancel := registry.cancels[id]
	if cancel != nil {
		now := time.Now().UTC()
		job.Status = "cancelled"
		job.Message = "下载已停止"
		job.SpeedBytes = 0
		job.UpdatedAt = now
		job.CompletedAt = &now
		registry.jobs[id] = job
	}
	registry.mu.Unlock()
	if cancel == nil {
		return job, true, errors.New("job cancel function unavailable")
	}
	cancel()
	registry.persist(job)
	return job, true, nil
}

func (registry *jobRegistry) updatePullProgress(id string, layer jobLayer) {
	registry.mu.Lock()
	job, exists := registry.jobs[id]
	if !exists {
		registry.mu.Unlock()
		return
	}
	if job.Layers == nil {
		job.Layers = make(map[string]jobLayer)
	}
	if layer.ID == "" {
		layer.ID = "status"
	}
	if previous, ok := job.Layers[layer.ID]; ok {
		if layer.Current <= 0 {
			layer.Current = previous.Current
		}
		if layer.Total <= 0 {
			layer.Total = previous.Total
		}
	}
	if (layer.Status == "Pull complete" || layer.Status == "Already exists") && layer.Total > 0 {
		layer.Current = layer.Total
	}
	job.Layers[layer.ID] = layer
	var current, total int64
	for _, item := range job.Layers {
		current += item.Current
		total += item.Total
	}
	previousBytes, previousAt := job.DownloadedBytes, job.UpdatedAt
	job.DownloadedBytes = max(job.DownloadedBytes, current)
	job.TotalBytes = max(job.TotalBytes, total)
	if elapsed := time.Since(previousAt).Seconds(); elapsed > 0 {
		job.SpeedBytes = int64(float64(current-previousBytes) / elapsed)
	}
	if job.TotalBytes > 0 {
		job.Progress = min(99, max(1, int(float64(job.DownloadedBytes)/float64(job.TotalBytes)*100)))
	}
	job.Message = layer.Status
	job.UpdatedAt = time.Now().UTC()
	registry.jobs[id] = job
	registry.mu.Unlock()
	registry.persist(job)
}

func (registry *jobRegistry) setExpectedTotal(id string, total int64) {
	if total <= 0 {
		return
	}
	registry.mu.Lock()
	job, exists := registry.jobs[id]
	if exists {
		job.TotalBytes = max(job.TotalBytes, total)
		registry.jobs[id] = job
	}
	registry.mu.Unlock()
	if exists {
		registry.persist(job)
	}
}

func (registry *jobRegistry) completePull(id string) {
	registry.mu.Lock()
	job, exists := registry.jobs[id]
	if exists {
		if job.TotalBytes > 0 {
			job.DownloadedBytes = job.TotalBytes
		}
		job.SpeedBytes = 0
		job.Progress = 100
		registry.jobs[id] = job
	}
	registry.mu.Unlock()
	if exists {
		registry.persist(job)
	}
}

func (registry *jobRegistry) get(id string) (jobSnapshot, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	job, exists := registry.jobs[id]
	return job, exists
}

func (registry *jobRegistry) list(kind string) []jobSnapshot {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	result := make([]jobSnapshot, 0, len(registry.jobs))
	for _, job := range registry.jobs {
		if kind == "" || job.Type == kind {
			result = append(result, job)
		}
	}
	sort.Slice(result, func(left, right int) bool { return result[left].CreatedAt.After(result[right].CreatedAt) })
	return result
}

func (registry *jobRegistry) delete(id string) (jobSnapshot, bool, error) {
	registry.mu.Lock()
	job, exists := registry.jobs[id]
	if !exists {
		registry.mu.Unlock()
		return jobSnapshot{}, false, nil
	}
	if job.Status == "queued" || job.Status == "running" {
		registry.mu.Unlock()
		return job, true, errors.New("active job cannot be deleted")
	}
	delete(registry.jobs, id)
	registry.mu.Unlock()
	if registry.store != nil {
		if err := registry.store.DeleteJob(context.Background(), id); err != nil {
			registry.mu.Lock()
			registry.jobs[id] = job
			registry.mu.Unlock()
			return job, true, err
		}
	}
	return job, true, nil
}

func (registry *jobRegistry) persist(job jobSnapshot) {
	if registry.store == nil {
		return
	}
	layers, _ := json.Marshal(job.Layers)
	record := controlstore.JobRecord{
		ID: job.ID, Type: job.Type, Status: job.Status, Reference: job.Reference,
		Message: job.Message, Progress: job.Progress, Error: job.Error,
		DownloadedBytes: job.DownloadedBytes, TotalBytes: job.TotalBytes, SpeedBytes: job.SpeedBytes,
		LayersJSON: string(layers), CreatedAt: job.CreatedAt, UpdatedAt: job.UpdatedAt,
	}
	if job.CompletedAt != nil {
		record.CompletedAt = *job.CompletedAt
	}
	_ = registry.store.UpsertJob(context.Background(), record)
}

func snapshotFromRecord(record controlstore.JobRecord) jobSnapshot {
	layers := make(map[string]jobLayer)
	_ = json.Unmarshal([]byte(record.LayersJSON), &layers)
	job := jobSnapshot{
		ID: record.ID, Type: record.Type, Status: record.Status, Reference: record.Reference,
		Message: record.Message, Progress: record.Progress, Error: record.Error,
		DownloadedBytes: record.DownloadedBytes, TotalBytes: record.TotalBytes, SpeedBytes: record.SpeedBytes,
		Layers: layers, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
	if !record.CompletedAt.IsZero() {
		completedAt := record.CompletedAt
		job.CompletedAt = &completedAt
	}
	return job
}

func (api *handler) listJobs(response http.ResponseWriter, request *http.Request) {
	jobs := api.jobs.list(request.URL.Query().Get("type"))
	if request.URL.Query().Get("type") == "" || request.URL.Query().Get("type") == "docker-image-pull" {
		jobs = api.resolveDockerArtifactStates(request.Context(), jobs)
	}
	writeJSON(response, http.StatusOK, map[string]any{"jobs": jobs})
}

func (api *handler) resolveDockerArtifactStates(ctx context.Context, jobs []jobSnapshot) []jobSnapshot {
	hasCompletedPull := false
	for _, job := range jobs {
		if job.Type == "docker-image-pull" && job.Status == "completed" {
			hasCompletedPull = true
			break
		}
	}
	if !hasCompletedPull {
		return jobs
	}

	lookupContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	inventory, err := api.dockerImages.ListDockerImages(lookupContext, api.agentSocketPath)
	if err != nil {
		for index := range jobs {
			if jobs[index].Type == "docker-image-pull" && jobs[index].Status == "completed" {
				jobs[index].ArtifactState = "unknown"
			}
		}
		return jobs
	}

	references := make(map[string]struct{})
	for _, image := range inventory.Images {
		for _, reference := range append(image.RepoTags, image.RepoDigests...) {
			normalized := strings.TrimPrefix(strings.TrimSpace(reference), "docker.io/")
			if normalized != "" {
				references[normalized] = struct{}{}
			}
		}
	}
	for index := range jobs {
		job := &jobs[index]
		if job.Type != "docker-image-pull" || job.Status != "completed" {
			continue
		}
		reference := strings.TrimPrefix(strings.TrimSpace(job.Reference), "docker.io/")
		if _, present := references[reference]; present {
			job.ArtifactState = "present"
		} else {
			job.ArtifactState = "deleted"
		}
	}
	return jobs
}

func (api *handler) deleteJob(response http.ResponseWriter, request *http.Request) {
	_, exists, err := api.jobs.delete(chi.URLParam(request, "jobID"))
	if !exists {
		api.writeError(response, request, http.StatusNotFound, "JOB_NOT_FOUND", "任务不存在或已被删除。")
		return
	}
	if err != nil {
		if err.Error() == "active job cannot be deleted" {
			api.writeError(response, request, http.StatusConflict, "JOB_DELETE_UNAVAILABLE", "进行中的任务不能删除。")
			return
		}
		api.writeError(response, request, http.StatusInternalServerError, "JOB_DELETE_FAILED", "任务记录删除失败。")
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (api *handler) retryJob(response http.ResponseWriter, request *http.Request) {
	previous, exists := api.jobs.get(chi.URLParam(request, "jobID"))
	if !exists {
		api.writeError(response, request, http.StatusNotFound, "JOB_NOT_FOUND", "任务不存在。")
		return
	}
	if previous.Type != "docker-image-pull" ||
		(previous.Status != "failed" && previous.Status != "interrupted" && previous.Status != "cancelled" && previous.Status != "completed") {
		api.writeError(response, request, http.StatusConflict, "JOB_RETRY_UNAVAILABLE", "当前任务不能重试。")
		return
	}
	job := api.jobs.create(previous.Type, previous.Reference)
	api.jobs.setExpectedTotal(job.ID, previous.TotalBytes)
	job, _ = api.jobs.get(job.ID)
	go api.runImagePull(job.ID, docker.ImagePullRequest{Reference: previous.Reference, ExpectedBytes: previous.TotalBytes})
	writeJSON(response, http.StatusAccepted, job)
}

func (api *handler) cancelJob(response http.ResponseWriter, request *http.Request) {
	job, exists, err := api.jobs.cancel(chi.URLParam(request, "jobID"))
	if !exists {
		api.writeError(response, request, http.StatusNotFound, "JOB_NOT_FOUND", "任务不存在。")
		return
	}
	if err != nil {
		api.writeError(response, request, http.StatusConflict, "JOB_CANCEL_UNAVAILABLE", "当前任务不能停止。")
		return
	}
	writeJSON(response, http.StatusAccepted, job)
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
	ticker := time.NewTicker(200 * time.Millisecond)
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
		if job.Status == "completed" || job.Status == "failed" || job.Status == "interrupted" || job.Status == "cancelled" {
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
