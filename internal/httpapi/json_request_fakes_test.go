package httpapi

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/agentsocket"
	ncpcompose "github.com/Kkwans/nas-control-plane/internal/compose"
	"github.com/Kkwans/nas-control-plane/internal/controlstore"
	ncpdatabase "github.com/Kkwans/nas-control-plane/internal/database"
	"github.com/Kkwans/nas-control-plane/internal/docker"
	"github.com/Kkwans/nas-control-plane/internal/filesystem"
	"github.com/Kkwans/nas-control-plane/internal/journal"
	"github.com/Kkwans/nas-control-plane/internal/system"
)

// 纯计数替身覆盖所有注入接口；意外调用也不会访问 Socket、Docker 或 NAS。
type jsonMIMERouteAgent struct{ calls atomic.Int32 }

func (f *jsonMIMERouteAgent) Probe(context.Context, string) (agentsocket.AgentStatus, error) {
	f.calls.Add(1)
	var value0 agentsocket.AgentStatus
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteAgent) CollectCapabilities(context.Context, string) (system.Capabilities, error) {
	f.calls.Add(1)
	var value0 system.Capabilities
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteAgent) CollectSystemSummary(context.Context, string) (system.Summary, error) {
	f.calls.Add(1)
	var value0 system.Summary
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteAgent) CollectSystemDetails(context.Context, string) (system.Details, error) {
	f.calls.Add(1)
	var value0 system.Details
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteAgent) CollectDockerInventory(context.Context, string) (docker.Inventory, error) {
	f.calls.Add(1)
	var value0 docker.Inventory
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteAgent) ControlContainer(context.Context, string, docker.ContainerActionRequest) (docker.ContainerActionResult, error) {
	f.calls.Add(1)
	var value0 docker.ContainerActionResult
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteAgent) ReadContainerLogs(context.Context, string, docker.ContainerLogsRequest) (docker.ContainerLogsResult, error) {
	f.calls.Add(1)
	var value0 docker.ContainerLogsResult
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteAgent) QueryJournal(context.Context, string, journal.Query) (journal.Page, error) {
	f.calls.Add(1)
	var value0 journal.Page
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteAgent) CollectDNSCapability(context.Context, string) (system.DNSCapability, error) {
	f.calls.Add(1)
	var value0 system.DNSCapability
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteAgent) PreviewDNSChange(context.Context, string, system.DNSChangeRequest) (system.DNSChangePreview, error) {
	f.calls.Add(1)
	var value0 system.DNSChangePreview
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteAgent) ConfirmDNSChange(context.Context, string, system.DNSChangeConfirmation) (system.DNSChangeResult, error) {
	f.calls.Add(1)
	var value0 system.DNSChangeResult
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteAgent) RollbackDNSChange(context.Context, string, system.DNSRollbackRequest) (system.DNSChangeResult, error) {
	f.calls.Add(1)
	var value0 system.DNSChangeResult
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteAgent) GetPublicEgressCapability(context.Context, string) (system.PublicEgressCapability, error) {
	f.calls.Add(1)
	var value0 system.PublicEgressCapability
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteAgent) DetectPublicEgress(context.Context, string) (system.PublicEgressResult, error) {
	f.calls.Add(1)
	var value0 system.PublicEgressResult
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteAgent) ProbeMihomo(context.Context, string) (system.MihomoCapability, error) {
	f.calls.Add(1)
	var value0 system.MihomoCapability
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteAgent) InvokeMihomo(context.Context, string, system.MihomoInvokeRequest) (system.MihomoInvokeResult, error) {
	f.calls.Add(1)
	var value0 system.MihomoInvokeResult
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteAgent) InspectMihomo(context.Context, string, bool) (system.MihomoInspection, error) {
	f.calls.Add(1)
	var value0 system.MihomoInspection
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteAgent) DiscoverDatabases(context.Context, string) (ncpdatabase.Discovery, error) {
	f.calls.Add(1)
	var value0 ncpdatabase.Discovery
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteAgent) CatalogDatabase(context.Context, string, ncpdatabase.CatalogRequest) (ncpdatabase.Catalog, error) {
	f.calls.Add(1)
	var value0 ncpdatabase.Catalog
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteAgent) QueryDatabase(context.Context, string, ncpdatabase.QueryRequest) (ncpdatabase.QueryResult, error) {
	f.calls.Add(1)
	var value0 ncpdatabase.QueryResult
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteAgent) ReadDatabaseRows(context.Context, string, ncpdatabase.RowsRequest) (ncpdatabase.RowsResult, error) {
	f.calls.Add(1)
	var value0 ncpdatabase.RowsResult
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteAgent) InsertDatabaseRow(context.Context, string, ncpdatabase.InsertRequest) (ncpdatabase.MutationResult, error) {
	f.calls.Add(1)
	var value0 ncpdatabase.MutationResult
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteAgent) UpdateDatabaseRow(context.Context, string, ncpdatabase.UpdateRequest) (ncpdatabase.MutationResult, error) {
	f.calls.Add(1)
	var value0 ncpdatabase.MutationResult
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteAgent) DeleteDatabaseRow(context.Context, string, ncpdatabase.DeleteRequest) (ncpdatabase.MutationResult, error) {
	f.calls.Add(1)
	var value0 ncpdatabase.MutationResult
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteAgent) TestDatabaseConnection(context.Context, string, ncpdatabase.Connection) (ncpdatabase.ConnectionDiagnostic, error) {
	f.calls.Add(1)
	var value0 ncpdatabase.ConnectionDiagnostic
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteAgent) ListDockerImages(context.Context, string) (docker.ImageInventory, error) {
	f.calls.Add(1)
	var value0 docker.ImageInventory
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteAgent) SearchDockerHub(context.Context, string, docker.HubSearchRequest) (docker.HubSearchResult, error) {
	f.calls.Add(1)
	var value0 docker.HubSearchResult
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteAgent) ListDockerHubTags(context.Context, string, docker.HubTagsRequest) (docker.HubTagsResult, error) {
	f.calls.Add(1)
	var value0 docker.HubTagsResult
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteAgent) PullDockerImage(context.Context, string, docker.ImagePullRequest) (docker.ImagePullResult, error) {
	f.calls.Add(1)
	var value0 docker.ImagePullResult
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteAgent) RemoveDockerImage(context.Context, string, docker.ImageRemoveRequest) (docker.ImageRemoveResult, error) {
	f.calls.Add(1)
	var value0 docker.ImageRemoveResult
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteAgent) RemoveDockerImages(context.Context, string, docker.ImageRemoveBatchRequest) (docker.ImageRemoveBatchResult, error) {
	f.calls.Add(1)
	var value0 docker.ImageRemoveBatchResult
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteAgent) ReadComposeConfig(context.Context, string, ncpcompose.ReadRequest) (ncpcompose.ProjectConfig, error) {
	f.calls.Add(1)
	var value0 ncpcompose.ProjectConfig
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteAgent) ValidateComposeConfig(context.Context, string, ncpcompose.ValidateRequest) (ncpcompose.ValidationResult, error) {
	f.calls.Add(1)
	var value0 ncpcompose.ValidationResult
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteAgent) DeployComposeConfig(context.Context, string, ncpcompose.DeployRequest) (ncpcompose.DeployResult, error) {
	f.calls.Add(1)
	var value0 ncpcompose.DeployResult
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteAgent) CreateDockerContainer(context.Context, string, docker.ContainerCreateRequest) (docker.ContainerCreateResult, error) {
	f.calls.Add(1)
	var value0 docker.ContainerCreateResult
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteAgent) ControlStandaloneProject(context.Context, string, docker.ProjectActionRequest) (docker.ProjectActionResult, error) {
	f.calls.Add(1)
	var value0 docker.ProjectActionResult
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteAgent) ControlComposeProject(context.Context, string, ncpcompose.LifecycleRequest) (ncpcompose.LifecycleResult, error) {
	f.calls.Add(1)
	var value0 ncpcompose.LifecycleResult
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteAgent) DeleteDockerProject(context.Context, string, docker.ProjectDeleteRequest) (docker.ProjectDeleteResult, error) {
	f.calls.Add(1)
	var value0 docker.ProjectDeleteResult
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteAgent) CollectDockerResources(context.Context, string) (docker.Resources, error) {
	f.calls.Add(1)
	var value0 docker.Resources
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteAgent) ListPath(context.Context, string, filesystem.Request) (filesystem.Page, error) {
	f.calls.Add(1)
	var value0 filesystem.Page
	var value1 error
	return value0, value1
}

type jsonMIMERouteStore struct{ calls atomic.Int32 }

func (f *jsonMIMERouteStore) Preferences(context.Context, int64) (controlstore.Preferences, error) {
	f.calls.Add(1)
	var value0 controlstore.Preferences
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteStore) UpdatePreferences(context.Context, int64, controlstore.Preferences) (controlstore.Preferences, error) {
	f.calls.Add(1)
	var value0 controlstore.Preferences
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteStore) DatabaseProjectPreferences(context.Context) ([]controlstore.DatabaseProjectPreference, error) {
	f.calls.Add(1)
	var value0 []controlstore.DatabaseProjectPreference
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteStore) SetDatabaseProjectPreference(context.Context, controlstore.DatabaseProjectPreference) (controlstore.DatabaseProjectPreference, error) {
	f.calls.Add(1)
	var value0 controlstore.DatabaseProjectPreference
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteStore) SiteProfiles(context.Context) ([]controlstore.SiteProfile, error) {
	f.calls.Add(1)
	var value0 []controlstore.SiteProfile
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteStore) UpsertSiteProfile(context.Context, controlstore.SiteProfile) (controlstore.SiteProfile, error) {
	f.calls.Add(1)
	var value0 controlstore.SiteProfile
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteStore) RecordSiteVisit(context.Context, string) (time.Time, error) {
	f.calls.Add(1)
	var value0 time.Time
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteStore) ComposeDraft(context.Context, string, string) (controlstore.ComposeDraft, error) {
	f.calls.Add(1)
	var value0 controlstore.ComposeDraft
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteStore) SaveComposeDraft(context.Context, controlstore.ComposeDraft) (controlstore.ComposeDraft, error) {
	f.calls.Add(1)
	var value0 controlstore.ComposeDraft
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteStore) RecordComposeRevision(context.Context, controlstore.ComposeRevision) (controlstore.ComposeRevision, error) {
	f.calls.Add(1)
	var value0 controlstore.ComposeRevision
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteStore) ComposeRevisions(context.Context, string, int) ([]controlstore.ComposeRevision, error) {
	f.calls.Add(1)
	var value0 []controlstore.ComposeRevision
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteStore) MetricSamples(context.Context, time.Time) ([]controlstore.MetricSample, error) {
	f.calls.Add(1)
	var value0 []controlstore.MetricSample
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteStore) ListPreference(context.Context, int64, string) (controlstore.ListPreference, error) {
	f.calls.Add(1)
	var value0 controlstore.ListPreference
	var value1 error
	return value0, value1
}

func (f *jsonMIMERouteStore) UpdateListPreference(context.Context, int64, controlstore.ListPreference) (controlstore.ListPreference, error) {
	f.calls.Add(1)
	var value0 controlstore.ListPreference
	var value1 error
	return value0, value1
}
