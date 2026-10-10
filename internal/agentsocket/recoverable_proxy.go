package agentsocket

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/system"
)

var errProxyInitialization = errors.New("AGENT_MIHOMO_INITIALIZATION_FAILED")

// Mihomo is optional. A missing configuration must not take down Docker,
// journald, filesystem or terminal RPCs. Retry initialization on demand without
// a background worker or request-driven retry storms.
type recoverableProxyProvider struct {
	mu        sync.Mutex
	factory   func() (ProxyProvider, error)
	provider  ProxyProvider
	now       func() time.Time
	nextRetry time.Time
	backoff   time.Duration
	logger    *slog.Logger
}

func newRecoverableProxyProvider(factory func() (ProxyProvider, error)) *recoverableProxyProvider {
	return &recoverableProxyProvider{factory: factory, now: time.Now, logger: slog.Default()}
}

func (p *recoverableProxyProvider) current(ctx context.Context) (ProxyProvider, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.provider != nil {
		return p.provider, nil
	}
	if p.now().Before(p.nextRetry) {
		return nil, errProxyInitialization
	}
	provider, err := p.factory()
	if err != nil || provider == nil {
		if p.backoff == 0 {
			p.backoff = time.Second
		} else {
			p.backoff = min(2*p.backoff, time.Minute)
		}
		p.nextRetry = p.now().Add(p.backoff)
		// Do not log the underlying error: it can include configuration or tokens.
		p.logger.Warn("optional dependency unavailable", "component", "mihomo", "code", errProxyInitialization.Error(), "retry_after_ms", p.backoff.Milliseconds())
		return nil, errProxyInitialization
	}
	if p.backoff != 0 {
		p.logger.Info("optional dependency recovered", "component", "mihomo")
	}
	p.provider = provider
	return provider, nil
}

func (p *recoverableProxyProvider) ProbeMihomo(ctx context.Context) (system.MihomoCapability, error) {
	provider, err := p.current(ctx)
	if err != nil {
		return system.MihomoCapability{}, err
	}
	return provider.ProbeMihomo(ctx)
}

func (p *recoverableProxyProvider) InvokeMihomo(ctx context.Context, request system.MihomoInvokeRequest) (system.MihomoInvokeResult, error) {
	provider, err := p.current(ctx)
	if err != nil {
		return system.MihomoInvokeResult{}, err
	}
	return provider.InvokeMihomo(ctx, request)
}

func (p *recoverableProxyProvider) InspectMihomo(ctx context.Context, force bool) (system.MihomoInspection, error) {
	provider, err := p.current(ctx)
	if err != nil {
		return system.MihomoInspection{}, err
	}
	return provider.InspectMihomo(ctx, force)
}
