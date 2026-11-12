package httpapi

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"slices"

	"github.com/Kkwans/nas-control-plane/internal/agentsocket"
	"github.com/Kkwans/nas-control-plane/internal/controlstore"
	"github.com/Kkwans/nas-control-plane/internal/docker"
)

const containerCreateJobType = "docker-container-create"

func (api *handler) createContainerJob(response http.ResponseWriter, request *http.Request) {
	var input docker.ContainerCreateRequest
	if !api.decodeControlBody(response, request, &input) {
		return
	}
	if _, err := input.Normalize(); err != nil {
		api.writeError(response, request, http.StatusBadRequest, "DOCKER_CONTAINER_CREATE_INVALID", "容器创建参数无效。")
		return
	}
	client, ok := api.agent.(DockerContainerCreateAgentClient)
	if !ok {
		api.writeError(response, request, http.StatusServiceUnavailable, "AGENT_DOCKER_CONTROL_UNAVAILABLE", "Root Agent 尚未提供 Docker 容器创建能力。")
		return
	}
	job, err := api.submitContainerCreate(client, input)
	if err != nil {
		api.writeJobError(response, request, err)
		return
	}
	writeJSON(response, http.StatusAccepted, job)
}

// 闭包是唯一的排队输入所有者；entry 在授予资格或冻结时清除该引用。
func (api *handler) submitContainerCreate(client DockerContainerCreateAgentClient, input docker.ContainerCreateRequest) (jobSnapshot, error) {
	frozen := cloneContainerCreateRequest(input)
	return api.jobs.submit(containerCreateJobType, "", 0, func(ctx context.Context, id string) {
		owned := frozen
		frozen = docker.ContainerCreateRequest{}
		api.runContainerCreate(ctx, id, client, owned)
	})
}

func cloneContainerCreateRequest(input docker.ContainerCreateRequest) docker.ContainerCreateRequest {
	input.Environment = maps.Clone(input.Environment)
	input.Env = slices.Clone(input.Env)
	input.Mounts = slices.Clone(input.Mounts)
	input.Ports = slices.Clone(input.Ports)
	input.Command = slices.Clone(input.Command)
	input.CapAdd = slices.Clone(input.CapAdd)
	input.CapDrop = slices.Clone(input.CapDrop)
	input.Devices = slices.Clone(input.Devices)
	if input.Network != nil {
		network := *input.Network
		input.Network = &network
	}
	if input.GPU != nil {
		gpu := cloneContainerCreateGPU(*input.GPU)
		input.GPU = &gpu
	}
	input.GPUs = slices.Clone(input.GPUs)
	for i := range input.GPUs {
		input.GPUs[i] = cloneContainerCreateGPU(input.GPUs[i])
	}
	return input
}
func cloneContainerCreateGPU(gpu docker.ContainerGPU) docker.ContainerGPU {
	gpu.DeviceIDs = slices.Clone(gpu.DeviceIDs)
	gpu.Capabilities = slices.Clone(gpu.Capabilities)
	gpu.Options = maps.Clone(gpu.Options)
	return gpu
}

func (api *handler) runContainerCreate(ctx context.Context, id string, client DockerContainerCreateAgentClient, input docker.ContainerCreateRequest) {
	// Normalize 只在本地检查；回执必须绑定这一规范化镜像和启动意图。
	spec, err := input.Normalize()
	if err != nil {
		api.jobs.fail(id, "DOCKER_CONTAINER_CREATE_INVALID", "容器创建未开始", "派发前参数校验未通过。")
		return
	}
	expectedImage, expectedRun := spec.Image, spec.RunContainer
	spec = docker.ContainerCreateSpec{}
	requestContext, cancel := context.WithTimeout(ctx, defaultDockerImageTimeout)
	defer cancel()
	result, err := client.CreateDockerContainer(requestContext, api.agentSocketPath, input)
	input = docker.ContainerCreateRequest{}
	if err != nil {
		status, code := containerCreateJobFailure(agentsocket.ErrorCode(err), err, requestContext.Err())
		if status == "failed" {
			api.jobs.fail(id, code, "容器创建未开始", "派发前校验未通过，请检查控制通道与请求。")
		} else {
			api.jobs.interrupt(id, code, "容器创建结果未确认，可能存在残留；请只读刷新核对，勿重复提交")
		}
		return
	}
	if requestContext.Err() != nil {
		_, code := containerCreateJobFailure("", requestContext.Err(), requestContext.Err())
		api.jobs.interrupt(id, code, "容器创建结果未确认；请只读刷新核对，勿重复提交")
		return
	}
	safe := containerCreateJobResult(expectedImage, expectedRun, result)
	result = docker.ContainerCreateResult{}
	if safe == nil {
		api.jobs.interrupt(id, "DOCKER_CONTAINER_CREATE_RECEIPT_INVALID", "容器创建回执无效，结果未确认；请只读刷新核对，勿重复提交")
		return
	}
	api.jobs.completeContainerCreate(id, *safe)
}

func containerCreateJobResult(image string, run bool, result docker.ContainerCreateResult) *controlstore.ContainerCreateJobResult {
	if !validContainerActionJobID(result.ContainerID) || result.Image != image || !result.Created || result.RunContainer != run || result.Started != run || (result.State != "running" && result.State != "stopped") {
		return nil
	}
	return &controlstore.ContainerCreateJobResult{ContainerID: result.ContainerID, Created: true, Started: result.Started}
}

func containerCreateJobFailure(code string, err, contextErr error) (string, string) {
	if errors.Is(err, context.DeadlineExceeded) || contextErr == context.DeadlineExceeded || code == "AGENT_RPC_TIMEOUT" {
		return "interrupted", "JOB_EXECUTION_TIMEOUT"
	}
	if contextErr != nil || errors.Is(err, context.Canceled) || code == "AGENT_RPC_CANCELED" {
		return "interrupted", "JOB_EXECUTION_INTERRUPTED"
	}
	if code == "AGENT_RPC_RESPONSE_INVALID" {
		return "interrupted", "DOCKER_CONTAINER_CREATE_RECEIPT_INVALID"
	}
	switch code {
	case "DOCKER_CONTAINER_CREATE_INVALID", "AGENT_PROTOCOL_MISMATCH", "AGENT_RPC_REQUEST_INVALID":
		return "failed", code
	default:
		return "interrupted", "JOB_EXECUTION_INTERRUPTED"
	}
}

// 仅本专用路径可将最小结果与 completed 放入同一次 CAS；pending 不向外发布结果。
func (r *jobRegistry) completeContainerCreate(id string, result controlstore.ContainerCreateJobResult) {
	e := r.lookup(id)
	if e == nil {
		return
	}
	e.opMu.Lock()
	defer e.opMu.Unlock()
	j := e.snapshot()
	if j.Type != containerCreateJobType || j.Status != "running" || e.deleted || e.pending != nil {
		return
	}
	if !validContainerActionJobID(result.ContainerID) || !result.Created {
		return
	}
	j.Status = "completed"
	j.CreateResult = &result
	j.Message = "容器创建调用已完成；当前运行状态请只读刷新核对"
	j.Error, j.ErrorCode = "", ""
	r.finishLocked(e, j)
}
