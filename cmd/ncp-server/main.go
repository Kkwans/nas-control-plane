package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/agentsocket"
	"github.com/Kkwans/nas-control-plane/internal/auth"
	"github.com/Kkwans/nas-control-plane/internal/httpapi"
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

func runHTTPServer(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	listenAddress := flags.String("listen", defaultHTTPListenAddress, "HTTP 监听地址")
	agentSocketPath := flags.String("agent-socket", agentsocket.DefaultSocketPath, "Agent Unix Socket 路径")
	databasePath := flags.String("database", defaultDatabasePath, "SQLite 数据库路径")
	secureCookie := flags.Bool("secure-cookie", false, "仅通过 HTTPS 发送登录 Cookie")
	terminalPOC := flags.Bool("terminal-poc", false, "启用受控 P0 终端 WebSocket 通道")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected serve arguments")
	}
	authService, err := auth.Open(*databasePath, auth.Options{})
	if err != nil {
		return err
	}
	defer authService.Close()

	listener, err := net.Listen("tcp", *listenAddress)
	if err != nil {
		return err
	}
	server := &http.Server{
		Handler: httpapi.NewHandler(httpapi.Config{
			AgentSocketPath:     *agentSocketPath,
			Auth:                authService,
			SessionCookieSecure: *secureCookie,
			TerminalPOCEnabled:  *terminalPOC,
		}),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
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
		shutdownContext, cancel := context.WithTimeout(context.Background(), httpShutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			return err
		}
		return <-serveErrors
	}
}
