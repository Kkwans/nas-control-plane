package agentsocket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"time"

	ncpcompose "github.com/Kkwans/nas-control-plane/internal/compose"
	"github.com/Kkwans/nas-control-plane/internal/docker"
	"github.com/Kkwans/nas-control-plane/internal/journal"
	"github.com/Kkwans/nas-control-plane/internal/system"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
)

type AgentStatus struct {
	ProtocolVersion string `json:"protocolVersion"`
	BuildVersion    string `json:"buildVersion"`
	AgentEUID       int    `json:"agentEUID"`
	Transport       string `json:"transport"`
}

func Probe(ctx context.Context, socketPath string) (AgentStatus, error) {
	if err := ctx.Err(); err != nil {
		return AgentStatus{}, contextError(err)
	}
	if socketPath == "" {
		return AgentStatus{}, coded("AGENT_RPC_TARGET_INVALID", errors.New("socket path is required"))
	}

	connection, err := grpc.NewClient("unix://"+socketPath, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return AgentStatus{}, coded("AGENT_RPC_CONNECTION_FAILED", err)
	}
	defer connection.Close()

	response, err := NewAgentProbeServiceClient(connection).GetStatus(ctx, &emptypb.Empty{})
	if err != nil {
		return AgentStatus{}, rpcError(err)
	}
	return decodeAgentStatus(response)
}

func CollectCapabilities(ctx context.Context, socketPath string) (system.Capabilities, error) {
	if err := ctx.Err(); err != nil {
		return system.Capabilities{}, contextError(err)
	}
	if socketPath == "" {
		return system.Capabilities{}, coded("AGENT_RPC_TARGET_INVALID", errors.New("socket path is required"))
	}

	connection, err := grpc.NewClient("unix://"+socketPath, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return system.Capabilities{}, coded("AGENT_RPC_CONNECTION_FAILED", err)
	}
	defer connection.Close()

	response, err := NewAgentProbeServiceClient(connection).GetCapabilities(ctx, &emptypb.Empty{})
	if err != nil {
		return system.Capabilities{}, rpcError(err)
	}
	return decodeCapabilities(response)
}

func CollectSystemSummary(ctx context.Context, socketPath string) (system.Summary, error) {
	if err := ctx.Err(); err != nil {
		return system.Summary{}, contextError(err)
	}
	connection, err := dialSocket(socketPath)
	if err != nil {
		return system.Summary{}, err
	}
	defer connection.Close()

	response, err := NewAgentDashboardServiceClient(connection).GetSystemSummary(ctx, &emptypb.Empty{})
	if err != nil {
		return system.Summary{}, rpcError(err)
	}
	return decodeSystemSummary(response)
}

func CollectSystemDetails(ctx context.Context, socketPath string) (system.Details, error) {
	if err := ctx.Err(); err != nil {
		return system.Details{}, contextError(err)
	}
	connection, err := dialSocket(socketPath)
	if err != nil {
		return system.Details{}, err
	}
	defer connection.Close()

	response, err := NewAgentDashboardServiceClient(connection).GetSystemDetails(ctx, &emptypb.Empty{})
	if err != nil {
		return system.Details{}, rpcError(err)
	}
	return decodeSystemDetails(response)
}

func CollectDockerInventory(ctx context.Context, socketPath string) (docker.Inventory, error) {
	if err := ctx.Err(); err != nil {
		return docker.Inventory{}, contextError(err)
	}
	connection, err := dialSocket(socketPath)
	if err != nil {
		return docker.Inventory{}, err
	}
	defer connection.Close()

	response, err := NewAgentDashboardServiceClient(connection).GetDockerInventory(ctx, &emptypb.Empty{})
	if err != nil {
		return docker.Inventory{}, rpcError(err)
	}
	return decodeDockerInventory(response)
}

func ProbeWeb(ctx context.Context, socketPath string, targetURL string) (WebProbeResult, error) {
	if err := ctx.Err(); err != nil {
		return WebProbeResult{}, contextError(err)
	}
	connection, err := dialSocket(socketPath)
	if err != nil {
		return WebProbeResult{}, err
	}
	defer connection.Close()
	request, err := structpb.NewStruct(map[string]any{"url": targetURL})
	if err != nil {
		return WebProbeResult{}, coded("AGENT_RPC_REQUEST_INVALID", err)
	}
	response, err := NewAgentWebProbeServiceClient(connection).Probe(ctx, request)
	if err != nil {
		return WebProbeResult{}, rpcError(err)
	}
	fields := response.GetFields()
	return WebProbeResult{
		URL:         fields["url"].GetStringValue(),
		Title:       fields["title"].GetStringValue(),
		IconURL:     fields["iconUrl"].GetStringValue(),
		ContentType: fields["contentType"].GetStringValue(),
		StatusCode:  int(fields["statusCode"].GetNumberValue()),
	}, nil
}

func ControlContainer(ctx context.Context, socketPath string, request docker.ContainerActionRequest) (docker.ContainerActionResult, error) {
	if err := request.Validate(); err != nil {
		return docker.ContainerActionResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return docker.ContainerActionResult{}, contextError(err)
	}
	status, err := Probe(ctx, socketPath)
	if err != nil {
		return docker.ContainerActionResult{}, err
	}
	if status.ProtocolVersion != ProtocolVersion {
		return docker.ContainerActionResult{}, coded("AGENT_PROTOCOL_MISMATCH", fmt.Errorf(
			"server protocol %s does not match agent protocol %s",
			ProtocolVersion,
			status.ProtocolVersion,
		))
	}
	connection, err := dialSocket(socketPath)
	if err != nil {
		return docker.ContainerActionResult{}, err
	}
	defer connection.Close()

	payload, err := structpb.NewStruct(map[string]any{
		"container_id": request.ContainerID,
		"action":       string(request.Action),
	})
	if err != nil {
		return docker.ContainerActionResult{}, coded("AGENT_RPC_REQUEST_INVALID", err)
	}
	response, err := NewAgentDockerControlServiceClient(connection).ControlContainer(ctx, payload)
	if err != nil {
		return docker.ContainerActionResult{}, rpcError(err)
	}
	return decodeContainerActionResult(response)
}

func ReadContainerLogs(ctx context.Context, socketPath string, request docker.ContainerLogsRequest) (docker.ContainerLogsResult, error) {
	request, err := request.Normalize()
	if err != nil {
		return docker.ContainerLogsResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return docker.ContainerLogsResult{}, contextError(err)
	}
	connection, err := dialSocket(socketPath)
	if err != nil {
		return docker.ContainerLogsResult{}, err
	}
	defer connection.Close()

	payload, err := structpb.NewStruct(map[string]any{
		"container_id": request.ContainerID,
		"tail":         request.Tail,
		"since":        request.Since,
	})
	if err != nil {
		return docker.ContainerLogsResult{}, coded("AGENT_RPC_REQUEST_INVALID", err)
	}
	response, err := NewAgentDockerLogsServiceClient(connection).ReadLogs(ctx, payload)
	if err != nil {
		return docker.ContainerLogsResult{}, rpcError(err)
	}
	return decodeContainerLogsResult(response)
}

func QueryJournal(ctx context.Context, socketPath string, query journal.Query) (journal.Page, error) {
	if err := ctx.Err(); err != nil {
		return journal.Page{}, contextError(err)
	}
	connection, err := dialSocket(socketPath)
	if err != nil {
		return journal.Page{}, err
	}
	defer connection.Close()
	since := ""
	if query.Since != nil {
		since = query.Since.UTC().Format(time.RFC3339)
	}
	payload, err := structpb.NewStruct(map[string]any{
		"unit": query.Unit, "cursor": query.Cursor, "limit": query.Limit, "since": since,
	})
	if err != nil {
		return journal.Page{}, coded("AGENT_RPC_REQUEST_INVALID", err)
	}
	response, err := NewAgentJournalServiceClient(connection).Query(ctx, payload)
	if err != nil {
		return journal.Page{}, rpcError(err)
	}
	var page journal.Page
	if err := decodeDashboardResponse(response, &page); err != nil {
		return journal.Page{}, err
	}
	if page.Entries == nil {
		page.Entries = make([]journal.Entry, 0)
	}
	return page, nil
}

func ListDockerImages(ctx context.Context, socketPath string) (docker.ImageInventory, error) {
	if err := ctx.Err(); err != nil {
		return docker.ImageInventory{}, contextError(err)
	}
	connection, err := dialSocket(socketPath)
	if err != nil {
		return docker.ImageInventory{}, err
	}
	defer connection.Close()

	response, err := NewAgentDockerImagesServiceClient(connection).ListImages(ctx, &emptypb.Empty{})
	if err != nil {
		return docker.ImageInventory{}, rpcError(err)
	}
	var inventory docker.ImageInventory
	if err := decodeDashboardResponse(response, &inventory); err != nil {
		return docker.ImageInventory{}, err
	}
	if inventory.CollectedAt.IsZero() || inventory.Images == nil {
		return docker.ImageInventory{}, coded("AGENT_RPC_RESPONSE_INVALID", errors.New("image inventory is incomplete"))
	}
	return inventory, nil
}

func SearchDockerHub(ctx context.Context, socketPath string, request docker.HubSearchRequest) (docker.HubSearchResult, error) {
	connection, err := dialSocket(socketPath)
	if err != nil {
		return docker.HubSearchResult{}, err
	}
	defer connection.Close()
	payload, err := structpb.NewStruct(map[string]any{
		"query": request.Query, "page": request.Page, "page_size": request.PageSize, "sort": request.Sort,
	})
	if err != nil {
		return docker.HubSearchResult{}, coded("AGENT_RPC_REQUEST_INVALID", err)
	}
	response, err := NewAgentDockerImagesServiceClient(connection).SearchImages(ctx, payload)
	if err != nil {
		return docker.HubSearchResult{}, rpcError(err)
	}
	var result docker.HubSearchResult
	if err := decodeDashboardResponse(response, &result); err != nil {
		return docker.HubSearchResult{}, err
	}
	if result.Results == nil {
		result.Results = make([]docker.HubRepository, 0)
	}
	return result, nil
}

func ListDockerHubTags(ctx context.Context, socketPath string, request docker.HubTagsRequest) (docker.HubTagsResult, error) {
	connection, err := dialSocket(socketPath)
	if err != nil {
		return docker.HubTagsResult{}, err
	}
	defer connection.Close()
	payload, err := structpb.NewStruct(map[string]any{
		"namespace": request.Namespace, "repository": request.Repository,
		"page": request.Page, "page_size": request.PageSize,
	})
	if err != nil {
		return docker.HubTagsResult{}, coded("AGENT_RPC_REQUEST_INVALID", err)
	}
	response, err := NewAgentDockerImagesServiceClient(connection).ListTags(ctx, payload)
	if err != nil {
		return docker.HubTagsResult{}, rpcError(err)
	}
	var result docker.HubTagsResult
	if err := decodeDashboardResponse(response, &result); err != nil {
		return docker.HubTagsResult{}, err
	}
	if result.Results == nil {
		result.Results = make([]docker.HubTag, 0)
	}
	for index := range result.Results {
		if result.Results[index].Architectures == nil {
			result.Results[index].Architectures = make([]string, 0)
		}
	}
	return result, nil
}

func PullDockerImage(ctx context.Context, socketPath string, request docker.ImagePullRequest) (docker.ImagePullResult, error) {
	connection, err := dialSocket(socketPath)
	if err != nil {
		return docker.ImagePullResult{}, err
	}
	defer connection.Close()
	payload, err := structpb.NewStruct(map[string]any{"reference": request.Reference})
	if err != nil {
		return docker.ImagePullResult{}, coded("AGENT_RPC_REQUEST_INVALID", err)
	}
	response, err := NewAgentDockerImagesServiceClient(connection).PullImage(ctx, payload)
	if err != nil {
		return docker.ImagePullResult{}, rpcError(err)
	}
	var result docker.ImagePullResult
	if err := decodeDashboardResponse(response, &result); err != nil {
		return docker.ImagePullResult{}, err
	}
	if result.Reference == "" || !result.Completed {
		return docker.ImagePullResult{}, coded("AGENT_RPC_RESPONSE_INVALID", errors.New("image pull result is incomplete"))
	}
	return result, nil
}

func PullDockerImageWithProgress(ctx context.Context, socketPath string, request docker.ImagePullRequest, onProgress func(docker.ImagePullProgress)) (docker.ImagePullResult, error) {
	connection, err := dialSocket(socketPath)
	if err != nil {
		return docker.ImagePullResult{}, err
	}
	defer connection.Close()
	payload, err := structpb.NewStruct(map[string]any{"reference": request.Reference})
	if err != nil {
		return docker.ImagePullResult{}, coded("AGENT_RPC_REQUEST_INVALID", err)
	}
	stream, err := NewAgentDockerImagesServiceClient(connection).PullImageStream(ctx, payload)
	if err != nil {
		return docker.ImagePullResult{}, rpcError(err)
	}
	for {
		message, receiveError := stream.Recv()
		if errors.Is(receiveError, io.EOF) {
			break
		}
		if receiveError != nil {
			return docker.ImagePullResult{}, rpcError(receiveError)
		}
		fields := message.GetFields()
		if fields["type"].GetStringValue() == "completed" {
			return docker.ImagePullResult{
				Reference: fields["reference"].GetStringValue(),
				Completed: fields["completed"].GetBoolValue(),
			}, nil
		}
		if onProgress != nil {
			onProgress(docker.ImagePullProgress{
				LayerID: fields["layerId"].GetStringValue(),
				Status:  fields["status"].GetStringValue(),
				Current: int64(fields["current"].GetNumberValue()),
				Total:   int64(fields["total"].GetNumberValue()),
			})
		}
	}
	return docker.ImagePullResult{}, coded("AGENT_RPC_RESPONSE_INVALID", errors.New("image pull stream ended without completion"))
}

func RemoveDockerImage(ctx context.Context, socketPath string, request docker.ImageRemoveRequest) (docker.ImageRemoveResult, error) {
	connection, err := dialSocket(socketPath)
	if err != nil {
		return docker.ImageRemoveResult{}, err
	}
	defer connection.Close()
	payload, err := structpb.NewStruct(map[string]any{"image_id": request.ImageID})
	if err != nil {
		return docker.ImageRemoveResult{}, coded("AGENT_RPC_REQUEST_INVALID", err)
	}
	response, err := NewAgentDockerImagesServiceClient(connection).RemoveImage(ctx, payload)
	if err != nil {
		return docker.ImageRemoveResult{}, rpcError(err)
	}
	var result docker.ImageRemoveResult
	if err := decodeDashboardResponse(response, &result); err != nil {
		return docker.ImageRemoveResult{}, err
	}
	if result.ImageID == "" || !result.Removed {
		return docker.ImageRemoveResult{}, coded("AGENT_RPC_RESPONSE_INVALID", errors.New("image remove result is incomplete"))
	}
	return result, nil
}

func ReadComposeConfig(ctx context.Context, socketPath string, request ncpcompose.ReadRequest) (ncpcompose.ProjectConfig, error) {
	connection, err := dialSocket(socketPath)
	if err != nil {
		return ncpcompose.ProjectConfig{}, err
	}
	defer connection.Close()
	files := make([]any, 0, len(request.ConfigFiles))
	for _, path := range request.ConfigFiles {
		files = append(files, path)
	}
	payload, err := structpb.NewStruct(map[string]any{
		"project_id": request.ProjectID, "working_directory": request.WorkingDirectory, "config_files": files,
	})
	if err != nil {
		return ncpcompose.ProjectConfig{}, coded("AGENT_RPC_REQUEST_INVALID", err)
	}
	response, err := NewAgentComposeServiceClient(connection).ReadConfig(ctx, payload)
	if err != nil {
		return ncpcompose.ProjectConfig{}, rpcError(err)
	}
	var result ncpcompose.ProjectConfig
	if err := decodeDashboardResponse(response, &result); err != nil {
		return ncpcompose.ProjectConfig{}, err
	}
	if result.ProjectID == "" || result.Files == nil {
		return ncpcompose.ProjectConfig{}, coded("AGENT_RPC_RESPONSE_INVALID", errors.New("compose config response is incomplete"))
	}
	return result, nil
}

func ValidateComposeConfig(ctx context.Context, socketPath string, request ncpcompose.ValidateRequest) (ncpcompose.ValidationResult, error) {
	connection, err := dialSocket(socketPath)
	if err != nil {
		return ncpcompose.ValidationResult{}, err
	}
	defer connection.Close()
	payload, err := structpb.NewStruct(map[string]any{"path": request.Path, "content": request.Content})
	if err != nil {
		return ncpcompose.ValidationResult{}, coded("AGENT_RPC_REQUEST_INVALID", err)
	}
	response, err := NewAgentComposeServiceClient(connection).ValidateConfig(ctx, payload)
	if err != nil {
		return ncpcompose.ValidationResult{}, rpcError(err)
	}
	var result ncpcompose.ValidationResult
	if err := decodeDashboardResponse(response, &result); err != nil {
		return ncpcompose.ValidationResult{}, err
	}
	if !result.Valid || result.Services == nil {
		return ncpcompose.ValidationResult{}, coded("AGENT_RPC_RESPONSE_INVALID", errors.New("compose validation response is incomplete"))
	}
	return result, nil
}

func DeployComposeConfig(ctx context.Context, socketPath string, request ncpcompose.DeployRequest) (ncpcompose.DeployResult, error) {
	connection, err := dialSocket(socketPath)
	if err != nil {
		return ncpcompose.DeployResult{}, err
	}
	defer connection.Close()
	files := make([]any, 0, len(request.ConfigFiles))
	for _, configPath := range request.ConfigFiles {
		files = append(files, configPath)
	}
	payload, err := structpb.NewStruct(map[string]any{
		"project_id": request.ProjectID, "working_directory": request.WorkingDirectory,
		"config_files": files, "target_path": request.TargetPath, "content": request.Content,
	})
	if err != nil {
		return ncpcompose.DeployResult{}, coded("AGENT_RPC_REQUEST_INVALID", err)
	}
	response, err := NewAgentComposeServiceClient(connection).DeployConfig(ctx, payload)
	if err != nil {
		return ncpcompose.DeployResult{}, rpcError(err)
	}
	var result ncpcompose.DeployResult
	if err := decodeDashboardResponse(response, &result); err != nil {
		return ncpcompose.DeployResult{}, err
	}
	if !result.Completed || result.ProjectID == "" || result.BackupPath == "" {
		return ncpcompose.DeployResult{}, coded("AGENT_RPC_RESPONSE_INVALID", errors.New("compose deploy result is incomplete"))
	}
	return result, nil
}

func dialSocket(socketPath string) (*grpc.ClientConn, error) {
	if socketPath == "" {
		return nil, coded("AGENT_RPC_TARGET_INVALID", errors.New("socket path is required"))
	}
	connection, err := grpc.NewClient("unix://"+socketPath, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, coded("AGENT_RPC_CONNECTION_FAILED", err)
	}
	return connection, nil
}

func decodeAgentStatus(response *structpb.Struct) (AgentStatus, error) {
	if response == nil || len(response.GetFields()) < 4 {
		return AgentStatus{}, coded("AGENT_RPC_RESPONSE_INVALID", errors.New("unexpected status field count"))
	}
	protocolVersion, ok := stringField(response, "protocol_version")
	if !ok || protocolVersion == "" {
		return AgentStatus{}, coded("AGENT_RPC_RESPONSE_INVALID", errors.New("invalid protocol version"))
	}
	buildVersion, ok := stringField(response, "build_version")
	if !ok || buildVersion == "" {
		return AgentStatus{}, coded("AGENT_RPC_RESPONSE_INVALID", errors.New("invalid build version"))
	}
	transport, ok := stringField(response, "transport")
	if !ok || transport != "unix" {
		return AgentStatus{}, coded("AGENT_RPC_RESPONSE_INVALID", errors.New("invalid transport"))
	}
	value, ok := response.GetFields()["agent_euid"]
	if !ok || value.GetKind() == nil {
		return AgentStatus{}, coded("AGENT_RPC_RESPONSE_INVALID", errors.New("invalid agent euid"))
	}
	uid := value.GetNumberValue()
	if uid < 0 || uid > math.MaxInt || math.Trunc(uid) != uid {
		return AgentStatus{}, coded("AGENT_RPC_RESPONSE_INVALID", fmt.Errorf("invalid agent euid %v", uid))
	}
	return AgentStatus{
		ProtocolVersion: protocolVersion,
		BuildVersion:    buildVersion,
		AgentEUID:       int(uid),
		Transport:       transport,
	}, nil
}

func decodeCapabilities(response *structpb.Struct) (system.Capabilities, error) {
	if response == nil {
		return system.Capabilities{}, coded("AGENT_RPC_RESPONSE_INVALID", errors.New("capabilities response is required"))
	}

	encoded, err := json.Marshal(response.AsMap())
	if err != nil {
		return system.Capabilities{}, coded("AGENT_RPC_RESPONSE_INVALID", err)
	}
	var capabilities system.Capabilities
	if err := json.Unmarshal(encoded, &capabilities); err != nil {
		return system.Capabilities{}, coded("AGENT_RPC_RESPONSE_INVALID", err)
	}
	if capabilities.Architecture == "" {
		return system.Capabilities{}, coded("AGENT_RPC_RESPONSE_INVALID", errors.New("capabilities architecture is required"))
	}
	return capabilities, nil
}

func decodeSystemSummary(response *structpb.Struct) (system.Summary, error) {
	var summary system.Summary
	if err := decodeDashboardResponse(response, &summary); err != nil {
		return system.Summary{}, err
	}
	if summary.CollectedAt.IsZero() {
		return system.Summary{}, coded("AGENT_RPC_RESPONSE_INVALID", errors.New("summary collection time is required"))
	}
	return summary, nil
}

func decodeSystemDetails(response *structpb.Struct) (system.Details, error) {
	var details system.Details
	if err := decodeDashboardResponse(response, &details); err != nil {
		return system.Details{}, err
	}
	if details.CollectedAt.IsZero() {
		return system.Details{}, coded("AGENT_RPC_RESPONSE_INVALID", errors.New("details collection time is required"))
	}
	if details.Warnings == nil {
		details.Warnings = make([]string, 0)
	}
	if details.Hardware.Sensors == nil {
		details.Hardware.Sensors = make([]system.TemperatureProbe, 0)
	}
	if details.Network.Interfaces == nil {
		details.Network.Interfaces = make([]system.NetworkInterface, 0)
	}
	if details.Network.Routes == nil {
		details.Network.Routes = make([]system.NetworkRoute, 0)
	}
	if details.Network.DNSServers == nil {
		details.Network.DNSServers = make([]string, 0)
	}
	if details.Network.ListeningPorts == nil {
		details.Network.ListeningPorts = make([]system.ListeningPort, 0)
	}
	if details.Storage.Mounts == nil {
		details.Storage.Mounts = make([]system.MountDetails, 0)
	}
	if details.Storage.Disks == nil {
		details.Storage.Disks = make([]system.PhysicalDiskDetails, 0)
	}
	if details.Storage.RAID == nil {
		details.Storage.RAID = make([]system.RAIDDetails, 0)
	}
	if details.Proxy.System == nil {
		details.Proxy.System = make([]system.ProxyEvidence, 0)
	}
	if details.Proxy.Associations == nil {
		details.Proxy.Associations = make([]system.ProxyAssociation, 0)
	}
	if details.Control.Nodes == nil {
		details.Control.Nodes = make([]system.ControlNode, 0)
	}
	return details, nil
}

func decodeDockerInventory(response *structpb.Struct) (docker.Inventory, error) {
	var inventory docker.Inventory
	if err := decodeDashboardResponse(response, &inventory); err != nil {
		return docker.Inventory{}, err
	}
	if inventory.CollectedAt.IsZero() {
		return docker.Inventory{}, coded("AGENT_RPC_RESPONSE_INVALID", errors.New("inventory collection time is required"))
	}
	return inventory, nil
}

func decodeContainerActionResult(response *structpb.Struct) (docker.ContainerActionResult, error) {
	var result docker.ContainerActionResult
	if err := decodeDashboardResponse(response, &result); err != nil {
		return docker.ContainerActionResult{}, err
	}
	if result.ContainerID == "" || result.Name == "" || result.State == "" {
		return docker.ContainerActionResult{}, coded("AGENT_RPC_RESPONSE_INVALID", errors.New("container action result is incomplete"))
	}
	if _, err := docker.ParseContainerAction(string(result.Action)); err != nil {
		return docker.ContainerActionResult{}, coded("AGENT_RPC_RESPONSE_INVALID", err)
	}
	return result, nil
}

func decodeContainerLogsResult(response *structpb.Struct) (docker.ContainerLogsResult, error) {
	var result docker.ContainerLogsResult
	if err := decodeDashboardResponse(response, &result); err != nil {
		return docker.ContainerLogsResult{}, err
	}
	if result.ContainerID == "" || result.Tail < 1 || result.CollectedAt.IsZero() || result.Entries == nil {
		return docker.ContainerLogsResult{}, coded("AGENT_RPC_RESPONSE_INVALID", errors.New("container logs result is incomplete"))
	}
	return result, nil
}

func decodeDashboardResponse(response *structpb.Struct, destination any) error {
	if response == nil {
		return coded("AGENT_RPC_RESPONSE_INVALID", errors.New("dashboard response is required"))
	}
	encoded, err := json.Marshal(response.AsMap())
	if err != nil {
		return coded("AGENT_RPC_RESPONSE_INVALID", err)
	}
	if err := json.Unmarshal(encoded, destination); err != nil {
		return coded("AGENT_RPC_RESPONSE_INVALID", err)
	}
	return nil
}

func stringField(response *structpb.Struct, name string) (string, bool) {
	value, ok := response.GetFields()[name]
	if !ok || value.GetKind() == nil {
		return "", false
	}
	return value.GetStringValue(), value.GetStringValue() != ""
}

func contextError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return coded("AGENT_RPC_TIMEOUT", err)
	}
	return coded("AGENT_RPC_CANCELED", err)
}

func rpcError(err error) error {
	if errors.Is(err, context.Canceled) || grpcstatus.Code(err) == codes.Canceled {
		return coded("AGENT_RPC_CANCELED", err)
	}
	if errors.Is(err, context.DeadlineExceeded) || grpcstatus.Code(err) == codes.DeadlineExceeded {
		return coded("AGENT_RPC_TIMEOUT", err)
	}
	status := grpcstatus.Convert(err)
	if status.Code() == codes.Unimplemented {
		return coded("AGENT_PROTOCOL_MISMATCH", err)
	}
	switch status.Message() {
	case "DOCKER_CONTAINER_NOT_FOUND":
		return coded("DOCKER_CONTAINER_NOT_FOUND", err)
	case "DOCKER_CONTAINER_ACTION_FAILED":
		return coded("DOCKER_CONTAINER_ACTION_FAILED", err)
	case "DOCKER_CONTAINER_INSPECT_FAILED":
		return coded("DOCKER_CONTAINER_INSPECT_FAILED", err)
	}
	return coded("AGENT_RPC_UNAVAILABLE", err)
}
