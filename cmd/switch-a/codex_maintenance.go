package main

import (
	"context"
	"fmt"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
	codexmaintenance "github.com/doraemonkeys/switch-a/internal/codex/maintenance"
	"github.com/doraemonkeys/switch-a/internal/store"

	"go.uber.org/zap"
)

// One hour keeps cleanup lag far below the shortest 24-hour retention boundary
// without turning a quiet database into a high-frequency background workload.
const defaultCodexMaintenanceSweepInterval = time.Hour

type applicationCodexMaintenance struct {
	runner *codexmaintenance.Runner
	owner  *codexmaintenance.Owner
}

func newApplicationCodexLifecycle(
	ctx context.Context,
	persistence *store.SQLiteStore,
	security *applicationCodexSecurity,
	clock internal.Clock,
	log *zap.Logger,
) (*applicationCodexRuntime, error) {
	runtime, err := newApplicationCodexRuntime(ctx, persistence, security, clock, log)
	if err != nil {
		return nil, err
	}
	maintenance, err := newApplicationCodexMaintenance(
		persistence, runtime.continuity, runtime.providerCookies, clock, log,
	)
	if err != nil {
		return nil, err
	}
	runtime.maintenance = maintenance
	return runtime, nil
}

func startApplicationCodexLifecycle(ctx context.Context, runtime *applicationCodexRuntime) error {
	if ctx == nil || runtime == nil || runtime.maintenance == nil {
		return fmt.Errorf("start Codex lifecycle: context and composed runtime are required")
	}
	return runtime.maintenance.Start(ctx)
}

func stopApplicationCodexLifecycle(runtime *applicationCodexRuntime, log *zap.Logger) {
	if runtime == nil || runtime.maintenance == nil {
		return
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), ShutdownTimeout)
	defer cancel()
	if err := runtime.maintenance.Stop(shutdownCtx); err != nil {
		log.Error("failed to stop Codex maintenance", zap.Error(err))
	}
}

func newApplicationCodexMaintenance(
	persistence *store.SQLiteStore,
	continuity *codexcontinuity.Service,
	cookies *providercookie.Service,
	clock internal.Clock,
	log *zap.Logger,
) (*applicationCodexMaintenance, error) {
	if persistence == nil || continuity == nil || cookies == nil || clock == nil || log == nil {
		return nil, fmt.Errorf("initialize Codex maintenance: persistence, continuity, Cookie services, clock, and logger are required")
	}
	interval, err := codexmaintenance.NewInterval(defaultCodexMaintenanceSweepInterval)
	if err != nil {
		return nil, fmt.Errorf("initialize Codex maintenance interval: %w", err)
	}
	runner, err := codexmaintenance.NewRunner(codexmaintenance.Config{
		Interval:   interval,
		Clock:      clock,
		Catalog:    persistence,
		Continuity: continuity,
		Cookies:    cookies,
		Observer:   codexMaintenanceLogObserver(log),
	})
	if err != nil {
		return nil, err
	}
	return &applicationCodexMaintenance{runner: runner}, nil
}

func (m *applicationCodexMaintenance) Start(ctx context.Context) error {
	if m == nil || m.runner == nil {
		return fmt.Errorf("start Codex maintenance: composed runner is required")
	}
	if m.owner != nil {
		return fmt.Errorf("start Codex maintenance: owner is already active")
	}
	owner, err := m.runner.Start(ctx)
	if err != nil {
		return err
	}
	m.owner = owner
	return nil
}

func (m *applicationCodexMaintenance) Stop(ctx context.Context) error {
	if m == nil || m.owner == nil {
		return nil
	}
	return m.owner.Stop(ctx)
}

func codexMaintenanceLogObserver(log *zap.Logger) codexmaintenance.Observer {
	return codexmaintenance.ObserverFunc(func(event codexmaintenance.Event) {
		fields := []zap.Field{
			zap.Time("at", event.At),
			zap.String("sweep_id", event.SweepID),
			zap.String("trigger", string(event.Trigger)),
			zap.Duration("duration", event.Duration),
			zap.Int("reachable_cookie_authorities", event.ReachableAuthorities),
			zap.Int64("continuity_expired", event.Continuity.Expired),
			zap.Int64("continuity_tombstoned", event.Continuity.Tombstoned),
			zap.Int64("continuity_deleted", event.Continuity.Deleted),
			zap.Int("cookie_expired_bindings", event.Cookies.ExpiredBindings),
			zap.Int("cookie_expired_entries", event.Cookies.ExpiredCookies),
			zap.Int("cookie_orphan_authorities", event.Cookies.OrphanAuthorities),
			zap.Int("cookie_empty_authorities", event.Cookies.EmptyAuthorities),
			zap.String("cookie_skip_reason", string(event.CookieSkipReason)),
			zap.NamedError("continuity_error", event.ContinuityError),
			zap.NamedError("cookie_error", event.CookieError),
		}
		if event.Failed() {
			log.Warn("codex.maintenance_sweep", fields...)
			return
		}
		log.Info("codex.maintenance_sweep", fields...)
	})
}
