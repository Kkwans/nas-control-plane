package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/system"
)

func TestDNSHandlerAllowsStaticBackendWithoutNetworkTargetAndRequiresConfirmation(t *testing.T) {
	agent := &systemHTTPAgent{
		fakeAgentClient: &fakeAgentClient{},
		dns:             system.DNSCapability{Backend: system.DNSBackendStaticResolv, CanPreview: true},
		preview:         system.DNSChangePreview{PreviewID: "p1", Backend: system.DNSBackendStaticResolv, RequiresConfirm: true},
	}
	api := &handler{agent: agent, agentSocketPath: "/run/ncp/test.sock", agentTimeout: time.Second}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/system/dns/preview", bytes.NewBufferString(`{"nameservers":["1.1.1.1"]}`))
	response := httptest.NewRecorder()
	api.previewDNSChange(response, request)
	if response.Code != http.StatusOK || !agent.previewCalled {
		t.Fatalf("static preview without network target status=%d called=%t body=%s", response.Code, agent.previewCalled, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/system/dns/confirm", bytes.NewBufferString(`{"previewId":"p1","confirmed":false}`))
	response = httptest.NewRecorder()
	api.confirmDNSChange(response, request)
	if response.Code != http.StatusBadRequest || agent.confirmCalled {
		t.Fatalf("unconfirmed change status=%d called=%t body=%s", response.Code, agent.confirmCalled, response.Body.String())
	}
}

func TestPublicEgressHandlerReturnsUnavailableResultExplicitly(t *testing.T) {
	agent := &systemHTTPAgent{fakeAgentClient: &fakeAgentClient{}, egress: system.PublicEgressResult{
		Status: system.CapabilityStateUnavailable, ErrorCode: "PUBLIC_EGRESS_ENDPOINT_NOT_CONFIGURED",
	}}
	api := &handler{agent: agent, agentSocketPath: "/run/ncp/test.sock", agentTimeout: time.Second}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/system/public-egress/check", nil)
	response := httptest.NewRecorder()
	api.detectPublicEgress(response, request)
	if response.Code != http.StatusServiceUnavailable || !agent.egressCalled {
		t.Fatalf("egress status=%d called=%t body=%s", response.Code, agent.egressCalled, response.Body.String())
	}
	var value system.PublicEgressResult
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil || value.ErrorCode != "PUBLIC_EGRESS_ENDPOINT_NOT_CONFIGURED" {
		t.Fatalf("egress response=%s error=%v", response.Body.String(), err)
	}
}

func TestMihomoHandlerRejectsRawOrUnsupportedOperation(t *testing.T) {
	agent := &systemHTTPAgent{fakeAgentClient: &fakeAgentClient{}}
	api := &handler{agent: agent, agentSocketPath: "/run/ncp/test.sock", agentTimeout: time.Second}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/proxy/mihomo/invoke", bytes.NewBufferString(`{"operation":"read-config","token":"do-not-accept"}`))
	response := httptest.NewRecorder()
	api.invokeMihomo(response, request)
	if response.Code != http.StatusBadRequest || agent.invokeCalled {
		t.Fatalf("mihomo invalid status=%d called=%t body=%s", response.Code, agent.invokeCalled, response.Body.String())
	}
}

func TestMihomoInspectionHandlerForwardsForceAndReturnsSafeChain(t *testing.T) {
	agent := &systemHTTPAgent{fakeAgentClient: &fakeAgentClient{}, inspection: system.MihomoInspection{
		Status:     system.CapabilityStateAvailable,
		LocalProxy: system.MihomoLocalProxy{Address: "http://127.0.0.1:7890", Mode: "rule"},
		Strategy:   system.MihomoStrategySelection{Group: "节点选择", SelectedNode: "上海-01", NodeType: "ss", Provider: "provider-a"},
	}}
	api := &handler{agent: agent, agentSocketPath: "/run/ncp/test.sock", agentTimeout: time.Second}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/proxy/mihomo/inspect", bytes.NewBufferString(`{"force":true}`))
	response := httptest.NewRecorder()
	api.inspectMihomo(response, request)
	if response.Code != http.StatusOK || !agent.inspectionCalled || !agent.inspectionForce {
		t.Fatalf("inspection status=%d called=%t force=%t body=%s", response.Code, agent.inspectionCalled, agent.inspectionForce, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "secret") || !strings.Contains(response.Body.String(), "上海-01") {
		t.Fatalf("inspection response=%s", response.Body.String())
	}
}

func TestDNSValidationRejectsInvalidNameserverAndDomain(t *testing.T) {
	if err := validateDNSChangeRequest(system.DNSChangeRequest{Interface: "eth0", Nameservers: []string{"not-an-ip"}}); err == nil {
		t.Fatal("invalid nameserver must fail validation")
	}
	if err := validateDNSChangeRequest(system.DNSChangeRequest{Interface: "eth0", Nameservers: []string{"1.1.1.1"}, SearchDomains: []string{"bad/domain"}}); err == nil {
		t.Fatal("invalid search domain must fail validation")
	}
}

func TestDNSFailureMessageExplainsReadOnlyBackend(t *testing.T) {
	message := dnsChangeFailureMessage(errors.New("DNS_BACKEND_READ_ONLY"), "fallback")
	if !strings.Contains(message, "/etc/resolv.conf") || !strings.Contains(message, "systemd-resolved") {
		t.Fatalf("read-only DNS message = %q", message)
	}
}

func TestDNSFailureMessageExplainsConcurrentChange(t *testing.T) {
	message := dnsChangeFailureMessage(errors.New("DNS_SOURCE_CHANGED"), "fallback")
	if !strings.Contains(message, "其他进程") || !strings.Contains(message, "取消") {
		t.Fatalf("concurrent DNS message = %q", message)
	}
}

type systemHTTPAgent struct {
	*fakeAgentClient
	dns              system.DNSCapability
	preview          system.DNSChangePreview
	confirm          system.DNSChangeResult
	rollback         system.DNSChangeResult
	egressCap        system.PublicEgressCapability
	egress           system.PublicEgressResult
	mihomo           system.MihomoCapability
	mihomoResult     system.MihomoInvokeResult
	inspection       system.MihomoInspection
	previewCalled    bool
	confirmCalled    bool
	egressCalled     bool
	invokeCalled     bool
	inspectionCalled bool
	inspectionForce  bool
}

func (f *systemHTTPAgent) CollectDNSCapability(context.Context, string) (system.DNSCapability, error) {
	return f.dns, nil
}
func (f *systemHTTPAgent) PreviewDNSChange(context.Context, string, system.DNSChangeRequest) (system.DNSChangePreview, error) {
	f.previewCalled = true
	return f.preview, nil
}
func (f *systemHTTPAgent) ConfirmDNSChange(context.Context, string, system.DNSChangeConfirmation) (system.DNSChangeResult, error) {
	f.confirmCalled = true
	return f.confirm, nil
}
func (f *systemHTTPAgent) RollbackDNSChange(context.Context, string, system.DNSRollbackRequest) (system.DNSChangeResult, error) {
	return f.rollback, nil
}
func (f *systemHTTPAgent) GetPublicEgressCapability(context.Context, string) (system.PublicEgressCapability, error) {
	return f.egressCap, nil
}
func (f *systemHTTPAgent) DetectPublicEgress(context.Context, string) (system.PublicEgressResult, error) {
	f.egressCalled = true
	return f.egress, nil
}
func (f *systemHTTPAgent) ProbeMihomo(context.Context, string) (system.MihomoCapability, error) {
	return f.mihomo, nil
}
func (f *systemHTTPAgent) InvokeMihomo(context.Context, string, system.MihomoInvokeRequest) (system.MihomoInvokeResult, error) {
	f.invokeCalled = true
	return f.mihomoResult, nil
}
func (f *systemHTTPAgent) InspectMihomo(_ context.Context, _ string, force bool) (system.MihomoInspection, error) {
	f.inspectionCalled = true
	f.inspectionForce = force
	return f.inspection, nil
}

// Keep this adapter tied to the existing AgentClient test fake; it does not alter production interfaces.
var (
	_ AgentClient = (*systemHTTPAgent)(nil)
)
