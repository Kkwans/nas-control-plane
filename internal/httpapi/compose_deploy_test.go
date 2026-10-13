package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/agentsocket"
	ncpcompose "github.com/Kkwans/nas-control-plane/internal/compose"
	"github.com/Kkwans/nas-control-plane/internal/controlstore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

const composeSecretFixture = "stderr: password=compose-private-fixture"

type composeFailureStatus struct {
	code    codes.Code
	message string
}

func (e composeFailureStatus) Error() string { return composeSecretFixture }
func (e composeFailureStatus) GRPCStatus() *grpcstatus.Status {
	return grpcstatus.New(e.code, e.message)
}

type fakeComposeDeployRPC struct {
	agentsocket.AgentComposeServiceClient
	err error
}

func (client fakeComposeDeployRPC) DeployConfig(context.Context, *structpb.Struct, ...grpc.CallOption) (*structpb.Struct, error) {
	return nil, client.err
}

type fakeComposeDeployClient struct {
	ComposeAgentClient
	run func(context.Context, ncpcompose.DeployRequest) (ncpcompose.DeployResult, error)
}

func (client fakeComposeDeployClient) DeployComposeConfig(ctx context.Context, _ string, request ncpcompose.DeployRequest) (ncpcompose.DeployResult, error) {
	return client.run(ctx, request)
}

type composeDeployJobStore struct {
	*controlstore.Store
	terminal      chan struct{}
	revisionCalls atomic.Int32
}

func (store *composeDeployJobStore) UpdateJob(ctx context.Context, record controlstore.JobRecord, revision int64) (int64, error) {
	next, err := store.Store.UpdateJob(ctx, record, revision)
	if err == nil && jobTerminal(record.Status) {
		store.terminal <- struct{}{}
	}
	return next, err
}

func (store *composeDeployJobStore) RecordComposeRevision(ctx context.Context, revision controlstore.ComposeRevision) (controlstore.ComposeRevision, error) {
	store.revisionCalls.Add(1)
	return store.Store.RecordComposeRevision(ctx, revision)
}

func runComposeDeployJob(t *testing.T, client ComposeAgentClient, wantRevisions int, afterSubmit ...func(*jobRegistry, string)) (jobSnapshot, jobSnapshot) {
	t.Helper()
	databasePath := filepath.Join(t.TempDir(), "control.sqlite")
	database, err := controlstore.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	store := &composeDeployJobStore{Store: database, terminal: make(chan struct{}, 1)}
	jobs, err := NewJobEngine(store)
	if err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	closed := false
	closeFixture := func() {
		if closed {
			return
		}
		closed = true
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := jobs.Close(ctx); err != nil {
			t.Error(err)
		}
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(closeFixture)
	api := &handler{compose: client, controlStore: store, jobs: jobs}
	request := httptest.NewRequest(http.MethodPost, "/api/compose/deploy", strings.NewReader(`{"projectId":"fixture","workingDirectory":"/srv/fixture","configFiles":["/srv/fixture/compose.yaml"],"targetPath":"/srv/fixture/compose.yaml","content":"services: {}"}`))
	response := httptest.NewRecorder()
	api.deployComposeConfig(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var submitted jobSnapshot
	if err := json.Unmarshal(response.Body.Bytes(), &submitted); err != nil {
		t.Fatal(err)
	}
	for _, action := range afterSubmit {
		action(jobs, submitted.ID)
	}
	select {
	case <-store.terminal:
	case <-time.After(5 * time.Second):
		t.Fatal("Compose Job did not persist a terminal result")
	}
	// 等待持久化之后的同一次终态提交完成，避免用 sleep 猜测 worker 进度。
	entry := jobs.lookup(submitted.ID)
	entry.opMu.Lock()
	finished := entry.snapshot()
	entry.opMu.Unlock()
	if calls := store.revisionCalls.Load(); calls != int32(wantRevisions) {
		t.Fatalf("revision writes = %d, want %d", calls, wantRevisions)
	}
	revisions, err := store.ComposeRevisions(context.Background(), "fixture", 30)
	if err != nil || len(revisions) != wantRevisions {
		t.Fatalf("revisions = %+v, err = %v", revisions, err)
	}
	closeFixture()

	reopened, err := controlstore.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	reloaded, err := NewJobEngine(reopened)
	if err != nil {
		t.Fatal(err)
	}
	restored, ok := reloaded.get(submitted.ID)
	if !ok {
		t.Fatal("persisted Compose Job missing after reload")
	}
	return finished, restored
}

func TestComposeDeployConfirmedFailuresPersistRecoveryDetail(t *testing.T) {
	tests := []struct {
		code    string
		message string
	}{
		{"COMPOSE_DEPLOY_ROLLBACK_CONFLICT", "Compose 部署失败；检测到配置已被外部修改，未覆盖当前配置，已跳过自动恢复；容器状态未确认。"},
		{"COMPOSE_DEPLOY_ROLLBACK_UNVERIFIED", "Compose 部署失败；无法确认当前配置，已跳过自动恢复；容器状态未确认。"},
		{"COMPOSE_DEPLOY_CONFIG_RESTORE_FAILED", "Compose 部署失败；旧配置未能写回，未重新应用；容器状态未确认。"},
		{"COMPOSE_DEPLOY_RUNTIME_REAPPLY_FAILED", "Compose 部署失败；已写回旧配置，但重新应用命令失败；容器状态未确认。"},
		{"COMPOSE_DEPLOY_RUNTIME_REAPPLY_SUCCEEDED", "Compose 部署失败；已写回旧配置，重新应用命令成功；运行健康未验证。"},
		{"COMPOSE_DEPLOY_FAILED", "Compose 部署失败，恢复结果未确认；请核对配置和容器状态。"},
		{"COMPOSE_CONFIG_INVALID", "Compose 配置校验失败，请检查 YAML 结构、服务引用和环境变量。"},
		{"COMPOSE_REQUEST_INVALID", "Compose 部署请求无效，请检查项目标识、配置路径和提交内容。"},
	}
	for _, test := range tests {
		t.Run(test.code, func(t *testing.T) {
			client := fakeComposeDeployClient{run: func(ctx context.Context, request ncpcompose.DeployRequest) (ncpcompose.DeployResult, error) {
				result, err := agentsocket.DeployComposeConfigWithClient(ctx, fakeComposeDeployRPC{err: composeFailureStatus{code: codes.Internal, message: test.code}}, request)
				// 同时验证已确认失败优先于结果中的伪成功字段。
				result.Completed = true
				result.Output = composeSecretFixture
				return result, err
			}}
			finished, restored := runComposeDeployJob(t, client, 0)
			for _, job := range []jobSnapshot{finished, restored} {
				if job.Status != "failed" || job.ErrorCode != test.code || job.Message != test.message || job.Error != test.message || job.CompletedAt == nil || job.PersistenceStatus != "" {
					t.Fatalf("unexpected recovery result: %+v", job)
				}
				encoded, err := json.Marshal(job)
				if err != nil || strings.Contains(string(encoded), composeSecretFixture) || strings.Contains(string(encoded), "已尝试恢复上一版本") {
					t.Fatalf("unsafe public result: %s, err = %v", encoded, err)
				}
			}
		})
	}
}

func TestComposeDeployTransportFailuresStayInterrupted(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"deadline", composeFailureStatus{code: codes.DeadlineExceeded, message: "COMPOSE_DEPLOY_RUNTIME_REAPPLY_SUCCEEDED"}},
		{"cancelled", composeFailureStatus{code: codes.Canceled, message: "COMPOSE_DEPLOY_ROLLBACK_CONFLICT"}},
		{"unavailable", composeFailureStatus{code: codes.Unavailable, message: "AGENT_RPC_UNAVAILABLE"}},
		{"unavailable-with-known-code", composeFailureStatus{code: codes.Unavailable, message: "COMPOSE_DEPLOY_RUNTIME_REAPPLY_SUCCEEDED"}},
		{"unknown-code", composeFailureStatus{code: codes.Internal, message: "COMPOSE_DEPLOY_FUTURE_UNKNOWN"}},
		{"untrusted-text", errors.New("COMPOSE_DEPLOY_FAILED " + composeSecretFixture)},
		{"untyped-code", errors.New("COMPOSE_DEPLOY_ROLLBACK_CONFLICT")},
		{"disconnect", io.EOF},
		{"local-timeout", context.DeadlineExceeded},
		{"local-cancel", context.Canceled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := fakeComposeDeployClient{run: func(ctx context.Context, request ncpcompose.DeployRequest) (ncpcompose.DeployResult, error) {
				return agentsocket.DeployComposeConfigWithClient(ctx, fakeComposeDeployRPC{err: test.err}, request)
			}}
			finished, restored := runComposeDeployJob(t, client, 0)
			for _, job := range []jobSnapshot{finished, restored} {
				if job.Status != "interrupted" || job.ErrorCode != "JOB_EXECUTION_INTERRUPTED" || job.Message != "连接中断，远端部署结果未确认" || job.Error != "" || job.CompletedAt == nil {
					t.Fatalf("transport outcome became confirmed: %+v", job)
				}
				encoded, err := json.Marshal(job)
				if err != nil || strings.Contains(string(encoded), composeSecretFixture) {
					t.Fatalf("unsafe transport result: %s, err = %v", encoded, err)
				}
			}
		})
	}
}

func TestComposeDeployIncompleteResultDoesNotRecordRevision(t *testing.T) {
	client := fakeComposeDeployClient{run: func(context.Context, ncpcompose.DeployRequest) (ncpcompose.DeployResult, error) {
		return ncpcompose.DeployResult{Output: composeSecretFixture}, nil
	}}
	finished, restored := runComposeDeployJob(t, client, 0)
	for _, job := range []jobSnapshot{finished, restored} {
		if job.Status != "interrupted" || job.ErrorCode != "JOB_EXECUTION_INTERRUPTED" || job.Message != "未收到可信完成确认，请核对 Compose 配置和容器状态" {
			t.Fatalf("incomplete result became successful: %+v", job)
		}
	}
}

func TestComposeDeployLocalCancellationDoesNotConfirmLateRecovery(t *testing.T) {
	entered := make(chan struct{})
	client := fakeComposeDeployClient{run: func(ctx context.Context, request ncpcompose.DeployRequest) (ncpcompose.DeployResult, error) {
		result, err := agentsocket.DeployComposeConfigWithClient(context.Background(), fakeComposeDeployRPC{err: composeFailureStatus{code: codes.Internal, message: "COMPOSE_DEPLOY_RUNTIME_REAPPLY_SUCCEEDED"}}, request)
		close(entered)
		<-ctx.Done()
		return result, err
	}}
	finished, restored := runComposeDeployJob(t, client, 0, func(jobs *jobRegistry, id string) {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("deployment did not start")
		}
		jobs.lookup(id).cancel()
	})
	for _, job := range []jobSnapshot{finished, restored} {
		if job.Status != "interrupted" || job.ErrorCode != "JOB_EXECUTION_INTERRUPTED" || job.Message != "本地调用已结束，远端部署结果未确认" || job.Error != "" {
			t.Fatalf("late recovery result overrode canceled call: %+v", job)
		}
	}
}

func TestComposeDeployConfirmedSuccessStillRecordsRevision(t *testing.T) {
	client := fakeComposeDeployClient{run: func(context.Context, ncpcompose.DeployRequest) (ncpcompose.DeployResult, error) {
		return ncpcompose.DeployResult{ProjectID: "fixture", BackupPath: "/srv/fixture/compose.backup", Completed: true, Output: composeSecretFixture}, nil
	}}
	finished, restored := runComposeDeployJob(t, client, 1)
	for _, job := range []jobSnapshot{finished, restored} {
		if job.Status != "completed" || job.ErrorCode != "" || job.Error != "" || job.Message != "Compose 项目部署完成并已保存版本" || job.CompletedAt == nil {
			t.Fatalf("successful result changed: %+v", job)
		}
		encoded, err := json.Marshal(job)
		if err != nil || strings.Contains(string(encoded), composeSecretFixture) {
			t.Fatalf("unsafe successful result: %s, err = %v", encoded, err)
		}
	}
}
