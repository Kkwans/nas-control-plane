package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/controlstore"
	"github.com/Kkwans/nas-control-plane/internal/docker"
	"github.com/go-chi/chi/v5"
)

type jobSnapshot struct {
	ProjectActionResult *controlstore.ProjectActionJobResult   `json:"projectActionResult,omitempty"`
	CreateResult        *controlstore.ContainerCreateJobResult `json:"createResult,omitempty"`
	ProgressLimited     bool                                   `json:"progressLimited,omitempty"`
	CancellationStatus  string                                 `json:"cancellationStatus,omitempty"`
	PersistenceStatus   string                                 `json:"persistenceStatus,omitempty"`
	Cancellable         bool                                   `json:"cancellable"`
	ErrorCode           string                                 `json:"errorCode,omitempty"`
	ID                  string                                 `json:"id"`
	Type                string                                 `json:"type"`
	Status              string                                 `json:"status"`
	Reference           string                                 `json:"reference,omitempty"`
	Message             string                                 `json:"message"`
	Progress            int                                    `json:"progress"`
	Error               string                                 `json:"error,omitempty"`
	CreatedAt           time.Time                              `json:"createdAt"`
	UpdatedAt           time.Time                              `json:"updatedAt"`
	CompletedAt         *time.Time                             `json:"completedAt,omitempty"`
	DownloadedBytes     int64                                  `json:"downloadedBytes"`
	TotalBytes          int64                                  `json:"totalBytes"`
	SpeedBytes          int64                                  `json:"speedBytes"`
	Layers              map[string]jobLayer                    `json:"layers"`
	ArtifactState       string                                 `json:"artifactState"`
}

type jobLayer struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Current int64  `json:"current"`
	Total   int64  `json:"total"`
}

const (
	jobArtifactPresent = "present"
	jobArtifactDeleted = "deleted"
	jobArtifactUnknown = "unknown"
)

func snapshotFromRecord(record controlstore.JobRecord) jobSnapshot {
	layers := make(map[string]jobLayer)
	_ = json.Unmarshal([]byte(record.LayersJSON), &layers)
	job := jobSnapshot{
		ProgressLimited: record.ProgressLimited, CancellationStatus: record.CancellationStatus, ErrorCode: record.ErrorCode, Cancellable: record.Type == "docker-image-pull",
		ID: record.ID, Type: record.Type, Status: record.Status, Reference: record.Reference,
		Message: record.Message, Progress: record.Progress, Error: record.Error,
		DownloadedBytes: record.DownloadedBytes, TotalBytes: record.TotalBytes, SpeedBytes: record.SpeedBytes,
		Layers: layers, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
		ArtifactState: jobArtifactUnknown,
	}
	result, err := controlstore.ParseContainerCreateJobResult(record)
	if err != nil {
		job.Status = "interrupted"
		job.ErrorCode = "JOB_HISTORY_CORRUPT"
		job.Message = "任务历史结果损坏，创建结果未确认，请只读刷新核对"
	} else {
		job.CreateResult = result
	}
	projectResult, projectErr := controlstore.ParseProjectActionJobResult(record)
	if projectErr != nil {
		job.Status = "interrupted"
		job.ErrorCode = "JOB_HISTORY_CORRUPT"
		job.Message = "任务历史结果损坏，项目操作结果未确认，请只读刷新核对"
	} else {
		job.ProjectActionResult = projectResult
	}
	if err != nil || projectErr != nil {
		job.CreateResult = nil
		job.ProjectActionResult = nil
	}
	if !validJobLayers(record.LayersJSON) {
		job.ErrorCode = "JOB_HISTORY_CORRUPT"
		job.Message = "任务历史进度损坏，请检查数据库备份"
	}
	if !record.CompletedAt.IsZero() {
		completedAt := record.CompletedAt
		job.CompletedAt = &completedAt
	}
	return job
}

// cloneJobSnapshot 必须在持有 registry 锁时调用（或输入已独立），避免可变字段逃逸。
func cloneJobSnapshot(job jobSnapshot) jobSnapshot {
	if job.ProjectActionResult != nil {
		result := *job.ProjectActionResult
		result.Items = append([]controlstore.ProjectActionJobItem(nil), result.Items...)
		job.ProjectActionResult = &result
	}
	if job.CreateResult != nil {
		result := *job.CreateResult
		job.CreateResult = &result
	}
	if job.Layers != nil {
		layers := make(map[string]jobLayer, len(job.Layers))
		for id, layer := range job.Layers {
			layers[id] = layer
		}
		job.Layers = layers
	}
	if job.CompletedAt != nil {
		completedAt := *job.CompletedAt
		job.CompletedAt = &completedAt
	}
	return job
}

func normalizeJobSnapshot(job jobSnapshot) jobSnapshot {
	switch job.ArtifactState {
	case jobArtifactPresent, jobArtifactDeleted, jobArtifactUnknown:
	default:
		job.ArtifactState = jobArtifactUnknown
	}
	if job.Layers == nil {
		job.Layers = make(map[string]jobLayer)
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
				jobs[index].ArtifactState = jobArtifactUnknown
			}
		}
		return jobs
	}

	references := make(map[string]struct{})
	for _, image := range inventory.Images {
		for _, reference := range append(image.RepoTags, image.RepoDigests...) {
			normalized := dockerArtifactReferenceKey(reference)
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
		reference := dockerArtifactReferenceKey(job.Reference)
		if reference == "" {
			job.ArtifactState = jobArtifactUnknown
			continue
		}
		if _, present := references[reference]; present {
			job.ArtifactState = jobArtifactPresent
		} else {
			job.ArtifactState = jobArtifactDeleted
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
		if errors.Is(err, errJobActive) {
			api.writeError(response, request, http.StatusConflict, "JOB_DELETE_UNAVAILABLE", "进行中的任务不能删除。")
			return
		}
		api.writeJobError(response, request, err)
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
	job, err := api.submitImagePull(docker.ImagePullRequest{Reference: previous.Reference, ExpectedBytes: previous.TotalBytes})
	if err != nil {
		api.writeJobError(response, request, err)
		return
	}
	writeJSON(response, http.StatusAccepted, job)
}

func (api *handler) cancelJob(response http.ResponseWriter, request *http.Request) {
	job, exists, err := api.jobs.cancel(chi.URLParam(request, "jobID"))
	if !exists {
		api.writeError(response, request, http.StatusNotFound, "JOB_NOT_FOUND", "任务不存在。")
		return
	}
	if err != nil {
		if errors.Is(err, errJobPersist) {
			api.writeJobError(response, request, err)
			return
		}
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
	if request.Context().Err() != nil {
		return
	}
	controller, err := api.prepareSSEStream(request.Context(), response)
	if err != nil {
		if request.Context().Err() != nil {
			return
		}
		api.writeError(response, request, http.StatusNotImplemented, "JOB_STREAM_UNSUPPORTED", "当前连接不支持任务进度流。")
		return
	}
	response.Header().Set("Content-Type", "text/event-stream")
	response.Header().Set("Cache-Control", "no-cache")
	response.Header().Set("X-Accel-Buffering", "no")
	ticker := api.realtimeTicker(200 * time.Millisecond)
	defer ticker.Stop()
	var lastPayload string
	for {
		if request.Context().Err() != nil {
			return
		}
		job, exists := api.jobs.get(chi.URLParam(request, "jobID"))
		if request.Context().Err() != nil || !exists {
			return
		}
		payload, err := json.Marshal(job)
		if err != nil {
			return
		}
		if string(payload) != lastPayload {
			if err := api.writeSSEFrame(request.Context(), response, controller, "event: progress\ndata: "+string(payload)+"\n\n"); err != nil {
				return
			}
			lastPayload = string(payload)
		}
		if job.Status == "completed" || job.Status == "failed" || job.Status == "interrupted" || job.Status == "cancelled" {
			return
		}
		select {
		case <-request.Context().Done():
			return
		case <-ticker.C():
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
