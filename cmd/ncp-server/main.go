package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/agentsocket"
	"github.com/Kkwans/nas-control-plane/internal/auth"
	"github.com/Kkwans/nas-control-plane/internal/controlstore"
	ncpdatabase "github.com/Kkwans/nas-control-plane/internal/database"
	"github.com/Kkwans/nas-control-plane/internal/docker"
	"github.com/Kkwans/nas-control-plane/internal/dockerrealtime"
	"github.com/Kkwans/nas-control-plane/internal/httpapi"
	"github.com/Kkwans/nas-control-plane/internal/monitoring"
	"github.com/Kkwans/nas-control-plane/internal/system"
)

const (
	agentProbeTimeout        = 5 * time.Second
	httpShutdownTimeout      = 10 * time.Second
	defaultHTTPListenAddress = "127.0.0.1:8750"
	defaultDatabasePath      = "/var/lib/ncp-server/ncp.sqlite"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if len(os.Args) > 1 && os.Args[1] == "--version" {
		if err := writeBuildVersion(os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "NCP_SERVER_VERSION_OUTPUT_FAILED")
			os.Exit(1)
		}
		return
	}

	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "NCP_SERVER_COMMAND_UNKNOWN")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "agent-probe":
		if err := runAgentProbe(ctx, os.Args[2:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, agentProbeErrorCode(err))
			os.Exit(1)
		}
	case "database-key-init":
		if err := runDatabaseKeyInit(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "NCP_SERVER_DATABASE_KEY_INIT_FAILED")
			os.Exit(1)
		}
	case "serve":
		if err := runHTTPServer(ctx, os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "NCP_SERVER_HTTP_FAILED")
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, "NCP_SERVER_COMMAND_UNKNOWN")
		os.Exit(1)
	}
}

func writeBuildVersion(output io.Writer) error {
	_, err := fmt.Fprintln(output, agentsocket.BuildVersion)
	return err
}

// runDatabaseKeyInit is an explicit installation-time operation. It never
// prints key material and refuses to replace an existing key ring, so an
// operator cannot accidentally make saved credentials undecryptable.
func runDatabaseKeyInit(args []string) error {
	flags := flag.NewFlagSet("database-key-init", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("path", "", "数据库凭据密钥环路径")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*path) == "" {
		return errors.New("database key path is required")
	}
	_, err := ncpdatabase.CreateFileKeyProvider(*path)
	return err
}

func runAgentProbe(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("agent-probe", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected agent-probe arguments")
	}

	requestContext, cancel := context.WithTimeout(ctx, agentProbeTimeout)
	defer cancel()
	status, err := agentsocket.Probe(requestContext, agentsocket.DefaultSocketPath)
	if err != nil {
		return err
	}

	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(status)
}

func agentProbeErrorCode(err error) string {
	if code := agentsocket.ErrorCode(err); code != "" {
		return code
	}
	return "NCP_SERVER_AGENT_PROBE_FAILED"
}

func runHTTPServer(ctx context.Context, args []string) (resultErr error) {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	listenAddress := flags.String("listen", defaultHTTPListenAddress, "HTTP 监听地址")
	agentSocketPath := flags.String("agent-socket", agentsocket.DefaultSocketPath, "Agent Unix Socket 路径")
	databasePath := flags.String("database", defaultDatabasePath, "SQLite 数据库路径")
	databaseKeyPath := flags.String("database-key", os.Getenv("NCP_DATABASE_KEY_PATH"), "数据库凭据密钥环路径；未配置时不启用自动凭据保存")
	secureCookie := flags.Bool("secure-cookie", false, "仅通过 HTTPS 发送登录 Cookie")
	terminalEnabled := flags.Bool("terminal", true, "启用主机与容器终端 WebSocket 通道")
	legacyTerminalPOC := flags.Bool("terminal-poc", true, "兼容旧版本的终端开关（已弃用）")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected serve arguments")
	}
	// Existing service units may still pass --terminal-poc. When that legacy
	// flag is explicitly present, preserve its boolean semantics for one
	// release while making --terminal the documented option.
	flags.Visit(func(flag *flag.Flag) {
		if flag.Name == "terminal-poc" {
			*terminalEnabled = *legacyTerminalPOC
		}
	})
	// 持锁覆盖初始化、恢复以及全部 store 关闭。
	if err := os.MkdirAll(filepath.Dir(*databasePath), 0700); err != nil {
		return err
	}
	unlockJobs, err := lockJobDatabase(*databasePath)
	if err != nil {
		return err
	}
	defer unlockJobs()
	authService, err := auth.Open(*databasePath, auth.Options{})
	if err != nil {
		return err
	}
	defer authService.Close()
	controlStore, err := controlstore.Open(*databasePath)
	if err != nil {
		return err
	}
	defer controlStore.Close()

	var databaseConnections *ncpdatabase.ConnectionCoordinator
	var credentialStore *ncpdatabase.SQLiteCredentialStore
	if keyPath := filepath.Clean(*databaseKeyPath); strings.TrimSpace(*databaseKeyPath) != "" {
		credentialStore, err = ncpdatabase.OpenCredentialStore(*databasePath)
		if err != nil {
			return err
		}
		defer credentialStore.Close()
		keyProvider, keyErr := ncpdatabase.NewFileKeyProvider(keyPath)
		if keyErr != nil {
			return keyErr
		}
		vault, vaultErr := ncpdatabase.NewCredentialVault(credentialStore, keyProvider)
		if vaultErr != nil {
			return vaultErr
		}
		if migrateErr := vault.Migrate(ctx); migrateErr != nil {
			return migrateErr
		}
		databaseConnections, err = ncpdatabase.NewConnectionCoordinator(vault, socketDatabaseConnectionTester{socketPath: *agentSocketPath})
		if err != nil {
			return err
		}
	}

	collector, err := monitoring.NewCollector(socketSummarySource{socketPath: *agentSocketPath}, controlStore, monitoring.Options{Logger: slog.Default()})
	if err != nil {
		return err
	}
	dockerCollector, err := dockerrealtime.NewCollector(socketDockerSource{socketPath: *agentSocketPath})
	if err != nil {
		return err
	}
	jobEngine, err := httpapi.NewJobEngine(controlStore)
	if err != nil {
		return err
	}
	ownerContext, cancelOwners := context.WithCancel(ctx)
	collectorStarted := false
	var server *http.Server
	// 所有退出分支共用一次预算；先取消流/RPC，再并行等待 HTTP、Job 和采集器。
	defer func() {
		cancelOwners()
		shutdownContext, cancel := context.WithTimeout(context.Background(), httpShutdownTimeout)
		defer cancel()
		if err := shutdownOwners(shutdownContext, server, jobEngine, collector, collectorStarted, dockerCollector); err != nil {
			slog.Error("服务关闭未完成", "code", "SERVER_SHUTDOWN_FAILED")
			resultErr = errors.Join(resultErr, err)
		}
	}()

	listener, err := net.Listen("tcp", *listenAddress)
	if err != nil {
		return err
	}
	server = &http.Server{
		BaseContext: func(net.Listener) context.Context { return ownerContext },
		Handler: httpapi.NewHandler(httpapi.Config{
			JobEngine:           jobEngine,
			AgentSocketPath:     *agentSocketPath,
			Auth:                authService,
			ControlStore:        controlStore,
			SummarySnapshots:    collector,
			DockerSnapshots:     dockerCollector,
			DatabaseConnections: databaseConnections,
			SiteAssetsDirectory: filepath.Join(filepath.Dir(*databasePath), "site-icons"),
			SessionCookieSecure: *secureCookie,
			TerminalEnabled:     *terminalEnabled,
			Logger:              slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})),
		}),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	collectorStarted = true
	go func() { _ = collector.Run(ownerContext) }()
	go func() { _ = dockerCollector.Run(ownerContext) }()
	serveErrors := make(chan error, 1)
	go func() {
		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveErrors <- err
	}()

	select {
	case err := <-serveErrors:
		return err
	case <-ctx.Done():
		return nil
	}
}

type socketDatabaseConnectionTester struct {
	socketPath string
}

func (tester socketDatabaseConnectionTester) TestConnection(ctx context.Context, connection ncpdatabase.Connection) (ncpdatabase.ConnectionDiagnostic, error) {
	return agentsocket.TestDatabaseConnection(ctx, tester.socketPath, connection)
}

// socketSummarySource 保持宿主机能力只由 Agent RPC 提供。
type socketSummarySource struct{ socketPath string }

func (s socketSummarySource) CollectSystemSummary(ctx context.Context) (system.Summary, error) {
	return agentsocket.CollectSystemSummary(ctx, s.socketPath)
}

type socketDockerSource struct{ socketPath string }

func (s socketDockerSource) CollectDockerInventory(ctx context.Context) (docker.Inventory, error) {
	return agentsocket.CollectDockerInventory(ctx, s.socketPath)
}

type shutdownJobOwner interface {
	StopAdmission()
	Close(context.Context) error
}
type shutdownCollector interface{ Done() <-chan struct{} }

func shutdownOwners(ctx context.Context, server *http.Server, jobs shutdownJobOwner, collector shutdownCollector, started bool, additional ...shutdownCollector) error {
	jobs.StopAdmission()
	results := make(chan error, 3+len(additional))
	for _, owner := range additional {
		go func() {
			if !started {
				results <- nil
				return
			}
			select {
			case <-owner.Done():
				results <- nil
			case <-ctx.Done():
				results <- ctx.Err()
			}
		}()
	}
	go func() { results <- jobs.Close(ctx) }()
	go func() {
		if server == nil {
			results <- nil
			return
		}
		err := server.Shutdown(ctx)
		if err != nil {
			_ = server.Close()
		}
		results <- err
	}()
	go func() {
		if !started {
			results <- nil
			return
		}
		select {
		case <-collector.Done():
			results <- nil
		case <-ctx.Done():
			results <- ctx.Err()
		}
	}()
	var result error
	for i := 0; i < 3+len(additional); i++ {
		result = errors.Join(result, <-results)
	}
	return result
}
