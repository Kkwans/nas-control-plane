package agentsocket

import (
	"testing"

	ncpdatabase "github.com/Kkwans/nas-control-plane/internal/database"
)

// 只捕获注册对象，不启动服务或调用数据库；普通与严格 TLS RPC 共用同一预算所有者。
func TestDatabaseOperationBudgetRegistrationSharesManager(t *testing.T) {
	manager := ncpdatabase.NewManager()
	services := tlsRPCRegistrar{}
	registerDatabaseServices(services, manager)
	ordinary, ok := services[agentDatabaseServiceName].implementation.(*databaseService)
	if !ok {
		t.Fatal("ordinary database service missing")
	}
	strict, ok := services[agentDatabaseTLSServiceName].implementation.(*databaseTLSService)
	if !ok {
		t.Fatal("strict TLS database service missing")
	}
	if ordinary.provider != manager || strict.provider != manager || strict.strictProvider != manager {
		t.Fatal("ordinary and strict TLS services must share the same Manager and operation budget")
	}
}
