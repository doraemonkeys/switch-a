package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/admin/tokenusageapi"
	"github.com/doraemonkeys/switch-a/internal/analyticswindow"
	"github.com/doraemonkeys/switch-a/internal/buildinfo"
	"github.com/doraemonkeys/switch-a/internal/config"
	"github.com/doraemonkeys/switch-a/internal/health"
	"github.com/doraemonkeys/switch-a/internal/logger"
	"github.com/doraemonkeys/switch-a/internal/providerauth"
	"github.com/doraemonkeys/switch-a/internal/proxy"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
	"github.com/doraemonkeys/switch-a/internal/selector"
	"github.com/doraemonkeys/switch-a/internal/server"
	"github.com/doraemonkeys/switch-a/internal/store"
	"github.com/doraemonkeys/switch-a/internal/tokenanalytics"
	tokenanalyticssqlite "github.com/doraemonkeys/switch-a/internal/tokenanalytics/sqlite"

	"go.uber.org/zap"
)

func openApplicationStore(
	dbPath string,
	clock internal.Clock,
	observeTimestampMigration store.RequestLogTimestampMigrationObserver,
) (*store.SQLiteStore, error) {
	sqlStore, err := store.NewSQLiteStore(dbPath, clock, observeTimestampMigration)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize store: %w", err)
	}
	return sqlStore, nil
}

type applicationAnalytics struct {
	repository *tokenanalyticssqlite.Repository
	window     analyticswindow.Resolver
	handler    *tokenusageapi.Handler
}

func newApplicationAnalytics(databasePath string, clock internal.Clock, log *zap.Logger) (*applicationAnalytics, error) {
	repository, err := tokenanalyticssqlite.Open(databasePath)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize token analytics repository: %w", err)
	}
	window := analyticswindow.NewResolver(clock)
	handler, err := tokenusageapi.NewHandler(tokenusageapi.Config{
		Analyzer:       tokenanalytics.NewService(repository),
		WindowResolver: &window,
		Clock:          clock,
		Logger:         log,
	})
	if err != nil {
		return nil, errors.Join(fmt.Errorf("failed to initialize token usage admin API: %w", err), repository.Close())
	}
	return &applicationAnalytics{repository: repository, window: window, handler: handler}, nil
}

func (runtime *applicationAnalytics) Close() error {
	return runtime.repository.Close()
}

const requestLogTimestampMigrationID = "request_log_created_at_instants"

func logRequestLogTimestampMigration(log *zap.Logger, report store.RequestLogTimestampMigrationReport) {
	if report.BackfilledCount == 0 && report.InvalidCount == 0 {
		return
	}
	fields := []zap.Field{
		zap.String("migration_id", requestLogTimestampMigrationID),
		zap.Int64("backfilled_count", report.BackfilledCount),
		zap.Int64("invalid_count", report.InvalidCount),
		zap.Uints("invalid_id_sample", report.InvalidIDs),
		zap.Bool("invalid_id_sample_truncated", report.InvalidCount > int64(len(report.InvalidIDs))),
	}
	if report.InvalidCount > 0 {
		log.Warn("request-log timestamp migration completed with quarantined rows", fields...)
		return
	}
	log.Info("request-log timestamp migration completed", fields...)
}

func logApplicationStartup(log *zap.Logger, cfg *config.Config) {
	if cfg.ConfigFileUsed != "" {
		log.Info("loaded config file", zap.String("path", cfg.ConfigFileUsed))
	}
	build := buildinfo.Current()
	log.Info("starting switch-a",
		zap.String("version", build.Version),
		zap.String("commit", build.Commit),
		zap.String("built_at", build.BuiltAt),
		zap.String("proxy_port", cfg.Port),
		zap.String("admin_port", cfg.AdminPort),
		zap.String("log_path", cfg.LogPath),
		zap.String("log_level", cfg.LogLevel),
	)
}

func loadApplicationConfiguration(
	startupID string,
	recorder applicationLifecycleRecorder,
) (*config.Config, string, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, "", err
	}
	recordApplicationLifecycle(recorder, startupID, startupPhaseConfig)
	resolvedKeyringPath, err := resolveCodexKeyringPath(cfg.CodexKeyringFile)
	if err != nil {
		return nil, "", err
	}
	return cfg, resolvedKeyringPath, nil
}

func newApplicationLogger(
	cfg *config.Config,
	startupID string,
	recorder applicationLifecycleRecorder,
) *zap.Logger {
	log := logger.New(logger.Config{
		LogPath:     cfg.LogPath,
		MaxSizeMB:   cfg.LogMaxSizeMB,
		MaxKeepDays: cfg.LogMaxKeepDays,
		Level:       cfg.LogLevel,
	})
	recordApplicationLifecycle(recorder, startupID, startupPhaseLogger)
	logApplicationStartup(log, cfg)
	return log
}

// applicationRuntime is the fully composed process graph. Background work is
// activated separately so construction failures cannot leave hidden goroutines
// using storage that the startup transaction is about to close.
type applicationRuntime struct {
	codex           *applicationCodexRuntime
	errorRules      *internalErrorRuntime
	captures        *requestcapture.Manager
	health          *health.Manager
	sticky          *selector.PersistentStickyCache
	store           *store.CachedStore
	activeRequests  *proxy.ActiveRequestRegistry
	continuitySeeds *proxy.MemoryVisibleContinuitySeedStore
	auth            *providerauth.Service
	proxyServer     *server.Server
	adminServer     *server.AdminServer
}

type applicationClock interface {
	internal.Clock
	requestcapture.Clock
}

func composeApplicationRuntime(
	cfg *config.Config,
	clock applicationClock,
	log *zap.Logger,
	sqlStore *store.SQLiteStore,
	st *store.CachedStore,
	codexSecurity *applicationCodexSecurity,
	analytics *applicationAnalytics,
) (*applicationRuntime, error) {
	codexRuntime, err := newApplicationCodexLifecycle(
		context.Background(), sqlStore, codexSecurity, clock, log,
	)
	if err != nil {
		return nil, err
	}
	errorRuntime, err := newInternalErrorRuntime(st.InternalErrorRuleRepository(), st, log)
	if err != nil {
		return nil, err
	}
	captureManager, err := newCaptureManager(cfg, clock, log)
	if err != nil {
		return nil, err
	}
	healthManager := health.NewManager(health.Config{Store: st, Clock: clock, Logger: log})
	stickyCache := selector.NewPersistentStickyCache(sqlStore, clock, log)
	limiter := selector.NewConcurrencyLimiter()
	// Exact generation ownership prevents provider recreation from releasing a
	// different generation's in-flight capacity.
	activeRegistry := proxy.NewActiveRequestRegistry()
	visibleContinuitySeedStore := proxy.NewVisibleContinuitySeedStore()
	providerSelector := selector.NewSelector(selector.Config{
		Store:         st,
		HealthChecker: healthManager,
		StickyCache:   stickyCache,
		Limiter:       limiter,
		Clock:         clock,
		Logger:        log,
	})
	authService := providerauth.NewService(providerauth.Config{
		CredentialStore: st,
		Clock:           clock,
		Logger:          log,
	})
	proxyServer := server.New(server.Config{
		Port:                       cfg.Port,
		Logger:                     log,
		Store:                      st,
		Health:                     healthManager,
		Selector:                   providerSelector,
		ActiveRegistry:             activeRegistry,
		VisibleContinuitySeedStore: visibleContinuitySeedStore,
		Auth:                       authService,
		Capture:                    captureManager,
		RuleSetProvider:            errorRuntime.ruleRepository,
		ResponseAnalyzer:           errorRuntime.responseAnalyzer,
		RuleStatistics:             errorRuntime.ruleStatistics,
		CodexHTTP:                  codexRuntime.HTTP,
		CodexWebSocket:             codexRuntime.WebSocket,
	})
	adminServer := server.NewAdmin(server.AdminConfig{
		Port:                cfg.AdminPort,
		AdminToken:          cfg.AdminToken,
		Logger:              log,
		Store:               st,
		Health:              healthManager,
		Selector:            providerSelector,
		ProviderLifecycles:  providerSelector,
		Concurrency:         limiter,
		ActiveReqList:       activeRegistry,
		Auth:                authService,
		ProviderImportStore: st,
		InternalErrorRules:  errorRuntime.adminHandler,
		CaptureSessions:     captureManager,
		CaptureQueries:      captureManager,
		CaptureExports:      captureManager,
		AnalyticsWindow:     &analytics.window,
		TokenUsageHandler:   analytics.handler,
	})
	return &applicationRuntime{
		codex:           codexRuntime,
		errorRules:      errorRuntime,
		captures:        captureManager,
		health:          healthManager,
		sticky:          stickyCache,
		store:           st,
		activeRequests:  activeRegistry,
		continuitySeeds: visibleContinuitySeedStore,
		auth:            authService,
		proxyServer:     proxyServer,
		adminServer:     adminServer,
	}, nil
}

func newCaptureManager(
	cfg *config.Config,
	clock requestcapture.Clock,
	log *zap.Logger,
) (*requestcapture.Manager, error) {
	manager, err := requestcapture.NewManager(requestCaptureManagerConfig(cfg, clock, log))
	if err != nil {
		return nil, fmt.Errorf("failed to initialize request capture manager: %w", err)
	}
	return manager, nil
}

func requestCaptureManagerConfig(
	cfg *config.Config,
	clock requestcapture.Clock,
	log *zap.Logger,
) requestcapture.Config {
	return requestcapture.Config{
		ProcessCeilingBytes:       cfg.DebugCaptureMemoryCeilingBytes,
		DefaultSessionQuotaBytes:  requestcapture.DefaultSessionQuotaBytes,
		ChunkBytes:                cfg.DebugCaptureChunkBytes,
		DefaultRecordsPerProvider: requestcapture.DefaultRecordsPerProvider,
		MaxRecordsPerProvider:     cfg.DebugCaptureMaxRecordsPerProvider,
		MaxActiveTraces:           cfg.DebugCaptureMaxActiveTraces,
		MaxActiveRecords:          cfg.DebugCaptureMaxActiveRecords,
		MaxTransitionsPerTrace:    cfg.DebugCaptureMaxTransitionsPerTrace,
		MaxPendingExports:         cfg.DebugCaptureMaxPendingExports,
		MaxActiveDownloads:        cfg.DebugCaptureMaxConcurrentDownloads,
		PreviewBytes:              cfg.DebugCaptureDetailPreviewBytes,
		DetailEventLimit:          cfg.DebugCaptureDetailEventLimit,
		ExportLineBytes:           cfg.DebugCaptureExportLineBytes,
		DownloadTokenTTL:          cfg.DebugCaptureDownloadTokenTTL,
		Clock:                     clock,
		Logger:                    log,
	}
}
