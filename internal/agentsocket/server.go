package agentsocket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	ncpcompose "github.com/Kkwans/nas-control-plane/internal/compose"
	ncpdatabase "github.com/Kkwans/nas-control-plane/internal/database"
	"github.com/Kkwans/nas-control-plane/internal/docker"
	"github.com/Kkwans/nas-control-plane/internal/journal"
	"github.com/Kkwans/nas-control-plane/internal/system"
	"github.com/Kkwans/nas-control-plane/internal/terminal"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	DefaultSocketPath          = "/run/ncp/agent.sock"
	defaultSocketMode          = 0o660
	defaultSocketDirectoryMode = 0o750
	ProtocolVersion            = "p0-v2"
)

// BuildVersion is injected for release builds through -ldflags. Keeping a
// readable fallback makes local builds and tests diagnosable as well.
var BuildVersion = "dev"

type SocketConfig struct {
	SocketPath          string
	SocketGroup         string
	SocketMode          os.FileMode
	EnableTerminalPOC   bool
	TerminalManager     *terminal.Manager
	DashboardProvider   DashboardProvider
	DockerControl       DockerControlProvider
	DockerLogs          DockerLogsProvider
	DockerImages        DockerImageProvider
	OutboundProxy       string
	ComposeRegistryPath string
	Compose             ComposeProvider
	Database            DatabaseProvider
	Journal             JournalProvider
	System              SystemProvider
	Proxy               ProxyProvider
}

func Serve(ctx context.Context, config SocketConfig) error {
	listener, cleanup, err := listenUnixSocket(config)
	if err != nil {
		return err
	}
	defer cleanup()
	dashboardProvider := config.DashboardProvider
	if dashboardProvider == nil {
		dashboardProvider, err = newLiveDashboardProvider()
		if err != nil {
			return coded("AGENT_DASHBOARD_INITIALIZATION_FAILED", err)
		}
	}
	dockerControl := config.DockerControl
	if dockerControl == nil {
		controller, controllerErr := docker.NewLiveContainerController()
		err = controllerErr
		if err != nil {
			return coded("AGENT_DOCKER_CONTROL_INITIALIZATION_FAILED", err)
		}
		if registryPath := strings.TrimSpace(config.ComposeRegistryPath); registryPath != "" {
			if !filepath.IsAbs(registryPath) || filepath.Clean(registryPath) != registryPath {
				return coded("AGENT_DOCKER_REGISTRY_PATH_INVALID", errors.New("compose registry path must be a clean absolute path"))
			}
			controller.SetComposeRegistryPath(registryPath)
		}
		dockerControl = controller
	}
	dockerLogs := config.DockerLogs
	if dockerLogs == nil {
		dockerLogs, err = docker.NewLiveContainerLogCollector()
		if err != nil {
			return coded("AGENT_DOCKER_LOGS_INITIALIZATION_FAILED", err)
		}
	}
	dockerImages := config.DockerImages
	if dockerImages == nil {
		dockerImages, err = docker.NewLiveImageManagerWithProxy(config.OutboundProxy)
		if err != nil {
			return coded("AGENT_DOCKER_IMAGES_INITIALIZATION_FAILED", err)
		}
	}
	composeProvider := config.Compose
	if composeProvider == nil {
		composeProvider = ncpcompose.NewManager(nil)
	}
	databaseProvider := config.Database
	if databaseProvider == nil {
		databaseProvider = ncpdatabase.NewManager()
	}
	journalProvider := config.Journal
	if journalProvider == nil {
		journalProvider = journal.NewReader(journal.OSRunner{})
	}
	systemProvider := config.System
	if systemProvider == nil {
		systemEnvironment := system.NewOSEnvironment()
		var dnsController system.DNSChangeController
		if dnsCapability := system.ProbeDNS(ctx, systemEnvironment); dnsCapability.Backend == system.DNSBackendStaticResolv {
			staticController, controllerErr := system.NewStaticResolvDNSController("/etc/resolv.conf", "/var/lib/ncp/dns")
			if controllerErr == nil {
				dnsController = staticController
			}
		}
		systemProvider, err = NewLiveSystemProviderWithProxy(
			systemEnvironment,
			os.Getenv("NCP_PUBLIC_EGRESS_ENDPOINT"),
			config.OutboundProxy,
			dnsController,
		)
		if err != nil {
			return coded("AGENT_PUBLIC_EGRESS_INITIALIZATION_FAILED", err)
		}
	}
	proxyProvider := config.Proxy
	if proxyProvider == nil {
		endpoint := strings.TrimSpace(os.Getenv("NCP_MIHOMO_CONTROLLER_ENDPOINT"))
		if endpoint == "" {
			endpoint = strings.TrimSpace(os.Getenv("MIHOMO_CONTROLLER_ENDPOINT"))
		}
		if endpoint != "" {
			proxyProvider, err = NewLiveProxyProvider(system.NewOSEnvironment(), endpoint, os.Getenv("NCP_MIHOMO_CONTROLLER_TOKEN"))
			if err != nil {
				return coded("AGENT_MIHOMO_INITIALIZATION_FAILED", err)
			}
		}
	}

	grpcServer := grpc.NewServer()
	RegisterAgentProbeServiceServer(grpcServer, newStatusService())
	RegisterAgentDashboardServiceServer(grpcServer, newDashboardService(dashboardProvider))
	RegisterAgentDockerControlServiceServer(grpcServer, newDockerControlService(dockerControl, composeProvider))
	RegisterAgentDockerLogsServiceServer(grpcServer, newDockerLogsService(dockerLogs))
	RegisterAgentDockerImagesServiceServer(grpcServer, newDockerImageService(dockerImages))
	RegisterAgentComposeServiceServer(grpcServer, newComposeService(composeProvider))
	RegisterAgentDatabaseServiceServer(grpcServer, newDatabaseService(databaseProvider))
	RegisterAgentJournalServiceServer(grpcServer, newJournalService(journalProvider))
	RegisterAgentWebProbeServiceServer(grpcServer, newWebProbeService())
	RegisterAgentSystemServiceServer(grpcServer, NewSystemService(systemProvider))
	RegisterAgentProxyServiceServer(grpcServer, NewProxyService(proxyProvider))
	if config.EnableTerminalPOC {
		manager := config.TerminalManager
		if manager == nil {
			manager, err = terminal.NewPOCManager()
			if err != nil {
				cleanup()
				return coded("TERMINAL_POC_INITIALIZATION_FAILED", err)
			}
		}
		RegisterAgentTerminalPOCServiceServer(grpcServer, newTerminalPOCService(manager))
	}
	stopped := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			grpcServer.Stop()
		case <-stopped:
		}
	}()

	err = grpcServer.Serve(listener)
	close(stopped)
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return coded("AGENT_SOCKET_SERVE_FAILED", err)
	}
	return nil
}

func listenUnixSocket(config SocketConfig) (net.Listener, func(), error) {
	config, err := normalizedSocketConfig(config)
	if err != nil {
		return nil, nil, err
	}
	if _, err := os.Lstat(config.SocketPath); err == nil {
		return nil, nil, coded("AGENT_SOCKET_PATH_OCCUPIED", errors.New("socket path already exists"))
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, coded("AGENT_SOCKET_PATH_CHECK_FAILED", err)
	}

	directory := filepath.Dir(config.SocketPath)
	if err := os.MkdirAll(directory, defaultSocketDirectoryMode); err != nil {
		return nil, nil, coded("AGENT_SOCKET_DIRECTORY_FAILED", err)
	}

	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: config.SocketPath, Net: "unix"})
	if err != nil {
		return nil, nil, coded("AGENT_SOCKET_LISTEN_FAILED", err)
	}
	cleanup := func() {
		_ = listener.Close()
		info, statErr := os.Lstat(config.SocketPath)
		if statErr == nil && info.Mode()&os.ModeSocket != 0 {
			_ = os.Remove(config.SocketPath)
		}
	}

	if err := applySocketPermissions(directory, config); err != nil {
		cleanup()
		return nil, nil, err
	}
	return listener, cleanup, nil
}

func normalizedSocketConfig(config SocketConfig) (SocketConfig, error) {
	if config.SocketPath == "" {
		config.SocketPath = DefaultSocketPath
	}
	if !filepath.IsAbs(config.SocketPath) {
		return SocketConfig{}, coded("AGENT_SOCKET_PATH_INVALID", errors.New("socket path must be absolute"))
	}
	if config.SocketMode == 0 {
		config.SocketMode = defaultSocketMode
	}
	if config.SocketMode.Perm() != defaultSocketMode {
		return SocketConfig{}, coded("AGENT_SOCKET_MODE_INVALID", fmt.Errorf("socket mode must be %o", defaultSocketMode))
	}
	return config, nil
}

func applySocketPermissions(directory string, config SocketConfig) error {
	groupID, err := socketGroupID(config.SocketGroup)
	if err != nil {
		return err
	}
	if groupID >= 0 {
		if err := os.Chown(directory, -1, groupID); err != nil {
			return coded("AGENT_SOCKET_DIRECTORY_OWNER_FAILED", err)
		}
	}
	if err := os.Chmod(directory, defaultSocketDirectoryMode); err != nil {
		return coded("AGENT_SOCKET_DIRECTORY_MODE_FAILED", err)
	}
	if groupID >= 0 {
		if err := os.Chown(config.SocketPath, -1, groupID); err != nil {
			return coded("AGENT_SOCKET_OWNER_FAILED", err)
		}
	}
	if err := os.Chmod(config.SocketPath, config.SocketMode); err != nil {
		return coded("AGENT_SOCKET_MODE_FAILED", err)
	}
	return nil
}

func socketGroupID(groupName string) (int, error) {
	if groupName == "" {
		return -1, nil
	}
	group, err := user.LookupGroup(groupName)
	if err != nil {
		return -1, coded("AGENT_SOCKET_GROUP_INVALID", err)
	}
	groupID, err := strconv.Atoi(group.Gid)
	if err != nil {
		return -1, coded("AGENT_SOCKET_GROUP_INVALID", err)
	}
	return groupID, nil
}

func ValidateServerSocketGroup(groupName string) error {
	if groupName == "" {
		return coded("AGENT_SOCKET_GROUP_REQUIRED", errors.New("socket group is required"))
	}
	_, err := socketGroupID(groupName)
	return err
}

// ValidateServerSocketPath 保持常规 Agent 的固定 Socket 边界；仅显式 P0 终端实测可使用临时路径。
func ValidateServerSocketPath(socketPath string, terminalPOC bool) error {
	if socketPath == "" || socketPath == DefaultSocketPath || terminalPOC {
		return nil
	}
	return coded("AGENT_SOCKET_PATH_POC_ONLY", errors.New("alternate socket path requires terminal POC mode"))
}

type capabilityCollector interface {
	Collect(context.Context) (system.Capabilities, error)
}

type statusService struct {
	collector capabilityCollector
}

func newStatusService() *statusService {
	return newStatusServiceWithCollector(system.NewProbe(system.NewOSEnvironment()))
}

func newStatusServiceWithCollector(collector capabilityCollector) *statusService {
	return &statusService{collector: collector}
}

func (statusService) GetStatus(ctx context.Context, _ *emptypb.Empty) (*structpb.Struct, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return structpb.NewStruct(map[string]any{
		"protocol_version": ProtocolVersion,
		"build_version":    BuildVersion,
		"agent_euid":       os.Geteuid(),
		"transport":        "unix",
	})
}

func (service *statusService) GetCapabilities(ctx context.Context, _ *emptypb.Empty) (*structpb.Struct, error) {
	if err := ctx.Err(); err != nil {
		return nil, grpcstatus.Error(codes.Canceled, "AGENT_RPC_CANCELED")
	}
	capabilities, err := service.collector.Collect(ctx)
	if err != nil {
		return nil, grpcstatus.Error(codes.Unavailable, "AGENT_CAPABILITIES_UNAVAILABLE")
	}

	encoded, err := json.Marshal(capabilities)
	if err != nil {
		return nil, grpcstatus.Error(codes.Internal, "AGENT_CAPABILITIES_RESPONSE_INVALID")
	}
	values := make(map[string]any)
	if err := json.Unmarshal(encoded, &values); err != nil {
		return nil, grpcstatus.Error(codes.Internal, "AGENT_CAPABILITIES_RESPONSE_INVALID")
	}
	response, err := structpb.NewStruct(values)
	if err != nil {
		return nil, grpcstatus.Error(codes.Internal, "AGENT_CAPABILITIES_RESPONSE_INVALID")
	}
	return response, nil
}
