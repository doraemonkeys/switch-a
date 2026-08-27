package maintenance

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
)

type Interval struct {
	duration time.Duration
}

func NewInterval(duration time.Duration) (Interval, error) {
	if duration <= 0 {
		return Interval{}, fmt.Errorf("maintenance interval must be positive")
	}
	return Interval{duration: duration}, nil
}

func (i Interval) Duration() time.Duration { return i.duration }

type Trigger string

const (
	TriggerInitial  Trigger = "initial"
	TriggerPeriodic Trigger = "periodic"
)

type CookieSkipReason string

const (
	CookieNotSkipped       CookieSkipReason = ""
	CookieCatalogFailed    CookieSkipReason = "catalog_failed"
	CookieCatalogInvalid   CookieSkipReason = "catalog_invalid"
	CookieOperationInvalid CookieSkipReason = "operation_id_invalid"
)

type Event struct {
	At                   time.Time
	SweepID              string
	Trigger              Trigger
	Duration             time.Duration
	ReachableAuthorities int
	Continuity           codexcontinuity.CleanupResult
	Cookies              providercookie.CleanupResult
	CookieSkipReason     CookieSkipReason
	ContinuityError      error
	CookieError          error
}

func (e Event) Failed() bool {
	return e.ContinuityError != nil || e.CookieError != nil || e.CookieSkipReason != CookieNotSkipped
}

type Observer interface {
	ObserveMaintenance(Event)
}

type ObserverFunc func(Event)

func (f ObserverFunc) ObserveMaintenance(event Event) { f(event) }

type Clock interface {
	Now() time.Time
}

type Ticker interface {
	C() <-chan time.Time
	Stop()
}

type TickerFactory interface {
	NewTicker(Interval) Ticker
}

type IDSource interface {
	NewID() string
}

type Catalog interface {
	LoadCodexMaintenanceCatalog(context.Context) (CatalogSnapshot, error)
}

type ContinuityCleaner interface {
	Cleanup(context.Context) (codexcontinuity.CleanupResult, error)
}

type CookieCleaner interface {
	Cleanup(context.Context, providercookie.OperationID, []codexidentity.CookieAuthority) (providercookie.CleanupResult, error)
}

type Config struct {
	Interval   Interval
	Clock      Clock
	Tickers    TickerFactory
	IDs        IDSource
	Catalog    Catalog
	Continuity ContinuityCleaner
	Cookies    CookieCleaner
	Observer   Observer
}

type Runner struct {
	interval   Interval
	clock      Clock
	tickers    TickerFactory
	ids        IDSource
	catalog    Catalog
	continuity ContinuityCleaner
	cookies    CookieCleaner
	observer   Observer
}

func NewRunner(config Config) (*Runner, error) {
	if config.Interval.duration <= 0 {
		return nil, fmt.Errorf("initialize Codex maintenance: interval is required")
	}
	if isNil(config.Clock) || isNil(config.Catalog) || isNil(config.Continuity) || isNil(config.Cookies) {
		return nil, fmt.Errorf("initialize Codex maintenance: clock, catalog, continuity, and Cookie cleaners are required")
	}
	if config.Tickers == nil {
		config.Tickers = systemTickerFactory{}
	}
	if config.IDs == nil {
		config.IDs = uuidIDSource{}
	}
	return &Runner{
		interval: config.Interval, clock: config.Clock, tickers: config.Tickers, ids: config.IDs,
		catalog: config.Catalog, continuity: config.Continuity, cookies: config.Cookies, observer: config.Observer,
	}, nil
}

// Run is the only production scheduling loop. Both cleanup families remain
// serial, while failures are isolated to one family and one sweep.
func (r *Runner) Run(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("run Codex maintenance: context is required")
	}
	if ctx.Err() != nil {
		return nil
	}
	r.sweep(ctx, TriggerInitial)
	ticker := r.tickers.NewTicker(r.interval)
	if isNil(ticker) {
		return fmt.Errorf("run Codex maintenance: ticker factory returned nil")
	}
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C():
			r.sweep(ctx, TriggerPeriodic)
		}
	}
}

func (r *Runner) sweep(ctx context.Context, trigger Trigger) {
	started := r.clock.Now().UTC()
	event := Event{At: started, SweepID: r.ids.NewID(), Trigger: trigger}
	event.Continuity, event.ContinuityError = r.continuity.Cleanup(ctx)

	operationID, err := providercookie.NewOperationID(event.SweepID)
	if err != nil {
		event.CookieSkipReason = CookieOperationInvalid
		event.CookieError = err
		r.finish(event, started)
		return
	}
	snapshot, err := r.catalog.LoadCodexMaintenanceCatalog(ctx)
	if err != nil {
		event.CookieSkipReason = CookieCatalogFailed
		event.CookieError = err
		r.finish(event, started)
		return
	}
	reachable, err := snapshot.ReachableCookieAuthorities()
	if err != nil {
		event.CookieSkipReason = CookieCatalogInvalid
		event.CookieError = err
		r.finish(event, started)
		return
	}
	event.ReachableAuthorities = len(reachable)
	event.Cookies, event.CookieError = r.cookies.Cleanup(ctx, operationID, reachable)
	r.finish(event, started)
}

func (r *Runner) finish(event Event, started time.Time) {
	event.Duration = r.clock.Now().UTC().Sub(started)
	if event.Duration < 0 {
		event.Duration = 0
	}
	if r.observer != nil {
		r.observer.ObserveMaintenance(event)
	}
}

type Owner struct {
	cancel   context.CancelFunc
	done     <-chan struct{}
	stopOnce sync.Once
	mu       sync.Mutex
	err      error
}

func (r *Runner) Start(parent context.Context) (*Owner, error) {
	if r == nil {
		return nil, fmt.Errorf("start Codex maintenance: runner is required")
	}
	if parent == nil {
		return nil, fmt.Errorf("start Codex maintenance: context is required")
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	owner := &Owner{cancel: cancel, done: done}
	go func() {
		err := r.Run(ctx)
		owner.mu.Lock()
		owner.err = err
		owner.mu.Unlock()
		close(done)
	}()
	return owner, nil
}

// Stop cancels in-flight storage calls and joins the scheduling goroutine. A
// caller-controlled deadline prevents shutdown from waiting forever on a
// broken driver while still proving normal termination before SQLite closes.
func (o *Owner) Stop(ctx context.Context) error {
	if o == nil {
		return nil
	}
	if ctx == nil {
		return fmt.Errorf("stop Codex maintenance: context is required")
	}
	o.stopOnce.Do(o.cancel)
	select {
	case <-o.done:
		o.mu.Lock()
		defer o.mu.Unlock()
		return o.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

type systemTickerFactory struct{}

func (systemTickerFactory) NewTicker(interval Interval) Ticker {
	return systemTicker{Ticker: time.NewTicker(interval.duration)}
}

type systemTicker struct{ *time.Ticker }

func (t systemTicker) C() <-chan time.Time { return t.Ticker.C }

type uuidIDSource struct{}

func (uuidIDSource) NewID() string { return uuid.NewString() }

func isNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
