package agentsocket

import (
	"context"
	"encoding/json"

	"github.com/Kkwans/nas-control-plane/internal/docker"
	"github.com/Kkwans/nas-control-plane/internal/system"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
)

type DashboardProvider interface {
	CollectSystemSummary(context.Context) (system.Summary, error)
	CollectSystemDetails(context.Context) (system.Details, error)
	CollectDockerInventory(context.Context) (docker.Inventory, error)
}

type dashboardProvider struct {
	systemSummary   *system.SummaryCollector
	systemDetails   *system.DetailsCollector
	dockerInventory *docker.InventoryCollector
}

func newLiveDashboardProvider() (DashboardProvider, error) {
	dockerInventory, err := docker.NewLiveInventoryCollector()
	if err != nil {
		return nil, err
	}
	return &dashboardProvider{
		systemSummary:   system.NewLiveSummaryCollector(),
		systemDetails:   system.NewDetailsCollector(),
		dockerInventory: dockerInventory,
	}, nil
}

func (p *dashboardProvider) CollectSystemSummary(ctx context.Context) (system.Summary, error) {
	return p.systemSummary.Collect(ctx)
}

func (p *dashboardProvider) CollectSystemDetails(ctx context.Context) (system.Details, error) {
	return p.systemDetails.Collect(ctx)
}

func (p *dashboardProvider) CollectDockerInventory(ctx context.Context) (docker.Inventory, error) {
	return p.dockerInventory.Collect(ctx)
}

type dashboardService struct {
	provider DashboardProvider
}

func newDashboardService(provider DashboardProvider) *dashboardService {
	return &dashboardService{provider: provider}
}

func (s *dashboardService) GetSystemSummary(ctx context.Context, _ *emptypb.Empty) (*structpb.Struct, error) {
	if err := ctx.Err(); err != nil {
		return nil, grpcstatus.Error(codes.Canceled, "AGENT_RPC_CANCELED")
	}
	if s.provider == nil {
		return nil, grpcstatus.Error(codes.Unavailable, "AGENT_SYSTEM_SUMMARY_UNAVAILABLE")
	}
	summary, err := s.provider.CollectSystemSummary(ctx)
	if err != nil {
		return nil, grpcstatus.Error(codes.Unavailable, "AGENT_SYSTEM_SUMMARY_UNAVAILABLE")
	}
	return dashboardStruct(summary, "AGENT_SYSTEM_SUMMARY_RESPONSE_INVALID")
}

func (s *dashboardService) GetSystemDetails(ctx context.Context, _ *emptypb.Empty) (*structpb.Struct, error) {
	if err := ctx.Err(); err != nil {
		return nil, grpcstatus.Error(codes.Canceled, "AGENT_RPC_CANCELED")
	}
	if s.provider == nil {
		return nil, grpcstatus.Error(codes.Unavailable, "AGENT_SYSTEM_DETAILS_UNAVAILABLE")
	}
	details, err := s.provider.CollectSystemDetails(ctx)
	if err != nil {
		return nil, grpcstatus.Error(codes.Unavailable, "AGENT_SYSTEM_DETAILS_UNAVAILABLE")
	}
	for index := range details.Control.Nodes {
		if details.Control.Nodes[index].ID == "agent" {
			details.Control.Nodes[index].Version = BuildVersion
		}
	}
	return dashboardStruct(details, "AGENT_SYSTEM_DETAILS_RESPONSE_INVALID")
}

func (s *dashboardService) GetDockerInventory(ctx context.Context, _ *emptypb.Empty) (*structpb.Struct, error) {
	if err := ctx.Err(); err != nil {
		return nil, grpcstatus.Error(codes.Canceled, "AGENT_RPC_CANCELED")
	}
	if s.provider == nil {
		return nil, grpcstatus.Error(codes.Unavailable, "AGENT_DOCKER_INVENTORY_UNAVAILABLE")
	}
	inventory, err := s.provider.CollectDockerInventory(ctx)
	if err != nil {
		return nil, grpcstatus.Error(codes.Unavailable, "AGENT_DOCKER_INVENTORY_UNAVAILABLE")
	}
	return dashboardStruct(inventory, "AGENT_DOCKER_INVENTORY_RESPONSE_INVALID")
}

func dashboardStruct(value any, errorCode string) (*structpb.Struct, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, grpcstatus.Error(codes.Internal, errorCode)
	}
	values := make(map[string]any)
	if err := json.Unmarshal(encoded, &values); err != nil {
		return nil, grpcstatus.Error(codes.Internal, errorCode)
	}
	response, err := structpb.NewStruct(values)
	if err != nil {
		return nil, grpcstatus.Error(codes.Internal, errorCode)
	}
	return response, nil
}
