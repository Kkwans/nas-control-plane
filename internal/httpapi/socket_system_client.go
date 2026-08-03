package httpapi

import (
	"context"

	"github.com/Kkwans/nas-control-plane/internal/agentsocket"
	"github.com/Kkwans/nas-control-plane/internal/system"
)

func (socketAgentClient) CollectDNSCapability(ctx context.Context, socketPath string) (system.DNSCapability, error) {
	return agentsocket.CollectDNSCapability(ctx, socketPath)
}

func (socketAgentClient) PreviewDNSChange(ctx context.Context, socketPath string, request system.DNSChangeRequest) (system.DNSChangePreview, error) {
	return agentsocket.PreviewDNSChange(ctx, socketPath, request)
}

func (socketAgentClient) ConfirmDNSChange(ctx context.Context, socketPath string, request system.DNSChangeConfirmation) (system.DNSChangeResult, error) {
	return agentsocket.ConfirmDNSChange(ctx, socketPath, request)
}

func (socketAgentClient) RollbackDNSChange(ctx context.Context, socketPath string, request system.DNSRollbackRequest) (system.DNSChangeResult, error) {
	return agentsocket.RollbackDNSChange(ctx, socketPath, request)
}

func (socketAgentClient) GetPublicEgressCapability(ctx context.Context, socketPath string) (system.PublicEgressCapability, error) {
	return agentsocket.GetPublicEgressCapability(ctx, socketPath)
}

func (socketAgentClient) DetectPublicEgress(ctx context.Context, socketPath string) (system.PublicEgressResult, error) {
	return agentsocket.DetectPublicEgress(ctx, socketPath)
}

func (socketAgentClient) ProbeMihomo(ctx context.Context, socketPath string) (system.MihomoCapability, error) {
	return agentsocket.ProbeMihomo(ctx, socketPath)
}

func (socketAgentClient) InvokeMihomo(ctx context.Context, socketPath string, request system.MihomoInvokeRequest) (system.MihomoInvokeResult, error) {
	return agentsocket.InvokeMihomo(ctx, socketPath, request)
}
