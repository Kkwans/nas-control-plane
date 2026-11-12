package agentsocket

import (
	"context"
	"errors"
	"net"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	ncpcompose "github.com/Kkwans/nas-control-plane/internal/compose"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/structpb"
)

// 旧接口故意不实现 checked 扩展，防止测试伪装成旧 Agent 却实际执行新路径。
type legacyComposeRPC struct {
	AgentComposeServiceClient
	calls int
}

func (client *legacyComposeRPC) DeployConfig(context.Context, *structpb.Struct, ...grpc.CallOption) (*structpb.Struct, error) {
	client.calls++
	return nil, errors.New("legacy deployment must never run")
}

type checkedComposeRPC struct {
	legacyComposeRPC
	checkedCalls int
	err          error
}

func (client *checkedComposeRPC) DeployConfigChecked(context.Context, *structpb.Struct, ...grpc.CallOption) (*structpb.Struct, error) {
	client.checkedCalls++
	return nil, client.err
}

type legacyComposeServer struct {
	AgentComposeServiceServer
	calls atomic.Int32
}

func (service *legacyComposeServer) DeployConfig(context.Context, *structpb.Struct) (*structpb.Struct, error) {
	service.calls.Add(1)
	return nil, errors.New("legacy deployment must never run")
}

func composeBufconnClient(t *testing.T, register func(*grpc.Server), options ...grpc.ServerOption) AgentComposeServiceClient {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer(options...)
	register(server)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	conn, err := grpc.NewClient("passthrough:///compose-baseline", grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return NewAgentComposeServiceClient(conn)
}

func TestComposeBaselineUnsupportedNeverFallsBack(t *testing.T) {
	t.Run("old client interface", func(t *testing.T) {
		client := &legacyComposeRPC{}
		_, err := DeployComposeConfigWithClient(context.Background(), client, validComposeDeployRequest())
		if ErrorCode(err) != "COMPOSE_BASELINE_UNSUPPORTED" || client.calls != 0 {
			t.Fatalf("error=%v, legacy calls=%d", err, client.calls)
		}
	})
	for _, oldDescription := range []bool{true, false} {
		name := "new description with old interface"
		if oldDescription {
			name = "actual old description without checked method"
		}
		t.Run(name, func(t *testing.T) {
			service := &legacyComposeServer{}
			var intercepted string
			client := composeBufconnClient(t, func(server *grpc.Server) {
				if oldDescription {
					description := agentComposeServiceDescription
					description.Methods = []grpc.MethodDesc{{MethodName: "DeployConfig", Handler: agentComposeDeployConfigHandler}}
					server.RegisterService(&description, service)
				} else {
					RegisterAgentComposeServiceServer(server, service)
				}
			}, grpc.UnaryInterceptor(func(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
				intercepted = info.FullMethod
				return handler(ctx, request)
			}))
			_, err := DeployComposeConfigWithClient(context.Background(), client, validComposeDeployRequest())
			if ErrorCode(err) != "COMPOSE_BASELINE_UNSUPPORTED" || service.calls.Load() != 0 {
				t.Fatalf("error=%v, legacy calls=%d", err, service.calls.Load())
			}
			if !oldDescription && intercepted != agentComposeDeployConfigCheckedFullMethod {
				t.Fatalf("interceptor method=%q", intercepted)
			}
		})
	}
	t.Run("checked unimplemented preserves cause", func(t *testing.T) {
		cause := grpcstatus.Error(codes.Unimplemented, "secret=config-password")
		client := &checkedComposeRPC{err: cause}
		_, err := DeployComposeConfigWithClient(context.Background(), client, validComposeDeployRequest())
		if ErrorCode(err) != "COMPOSE_BASELINE_UNSUPPORTED" || !errors.Is(err, cause) || client.checkedCalls != 1 || client.calls != 0 {
			t.Fatalf("error=%v", err)
		}
	})
}

func TestComposeBaselineCheckedRPCPreservesTypedRequest(t *testing.T) {
	expected := validComposeDeployRequest()
	var calls atomic.Int32
	client := composeBufconnClient(t, func(server *grpc.Server) {
		RegisterAgentComposeServiceServer(server, newComposeService(fakeComposeDeployProvider{deploy: func(_ context.Context, request ncpcompose.DeployRequest) (ncpcompose.DeployResult, error) {
			calls.Add(1)
			if !reflect.DeepEqual(request, expected) {
				t.Errorf("request=%+v, want=%+v", request, expected)
			}
			return ncpcompose.DeployResult{ProjectID: request.ProjectID, TargetPath: request.TargetPath, BackupPath: "/volume1/project/.ncp-backups/original.yaml", Completed: true}, nil
		}}))
	})
	result, err := DeployComposeConfigWithClient(context.Background(), client, expected)
	if err != nil || !result.Completed || calls.Load() != 1 {
		t.Fatalf("result=%+v, err=%v, calls=%d", result, err, calls.Load())
	}
}

func TestComposeBaselineRejectsMalformedBeforeProvider(t *testing.T) {
	for name, value := range map[string]any{"missing": nil, "empty": "", "number": 1, "bool": true, "short": "abc", "uppercase": strings.Repeat("A", 64), "nonhex": strings.Repeat("g", 64), "leading space": " " + strings.Repeat("a", 64), "trailing space": strings.Repeat("a", 64) + " ", "long": strings.Repeat("a", 65)} {
		for _, checked := range []bool{false, true} {
			method := "legacy"
			if checked {
				method = "checked"
			}
			t.Run(name+"/"+method, func(t *testing.T) {
				service := newComposeService(fakeComposeDeployProvider{deploy: func(context.Context, ncpcompose.DeployRequest) (ncpcompose.DeployResult, error) {
					t.Fatal("invalid baseline reached provider")
					return ncpcompose.DeployResult{}, nil
				}})
				payload := validComposeDeployPayload(t)
				if value == nil {
					delete(payload.Fields, "base_content_hash")
				} else {
					field, err := structpb.NewValue(value)
					if err != nil {
						t.Fatal(err)
					}
					payload.Fields["base_content_hash"] = field
				}
				var err error
				if checked {
					_, err = service.DeployConfigChecked(context.Background(), payload)
				} else {
					_, err = service.DeployConfig(context.Background(), payload)
				}
				if grpcstatus.Code(err) != codes.InvalidArgument || grpcstatus.Convert(err).Message() != "COMPOSE_BASELINE_INVALID" {
					t.Fatalf("err=%v", err)
				}
			})
		}
		if str, ok := value.(string); ok || value == nil {
			t.Run(name+"/client", func(t *testing.T) {
				request := validComposeDeployRequest()
				request.BaseContentHash = str
				client := &fakeComposeDeployRPC{}
				_, err := DeployComposeConfigWithClient(context.Background(), client, request)
				if ErrorCode(err) != "COMPOSE_BASELINE_INVALID" || client.request != nil {
					t.Fatalf("err=%v, request=%v", err, client.request)
				}
			})
		}
	}
}

type composeReadRPC struct {
	AgentComposeServiceClient
	response *structpb.Struct
}

func (client composeReadRPC) ReadConfig(context.Context, *structpb.Struct, ...grpc.CallOption) (*structpb.Struct, error) {
	return client.response, nil
}

func TestReadComposeConfigVerifiesExactResponseHash(t *testing.T) {
	content := "\ufeff# 中文 🐱\r\nservices: {}\r\n  \n"
	hash := ncpcompose.ContentHash([]byte(content))
	for name, value := range map[string]any{"valid": hash, "missing": nil, "empty": "", "uppercase": strings.ToUpper(hash), "mismatch": ncpcompose.ContentHash([]byte("other")), "nonstring": true} {
		t.Run(name, func(t *testing.T) {
			file := map[string]any{"path": "/volume1/project/compose.yaml", "name": "compose.yaml", "content": content, "size": len([]byte(content))}
			if value != nil {
				file["contentHash"] = value
			}
			response, err := structpb.NewStruct(map[string]any{"projectId": "compose:project", "files": []any{file}})
			if err != nil {
				t.Fatal(err)
			}
			result, err := ReadComposeConfigWithClient(context.Background(), composeReadRPC{response: response}, ncpcompose.ReadRequest{ProjectID: "compose:project"})
			if name == "valid" {
				if err != nil || len(result.Files) != 1 || result.Files[0].Content != content || result.Files[0].ContentHash != hash {
					t.Fatalf("result=%+v err=%v", result, err)
				}
			} else if ErrorCode(err) != "AGENT_RPC_RESPONSE_INVALID" {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestDeployComposeConfigRejectsMismatchedIdentity(t *testing.T) {
	request := validComposeDeployRequest()
	for _, test := range []struct {
		name, project, target string
		completed             bool
	}{
		{"wrong project", "compose:other", request.TargetPath, true},
		{"empty target", request.ProjectID, "", true},
		{"wrong target", request.ProjectID, "/volume1/project/other.yaml", true},
		{"uncompleted", request.ProjectID, request.TargetPath, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			response, _ := structpb.NewStruct(map[string]any{"projectId": test.project, "targetPath": test.target, "backupPath": "backup", "completed": test.completed})
			_, err := DeployComposeConfigWithClient(context.Background(), &fakeComposeDeployRPC{response: response}, request)
			if ErrorCode(err) != "AGENT_RPC_RESPONSE_INVALID" {
				t.Fatalf("err=%v", err)
			}
		})
	}
	request.TargetPath = " ./compose.yaml "
	response, _ := structpb.NewStruct(map[string]any{"projectId": request.ProjectID, "targetPath": "/volume1/project/compose.yaml", "backupPath": "backup", "completed": true})
	if _, err := DeployComposeConfigWithClient(context.Background(), &fakeComposeDeployRPC{response: response}, request); err != nil {
		t.Fatalf("normalized relative target rejected: %v", err)
	}
}

type snapshotComposeProvider struct {
	fakeComposeDeployProvider
	snapshot ncpcompose.ProjectConfig
}

func (provider snapshotComposeProvider) Read(context.Context, ncpcompose.ReadRequest) (ncpcompose.ProjectConfig, error) {
	return provider.snapshot, nil
}

func TestReadComposeConfigHashRoundTripsThroughRPC(t *testing.T) {
	content := "\ufeff# 中文 🐱\r\nservices: {}\r\n"
	expected := ncpcompose.ProjectConfig{ProjectID: "compose:project", WorkingDirectory: "/volume1/project", Files: []ncpcompose.FileSnapshot{{Path: "/volume1/project/compose.yaml", Name: "compose.yaml", Content: content, ContentHash: ncpcompose.ContentHash([]byte(content)), Size: int64(len(content))}}}
	client := composeBufconnClient(t, func(server *grpc.Server) {
		RegisterAgentComposeServiceServer(server, newComposeService(snapshotComposeProvider{snapshot: expected}))
	})
	result, err := ReadComposeConfigWithClient(context.Background(), client, ncpcompose.ReadRequest{ProjectID: expected.ProjectID, WorkingDirectory: expected.WorkingDirectory, ConfigFiles: []string{expected.Files[0].Path}})
	if err != nil || !reflect.DeepEqual(result, expected) {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestComposeServiceCancellationOverridesLateSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service := newComposeService(fakeComposeDeployProvider{deploy: func(context.Context, ncpcompose.DeployRequest) (ncpcompose.DeployResult, error) {
		cancel()
		return ncpcompose.DeployResult{Completed: true}, nil
	}})
	response, err := service.DeployConfigChecked(ctx, validComposeDeployPayload(t))
	if response != nil || grpcstatus.Code(err) != codes.Canceled {
		t.Fatalf("response=%v err=%v", response, err)
	}
}
