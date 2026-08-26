package httpapi

import (
	"context"

	"github.com/Kkwans/nas-control-plane/internal/agentsocket"
	ncpcompose "github.com/Kkwans/nas-control-plane/internal/compose"
	"github.com/Kkwans/nas-control-plane/internal/database"
	"github.com/Kkwans/nas-control-plane/internal/docker"
	"github.com/Kkwans/nas-control-plane/internal/journal"
	"github.com/Kkwans/nas-control-plane/internal/system"
)

// socketAgentClient is the default HTTP-side adapter for the root Agent.
// Keeping transport wiring in its own file makes handler.go responsible only
// for dependency construction and route registration.
type socketAgentClient struct{}

func (socketAgentClient) Probe(ctx context.Context, socketPath string) (agentsocket.AgentStatus, error) {
	return agentsocket.Probe(ctx, socketPath)
}

func (socketAgentClient) CollectCapabilities(ctx context.Context, socketPath string) (system.Capabilities, error) {
	return agentsocket.CollectCapabilities(ctx, socketPath)
}

func (socketAgentClient) CollectSystemSummary(ctx context.Context, socketPath string) (system.Summary, error) {
	return agentsocket.CollectSystemSummary(ctx, socketPath)
}

func (socketAgentClient) CollectSystemDetails(ctx context.Context, socketPath string) (system.Details, error) {
	return agentsocket.CollectSystemDetails(ctx, socketPath)
}

func (socketAgentClient) CollectDockerInventory(ctx context.Context, socketPath string) (docker.Inventory, error) {
	return agentsocket.CollectDockerInventory(ctx, socketPath)
}

func (socketAgentClient) ProbeWeb(ctx context.Context, socketPath string, targetURL string) (agentsocket.WebProbeResult, error) {
	return agentsocket.ProbeWeb(ctx, socketPath, targetURL)
}

func (socketAgentClient) DiscoverHostSiteCandidates(ctx context.Context, socketPath string) ([]docker.HostSiteCandidate, error) {
	return agentsocket.DiscoverHostSiteCandidates(ctx, socketPath)
}

func (socketAgentClient) ControlContainer(ctx context.Context, socketPath string, request docker.ContainerActionRequest) (docker.ContainerActionResult, error) {
	return agentsocket.ControlContainer(ctx, socketPath, request)
}

func (socketAgentClient) ReadContainerLogs(ctx context.Context, socketPath string, request docker.ContainerLogsRequest) (docker.ContainerLogsResult, error) {
	return agentsocket.ReadContainerLogs(ctx, socketPath, request)
}

func (socketAgentClient) QueryJournal(ctx context.Context, socketPath string, query journal.Query) (journal.Page, error) {
	return agentsocket.QueryJournal(ctx, socketPath, query)
}

func (socketAgentClient) ListDockerImages(ctx context.Context, socketPath string) (docker.ImageInventory, error) {
	return agentsocket.ListDockerImages(ctx, socketPath)
}

func (socketAgentClient) SearchDockerHub(ctx context.Context, socketPath string, request docker.HubSearchRequest) (docker.HubSearchResult, error) {
	return agentsocket.SearchDockerHub(ctx, socketPath, request)
}

func (socketAgentClient) ListDockerHubTags(ctx context.Context, socketPath string, request docker.HubTagsRequest) (docker.HubTagsResult, error) {
	return agentsocket.ListDockerHubTags(ctx, socketPath, request)
}

func (socketAgentClient) PullDockerImage(ctx context.Context, socketPath string, request docker.ImagePullRequest) (docker.ImagePullResult, error) {
	return agentsocket.PullDockerImage(ctx, socketPath, request)
}

func (socketAgentClient) PullDockerImageWithProgress(ctx context.Context, socketPath string, request docker.ImagePullRequest, onProgress func(docker.ImagePullProgress)) (docker.ImagePullResult, error) {
	return agentsocket.PullDockerImageWithProgress(ctx, socketPath, request, onProgress)
}

func (socketAgentClient) RemoveDockerImage(ctx context.Context, socketPath string, request docker.ImageRemoveRequest) (docker.ImageRemoveResult, error) {
	return agentsocket.RemoveDockerImage(ctx, socketPath, request)
}

func (socketAgentClient) ReadComposeConfig(ctx context.Context, socketPath string, request ncpcompose.ReadRequest) (ncpcompose.ProjectConfig, error) {
	return agentsocket.ReadComposeConfig(ctx, socketPath, request)
}

func (socketAgentClient) ValidateComposeConfig(ctx context.Context, socketPath string, request ncpcompose.ValidateRequest) (ncpcompose.ValidationResult, error) {
	return agentsocket.ValidateComposeConfig(ctx, socketPath, request)
}

func (socketAgentClient) DeployComposeConfig(ctx context.Context, socketPath string, request ncpcompose.DeployRequest) (ncpcompose.DeployResult, error) {
	return agentsocket.DeployComposeConfig(ctx, socketPath, request)
}

func (socketAgentClient) DiscoverDatabases(ctx context.Context, socketPath string) (database.Discovery, error) {
	return agentsocket.DiscoverDatabases(ctx, socketPath)
}

func (socketAgentClient) CatalogDatabase(ctx context.Context, socketPath string, request database.CatalogRequest) (database.Catalog, error) {
	return agentsocket.CatalogDatabase(ctx, socketPath, request)
}

func (socketAgentClient) QueryDatabase(ctx context.Context, socketPath string, request database.QueryRequest) (database.QueryResult, error) {
	return agentsocket.QueryDatabase(ctx, socketPath, request)
}

func (socketAgentClient) ReadDatabaseRows(ctx context.Context, socketPath string, request database.RowsRequest) (database.RowsResult, error) {
	return agentsocket.ReadDatabaseRows(ctx, socketPath, request)
}

func (socketAgentClient) InsertDatabaseRow(ctx context.Context, socketPath string, request database.InsertRequest) (database.MutationResult, error) {
	return agentsocket.InsertDatabaseRow(ctx, socketPath, request)
}

func (socketAgentClient) UpdateDatabaseRow(ctx context.Context, socketPath string, request database.UpdateRequest) (database.MutationResult, error) {
	return agentsocket.UpdateDatabaseRow(ctx, socketPath, request)
}

func (socketAgentClient) DeleteDatabaseRow(ctx context.Context, socketPath string, request database.DeleteRequest) (database.MutationResult, error) {
	return agentsocket.DeleteDatabaseRow(ctx, socketPath, request)
}
