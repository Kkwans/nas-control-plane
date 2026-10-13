package agentsocket

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"sync"
	"testing"
	"time"

	ncpcompose "github.com/Kkwans/nas-control-plane/internal/compose"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/structpb"
)

var composeDeployFailureCodes = []string{
	"COMPOSE_DEPLOY_FAILED",
	"COMPOSE_CONFIG_INVALID",
	"COMPOSE_REQUEST_INVALID",
	"COMPOSE_DEPLOY_ROLLBACK_CONFLICT",
	"COMPOSE_DEPLOY_ROLLBACK_UNVERIFIED",
	"COMPOSE_DEPLOY_CONFIG_RESTORE_FAILED",
	"COMPOSE_DEPLOY_RUNTIME_REAPPLY_FAILED",
	"COMPOSE_DEPLOY_RUNTIME_REAPPLY_SUCCEEDED",
}

func TestComposeDeployFailurePreservesAllowlistedCodes(t *testing.T) {
	for _, code := range composeDeployFailureCodes {
		t.Run(code, func(t *testing.T) {
			err := composeDeployFailure(code)
			if grpcstatus.Code(err) != codes.Internal || grpcstatus.Convert(err).Message() != code {
				t.Fatalf("composeDeployFailure() = %v", err)
			}
			_, clientErr := DeployComposeConfigWithClient(context.Background(), &fakeComposeDeployRPC{err: err}, ncpcompose.DeployRequest{})
			if ErrorCode(clientErr) != code || !errors.Is(clientErr, err) {
				t.Fatalf("client error = %v, want wrapped %q", clientErr, code)
			}
		})
	}
}

func TestComposeDeployFailureSanitizesUnknownCodes(t *testing.T) {
	for _, code := range []string{"", "secret=config-password", "COMPOSE_LIFECYCLE_INVALID", "COMPOSE_DEPLOY_FUTURE_CODE", "COMPOSE_DEPLOY_ROLLBACK_CONFLICT secret=config-password"} {
		t.Run(code, func(t *testing.T) {
			err := composeDeployFailure(code)
			if grpcstatus.Code(err) != codes.Internal || grpcstatus.Convert(err).Message() != "COMPOSE_DEPLOY_FAILED" {
				t.Fatalf("composeDeployFailure() = %v", err)
			}
		})
	}
}

func TestComposeServiceDeploySanitizesProviderErrors(t *testing.T) {
	_, lifecycleErr := ncpcompose.ParseLifecycleAction("invalid")
	if ncpcompose.ErrorCode(lifecycleErr) != "COMPOSE_LIFECYCLE_INVALID" {
		t.Fatalf("expected typed lifecycle fixture, got %v", lifecycleErr)
	}
	for name, providerErr := range map[string]error{
		"raw sensitive error":   errors.New("deploy stderr: secret=config-password"),
		"code in error text":    errors.New("COMPOSE_DEPLOY_ROLLBACK_CONFLICT"),
		"foreign status error":  grpcstatus.Error(codes.Internal, "COMPOSE_DEPLOY_ROLLBACK_CONFLICT"),
		"unrelated typed error": fmt.Errorf("secret=config-password: %w", lifecycleErr),
	} {
		t.Run(name, func(t *testing.T) {
			service := newComposeService(fakeComposeDeployProvider{deploy: func(context.Context, ncpcompose.DeployRequest) (ncpcompose.DeployResult, error) {
				return ncpcompose.DeployResult{Output: "secret=config-password"}, providerErr
			}})
			response, err := service.DeployConfig(context.Background(), validComposeDeployPayload(t))
			if response != nil || grpcstatus.Code(err) != codes.Internal || grpcstatus.Convert(err).Message() != "COMPOSE_DEPLOY_FAILED" {
				t.Fatalf("DeployConfig() response=%v, error=%v", response, err)
			}
			if ErrorCode(rpcError(err)) != "COMPOSE_DEPLOY_FAILED" {
				t.Fatalf("client lost safe generic code: %v", rpcError(err))
			}
		})
	}
}

func TestComposeServiceDeployPreservesManagerValidationCodes(t *testing.T) {
	for _, test := range []struct {
		name      string
		directory string
		code      string
	}{
		{name: "request", directory: "/outside-data-volumes", code: "COMPOSE_REQUEST_INVALID"},
		{name: "content", directory: "/volume1/project", code: "COMPOSE_CONFIG_INVALID"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := newComposeService(ncpcompose.NewManager(failingComposeValidationRunner{}))
			payload := validComposeDeployPayload(t)
			payload.Fields["working_directory"] = structpb.NewStringValue(test.directory)
			response, err := service.DeployConfig(context.Background(), payload)
			if response != nil || grpcstatus.Code(err) != codes.Internal || grpcstatus.Convert(err).Message() != test.code {
				t.Fatalf("DeployConfig() response=%v, error=%v, want %q", response, err, test.code)
			}
		})
	}
}

func TestComposeServiceDeployRejectsIncompleteRequest(t *testing.T) {
	service := newComposeService(fakeComposeDeployProvider{deploy: func(context.Context, ncpcompose.DeployRequest) (ncpcompose.DeployResult, error) {
		t.Fatal("incomplete request reached provider")
		return ncpcompose.DeployResult{}, nil
	}})
	_, err := service.DeployConfig(context.Background(), nil)
	if grpcstatus.Code(err) != codes.InvalidArgument || ErrorCode(rpcError(err)) != "COMPOSE_REQUEST_INVALID" {
		t.Fatalf("DeployConfig() error=%v", err)
	}
}

func TestComposeServiceDeployContextFailureIsUnknown(t *testing.T) {
	for _, test := range []struct {
		name string
		ctx  func() (context.Context, context.CancelFunc)
		code codes.Code
		want string
	}{
		{name: "canceled", ctx: func() (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx, cancel
		}, code: codes.Canceled, want: "AGENT_RPC_CANCELED"},
		{name: "deadline", ctx: func() (context.Context, context.CancelFunc) {
			return context.WithDeadline(context.Background(), time.Unix(0, 0))
		}, code: codes.DeadlineExceeded, want: "AGENT_RPC_TIMEOUT"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := test.ctx()
			defer cancel()
			service := newComposeService(fakeComposeDeployProvider{deploy: func(context.Context, ncpcompose.DeployRequest) (ncpcompose.DeployResult, error) {
				t.Fatal("expired request reached provider")
				return ncpcompose.DeployResult{}, nil
			}})
			_, err := service.DeployConfig(ctx, validComposeDeployPayload(t))
			if grpcstatus.Code(err) != test.code || ErrorCode(rpcError(err)) != test.want {
				t.Fatalf("DeployConfig() error=%v, client code=%q", err, ErrorCode(rpcError(err)))
			}
		})
	}

	t.Run("canceled during deployment", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		service := newComposeService(fakeComposeDeployProvider{deploy: func(context.Context, ncpcompose.DeployRequest) (ncpcompose.DeployResult, error) {
			cancel()
			return ncpcompose.DeployResult{}, errors.New("secret=config-password")
		}})
		_, err := service.DeployConfig(ctx, validComposeDeployPayload(t))
		if grpcstatus.Code(err) != codes.Canceled || ErrorCode(rpcError(err)) != "AGENT_RPC_CANCELED" {
			t.Fatalf("DeployConfig() error=%v", err)
		}
	})
}

func TestComposeRPCErrorKeepsTransportFailuresUnknown(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want string
	}{
		{name: "canceled status overrides recovery", err: grpcstatus.Error(codes.Canceled, "COMPOSE_DEPLOY_RUNTIME_REAPPLY_SUCCEEDED"), want: "AGENT_RPC_CANCELED"},
		{name: "deadline status overrides recovery", err: grpcstatus.Error(codes.DeadlineExceeded, "COMPOSE_DEPLOY_RUNTIME_REAPPLY_FAILED"), want: "AGENT_RPC_TIMEOUT"},
		{name: "wrapped canceled context", err: fmt.Errorf("request: %w", context.Canceled), want: "AGENT_RPC_CANCELED"},
		{name: "wrapped deadline context", err: fmt.Errorf("request: %w", context.DeadlineExceeded), want: "AGENT_RPC_TIMEOUT"},
		{name: "unimplemented overrides business code", err: grpcstatus.Error(codes.Unimplemented, "COMPOSE_DEPLOY_FAILED"), want: "AGENT_PROTOCOL_MISMATCH"},
		{name: "transport unavailable", err: grpcstatus.Error(codes.Unavailable, "connection closed"), want: "AGENT_RPC_UNAVAILABLE"},
		{name: "unavailable overrides business code", err: grpcstatus.Error(codes.Unavailable, "COMPOSE_DEPLOY_FAILED"), want: "AGENT_RPC_UNAVAILABLE"},
		{name: "raw business code text", err: errors.New("COMPOSE_DEPLOY_FAILED"), want: "AGENT_RPC_UNAVAILABLE"},
		{name: "future code", err: grpcstatus.Error(codes.Internal, "COMPOSE_DEPLOY_FUTURE_CODE"), want: "AGENT_RPC_UNAVAILABLE"},
		{name: "code with sensitive suffix", err: grpcstatus.Error(codes.Internal, "COMPOSE_DEPLOY_ROLLBACK_CONFLICT secret=config-password"), want: "AGENT_RPC_UNAVAILABLE"},
		{name: "unrelated compose code", err: grpcstatus.Error(codes.Internal, "COMPOSE_LIFECYCLE_INVALID"), want: "AGENT_RPC_UNAVAILABLE"},
		{name: "aborted recovery status", err: grpcstatus.Error(codes.Aborted, "COMPOSE_DEPLOY_ROLLBACK_CONFLICT"), want: "COMPOSE_DEPLOY_ROLLBACK_CONFLICT"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := DeployComposeConfigWithClient(context.Background(), &fakeComposeDeployRPC{err: test.err}, ncpcompose.DeployRequest{})
			if ErrorCode(err) != test.want || !errors.Is(err, test.err) {
				t.Fatalf("client error=%v, want wrapped %q", err, test.want)
			}
		})
	}
}

func TestDeployComposeConfigWithClientPreservesPayloadAndResponse(t *testing.T) {
	request := ncpcompose.DeployRequest{
		ProjectID: "compose:project", WorkingDirectory: "/volume1/project",
		ConfigFiles: []string{"/volume1/project/compose.yaml", "/volume1/project/extra.yaml"},
		TargetPath:  "/volume1/project/compose.yaml", Content: "services:\n  app:\n    image: app:v2\n",
	}
	response, err := structpb.NewStruct(map[string]any{
		"projectId": request.ProjectID, "targetPath": request.TargetPath, "backupPath": "/volume1/project/.ncp-backups/backup.yaml",
		"services": []any{"app"}, "output": "done", "completed": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeComposeDeployRPC{response: response}
	result, err := DeployComposeConfigWithClient(context.Background(), client, request)
	if err != nil || !result.Completed || result.ProjectID != request.ProjectID || result.TargetPath != request.TargetPath || result.Output != "done" {
		t.Fatalf("client result=%#v, error=%v", result, err)
	}
	wantPayload := map[string]any{
		"project_id": request.ProjectID, "working_directory": request.WorkingDirectory,
		"config_files": []any{request.ConfigFiles[0], request.ConfigFiles[1]}, "target_path": request.TargetPath, "content": request.Content,
	}
	if !reflect.DeepEqual(client.request.AsMap(), wantPayload) {
		t.Fatalf("payload=%#v, want %#v", client.request.AsMap(), wantPayload)
	}
}

func TestDeployComposeConfigWithClientRejectsIncompleteResponse(t *testing.T) {
	for name, response := range map[string]*structpb.Struct{"nil": nil, "empty": {}} {
		t.Run(name, func(t *testing.T) {
			_, err := DeployComposeConfigWithClient(context.Background(), &fakeComposeDeployRPC{response: response}, ncpcompose.DeployRequest{})
			if ErrorCode(err) != "AGENT_RPC_RESPONSE_INVALID" {
				t.Fatalf("client error=%v", err)
			}
		})
	}
}

func TestDeployComposeConfigWithClientStopsExpiredRequests(t *testing.T) {
	for _, test := range []struct {
		name string
		ctx  func() (context.Context, context.CancelFunc)
		want string
	}{
		{name: "canceled", ctx: func() (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx, cancel
		}, want: "AGENT_RPC_CANCELED"},
		{name: "deadline", ctx: func() (context.Context, context.CancelFunc) {
			return context.WithDeadline(context.Background(), time.Unix(0, 0))
		}, want: "AGENT_RPC_TIMEOUT"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := test.ctx()
			defer cancel()
			client := &fakeComposeDeployRPC{err: grpcstatus.Error(codes.Internal, "COMPOSE_DEPLOY_RUNTIME_REAPPLY_SUCCEEDED")}
			_, err := DeployComposeConfigWithClient(ctx, client, ncpcompose.DeployRequest{})
			if ErrorCode(err) != test.want || client.request != nil {
				t.Fatalf("client error=%v, request=%v", err, client.request)
			}
		})
	}
}

func TestDeployComposeConfigWithClientDeadlineDoesNotConfirmLateRecovery(t *testing.T) {
	service := &blockedComposeRecoveryService{
		started: make(chan struct{}), release: make(chan struct{}), returned: make(chan struct{}),
	}
	release := sync.OnceFunc(func() { close(service.release) })
	defer release()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	RegisterAgentComposeServiceServer(server, service)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	connection, err := grpc.NewClient("passthrough:///compose-deadline",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	clientResult := make(chan error, 1)
	go func() {
		_, err := DeployComposeConfigWithClient(ctx, NewAgentComposeServiceClient(connection), ncpcompose.DeployRequest{})
		clientResult <- err
	}()
	select {
	case <-service.started:
	case <-ctx.Done():
		t.Fatal("RPC did not reach recovery before its deadline")
	}
	select {
	case err := <-clientResult:
		if ErrorCode(err) != "AGENT_RPC_TIMEOUT" {
			t.Fatalf("client error=%v, want AGENT_RPC_TIMEOUT", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("client did not honor the RPC deadline")
	}
	select {
	case <-service.returned:
		t.Fatal("recovery returned before the test released it")
	default:
	}
	release()
	select {
	case <-service.returned:
	case <-time.After(time.Second):
		t.Fatal("late recovery did not finish")
	}
}

func validComposeDeployPayload(t *testing.T) *structpb.Struct {
	t.Helper()
	payload, err := structpb.NewStruct(map[string]any{
		"project_id": "compose:project", "working_directory": "/volume1/project",
		"config_files": []any{"/volume1/project/compose.yaml"}, "target_path": "/volume1/project/compose.yaml",
		"content": "services:\n  app:\n    image: app:v2\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

type fakeComposeDeployProvider struct {
	deploy func(context.Context, ncpcompose.DeployRequest) (ncpcompose.DeployResult, error)
}

func (fakeComposeDeployProvider) Read(context.Context, ncpcompose.ReadRequest) (ncpcompose.ProjectConfig, error) {
	return ncpcompose.ProjectConfig{}, errors.New("unexpected Read")
}

func (fakeComposeDeployProvider) Validate(context.Context, ncpcompose.ValidateRequest) (ncpcompose.ValidationResult, error) {
	return ncpcompose.ValidationResult{}, errors.New("unexpected Validate")
}

func (provider fakeComposeDeployProvider) Deploy(ctx context.Context, request ncpcompose.DeployRequest) (ncpcompose.DeployResult, error) {
	return provider.deploy(ctx, request)
}

type failingComposeValidationRunner struct{}

func (failingComposeValidationRunner) Validate(context.Context, string, string) (string, error) {
	return "", errors.New("validation stderr: secret=config-password")
}

func (failingComposeValidationRunner) Deploy(context.Context, string, []string) (string, error) {
	panic("validation failure must not start deployment")
}

type fakeComposeDeployRPC struct {
	AgentComposeServiceClient
	request  *structpb.Struct
	response *structpb.Struct
	err      error
}

func (client *fakeComposeDeployRPC) DeployConfig(_ context.Context, request *structpb.Struct, _ ...grpc.CallOption) (*structpb.Struct, error) {
	client.request = request
	return client.response, client.err
}

type blockedComposeRecoveryService struct {
	AgentComposeServiceServer
	started  chan struct{}
	release  chan struct{}
	returned chan struct{}
}

func (service *blockedComposeRecoveryService) DeployConfig(context.Context, *structpb.Struct) (*structpb.Struct, error) {
	close(service.started)
	<-service.release
	defer close(service.returned)
	return nil, composeDeployFailure("COMPOSE_DEPLOY_RUNTIME_REAPPLY_SUCCEEDED")
}
