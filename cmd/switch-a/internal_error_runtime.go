package main

import (
	"context"
	"fmt"
	"sync"

	adminerrorruleapi "github.com/doraemonkeys/switch-a/internal/admin/errorruleapi"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	errorrulesqlite "github.com/doraemonkeys/switch-a/internal/errorrule/sqlite"
	"github.com/doraemonkeys/switch-a/internal/errorrule/statistics"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis"

	"go.uber.org/zap"
)

// internalErrorRuntime holds the single process-wide instances shared by the
// request path and the administrative control plane. Keeping this graph here
// prevents either HTTP server from silently constructing a private budget,
// analyzer, repository view, or statistics accumulator.
type internalErrorRuntime struct {
	ruleRepository   *errorrulesqlite.Repository
	processBudget    *responseanalysis.ProcessMemoryBudget
	scheduler        responseanalysis.Scheduler
	responseAnalyzer *responseanalysis.Analyzer
	ruleStatistics   *statistics.Accumulator
	adminHandler     *adminerrorruleapi.Handler
}

func newInternalErrorRuntime(
	repository *errorrulesqlite.Repository,
	providers adminerrorruleapi.ProviderCatalog,
	log *zap.Logger,
) (*internalErrorRuntime, error) {
	if repository == nil {
		return nil, fmt.Errorf("internal-error rule repository is required")
	}
	if providers == nil {
		return nil, fmt.Errorf("internal-error provider catalog is required")
	}
	if log == nil {
		return nil, fmt.Errorf("internal-error runtime logger is required")
	}
	snapshot := repository.CurrentRuleSet()
	if snapshot == nil {
		return nil, fmt.Errorf("internal-error rule repository has no compiled snapshot")
	}

	accumulator, err := statistics.New(repository)
	if err != nil {
		return nil, fmt.Errorf("initialize internal-error rule statistics: %w", err)
	}
	budget, err := responseanalysis.NewDefaultProcessMemoryBudget()
	if err != nil {
		return nil, fmt.Errorf("initialize response-analysis process budget: %w", err)
	}
	// One scheduler instance owns every probe timer so test and production
	// construction cannot diverge into per-handler timing domains.
	scheduler := &responseanalysis.RealScheduler{}
	analyzer, err := responseanalysis.NewAnalyzer(
		responseanalysis.NewRegistry(),
		budget,
		responseanalysis.AnalyzerOptions{Scheduler: scheduler},
	)
	if err != nil {
		return nil, fmt.Errorf("initialize response analyzer: %w", err)
	}
	adminHandler, err := adminerrorruleapi.NewHandler(adminerrorruleapi.Config{
		Rules:        repository,
		Stats:        repository,
		StatsOverlay: accumulator,
		Providers:    providers,
		Analyzer:     adminerrorruleapi.NewRegistryAnalyzer(),
		Logger:       log,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize internal-error admin API: %w", err)
	}
	// Bind only after every fallible dependent is ready. A failed construction
	// must not leave a reusable repository half-bound to discarded state.
	if err := repository.BindStatsGenerationRetirer(accumulator.Retire); err != nil {
		return nil, fmt.Errorf("bind internal-error statistics retirement: %w", err)
	}

	log.Info("initialized internal-error runtime",
		zap.String("rule_set_revision", snapshot.Revision().String()),
		zap.Int("rule_count", len(snapshot.Rules())),
		zap.Int("response_probe_memory_budget_bytes", budget.Limit()),
	)
	return &internalErrorRuntime{
		ruleRepository:   repository,
		processBudget:    budget,
		scheduler:        scheduler,
		responseAnalyzer: analyzer,
		ruleStatistics:   accumulator,
		adminHandler:     adminHandler,
	}, nil
}

type ruleStatsRunner interface {
	Run(context.Context) error
}

// ruleStatsWorker gives cancellation and the final flush one idempotent owner.
// The worker stores only a cancel function; the execution context remains local
// to the goroutine that consumes it.
type ruleStatsWorker struct {
	cancel   context.CancelFunc
	done     <-chan error
	logger   *zap.Logger
	workerID string
	stopOnce sync.Once
	stopErr  error
}

func startRuleStatsWorker(runner ruleStatsRunner, log *zap.Logger) (*ruleStatsWorker, error) {
	if runner == nil {
		return nil, fmt.Errorf("internal-error statistics runner is required")
	}
	if log == nil {
		return nil, fmt.Errorf("internal-error statistics logger is required")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	worker := &ruleStatsWorker{
		cancel: cancel, done: done, logger: log,
		workerID: errorrule.UUIDGenerator{}.NewID(),
	}
	log.Info("starting internal-error statistics worker",
		zap.String("stats_worker_id", worker.workerID),
		zap.Duration("flush_interval", statistics.StatsFlushInterval),
		zap.Duration("shutdown_timeout", statistics.StatsShutdownTimeout),
	)
	go func() {
		done <- runner.Run(ctx)
		close(done)
	}()
	return worker, nil
}

func (w *ruleStatsWorker) Shutdown() error {
	if w == nil {
		return nil
	}
	w.stopOnce.Do(func() {
		w.logger.Info("stopping internal-error statistics worker",
			zap.String("stats_worker_id", w.workerID),
		)
		w.cancel()
		if err := <-w.done; err != nil {
			w.stopErr = fmt.Errorf("final internal-error statistics flush: %w", err)
			w.logger.Error("internal-error statistics worker stopped with an error",
				zap.String("stats_worker_id", w.workerID),
				zap.Error(err),
			)
			return
		}
		w.logger.Info("internal-error statistics worker stopped",
			zap.String("stats_worker_id", w.workerID),
			zap.String("shutdown_result", "final_flush_completed"),
		)
	})
	return w.stopErr
}
