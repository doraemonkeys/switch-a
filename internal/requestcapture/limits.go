package requestcapture

import (
	"crypto/rand"
	"fmt"
	"io"
	"math"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/doraemonkeys/switch-a/internal/defaults"
	"github.com/doraemonkeys/switch-a/internal/requestcapture/exportregistry"

	"go.uber.org/zap"
)

const providerRecordIndexChargeBytes = int64(unsafe.Sizeof(providerRecordIndex{}))

type providerRecordIndex struct {
	first *recordState
	last  *recordState
	count int
}

type gatewayHandleSlot struct {
	gateway  *gatewayState
	sequence uint64
	nextFree uint32
}

type recordHandleSlot struct {
	record   *recordState
	sequence uint64
	nextFree uint32
}

type handleSlotShape struct {
	gatewayCount int
	recordCount  int
	charge       int64
}

// Manager owns the single process-wide capture generation. The active pointer is
// intentionally the only datum touched by the disabled proxy path.
type Manager struct {
	cfg normalizedConfig

	lifecycleMu sync.Mutex
	generation  uint64
	closed      bool
	starting    bool
	// accessGate makes loading the published session and acquiring an owner one
	// indivisible lifetime operation. It never protects session data itself.
	accessGate sync.RWMutex
	active     atomic.Pointer[sessionState]

	// mu is the terminal process-account lock. Callers may only validate and
	// mutate scalar accounting state while holding it.
	mu               sync.Mutex
	processCharged   int64
	processPinned    int64
	processReleasing int64
	processTemporary int64

	statusEpochMu      sync.Mutex
	statusEpochWriters uint64
	statusEpoch        atomic.Uint64
	statusLeaseMu      sync.Mutex
	statusLeaseNext    uint64
	statusLeaseClaim   statusLeaseClaim
	managerStatusSlot  statusJSONSlot
	managerStatusBytes [statusStoppedJSONBytes]byte

	// Export registry mutations may allocate, signal cancellation, and manage
	// timers, so they are isolated from the terminal account lock.
	exportMu              sync.Mutex
	nextExportReservation uint64
	reservedExportSlots   int
	reservedDownloadSlots int
	exports               exportregistry.Registry[*exportState]
	exportRegistryCharge  int64
	exportRegistryCharged bool
	nextDownloadEpoch     uint64
	activeDownloads       int
}

type sessionState struct {
	manager    *Manager
	id         string
	generation uint64
	startedAt  int64

	ownerMu        sync.Mutex
	ownerCount     int
	ownerAccepting bool
	rootCharge     int64

	gate      sync.RWMutex
	mu        sync.Mutex
	accepting bool

	exportAdmission chan struct{}

	queryMu         sync.Mutex
	queryDone       chan struct{}
	queryCancelOnce sync.Once

	providers          map[string]ProviderIdentity
	providerOrder      []ProviderIdentity
	recordsPerProvider int
	quotaBytes         int64
	chargedBytes       int64
	temporaryBytes     int64
	releasing          bool

	traceFirst            *gatewayState
	traceLast             *gatewayState
	traceCount            int
	oldestRecord          *recordState
	newestRecord          *recordState
	retainedRecordCount   int
	providerRecordIndex   []providerRecordIndex
	providerRecords       map[string]*providerRecordIndex
	nextRecordSequence    uint64
	nextTraceSequence     uint64
	mutationEpoch         uint64
	activeRecords         int
	activeTraces          int
	baseCharge            int64
	gatewayHandleSlots    []gatewayHandleSlot
	recordHandleSlots     []recordHandleSlot
	freeGatewayHandleSlot uint32
	freeRecordHandleSlot  uint32
	queryLeaseFirst       *queryLease
	queryLeaseLast        *queryLease
	queryLeaseCount       int
	nextQuerySequence     uint64
	evictionRangeFirst    *evictionRange
	evictionRangeLast     *evictionRange
	evictionRangeCount    int
	evictionIndexCharge   int64

	evictedCount           uint64
	overflowedCount        uint64
	truncatedTraceCount    uint64
	droppedTraceCount      uint64
	droppedExchangeCount   uint64
	droppedTransitionCount uint64

	statusSlot   *statusJSONSlot
	statusCharge int64
}

type startShape struct {
	recordsPerProvider int
	quotaBytes         int64
	providerBytes      int64
	statusJSONBytes    int
	handleSlots        handleSlotShape
}

type startAllocation struct {
	manager *Manager
	bytes   int64
	active  bool
}

func (m *Manager) beginStartAllocation(
	quotaBytes, candidateBytes int64,
	allocation *startAllocation,
) bool {
	if m == nil || allocation == nil || allocation.active ||
		quotaBytes <= 0 || candidateBytes <= 0 || candidateBytes > quotaBytes {
		return false
	}
	m.mu.Lock()
	if candidateBytes > m.cfg.processCeilingBytes-m.processCharged {
		m.mu.Unlock()
		return false
	}
	m.processCharged += candidateBytes
	m.processTemporary += candidateBytes
	m.mu.Unlock()
	*allocation = startAllocation{manager: m, bytes: candidateBytes, active: true}
	return true
}

func (allocation *startAllocation) commit(session *sessionState) bool {
	if allocation == nil || !allocation.active || session == nil ||
		allocation.manager != session.manager || allocation.bytes > session.quotaBytes ||
		session.statusCharge <= 0 ||
		session.statusCharge > allocation.bytes-sessionRootChargeBytes {
		return false
	}
	m := allocation.manager
	m.mu.Lock()
	if allocation.bytes > m.processTemporary || allocation.bytes > m.processCharged {
		m.mu.Unlock()
		return false
	}
	m.processTemporary -= allocation.bytes
	session.chargedBytes = allocation.bytes
	session.rootCharge = sessionRootChargeBytes
	session.baseCharge = allocation.bytes - session.rootCharge - session.statusCharge
	session.ownerCount = 1
	session.ownerAccepting = true
	m.generation = session.generation
	m.mu.Unlock()
	allocation.active = false
	allocation.manager = nil
	return true
}

func (allocation *startAllocation) rollback() bool {
	if allocation == nil || !allocation.active || allocation.manager == nil {
		return allocation != nil && !allocation.active
	}
	m := allocation.manager
	m.mu.Lock()
	if allocation.bytes > m.processTemporary || allocation.bytes > m.processCharged {
		m.mu.Unlock()
		return false
	}
	m.processTemporary -= allocation.bytes
	m.processCharged -= allocation.bytes
	m.mu.Unlock()
	allocation.active = false
	allocation.manager = nil
	return true
}

func discardUnpublishedSessionCandidate(session *sessionState) {
	if session == nil {
		return
	}
	discardStatusSlot(session.statusSlot)
	session.statusSlot = nil
	session.statusCharge = 0
	session.severHandleSlotsLocked()
	session.providers = nil
	session.providerOrder = nil
	session.providerRecordIndex = nil
	session.providerRecords = nil
	session.queryDone = nil
	session.exportAdmission = nil
	session.id = ""
	session.startedAt = 0
	session.generation = 0
	session.accepting = false
	session.manager = nil
}

func NewManager(cfg Config) (*Manager, error) {
	normalized, err := normalizeConfig(cfg)
	if err != nil {
		return nil, err
	}
	if normalized.maxPendingExports > math.MaxInt-normalized.maxActiveDownloads {
		return nil, ErrCapacityExceeded
	}
	exportCapacity := normalized.maxPendingExports + normalized.maxActiveDownloads
	exportRegistryCharge, validCharge := exportregistry.BackingChargeBytes[*exportState](exportCapacity)
	if !validCharge || exportRegistryCharge > normalized.processCeilingBytes {
		return nil, ErrCapacityExceeded
	}
	exports, validRegistry := exportregistry.New[*exportState](exportCapacity)
	if !validRegistry {
		return nil, ErrCapacityExceeded
	}
	manager := &Manager{
		cfg:                  normalized,
		exports:              exports,
		exportRegistryCharge: exportRegistryCharge,
	}
	manager.managerStatusSlot.storage = manager.managerStatusBytes[:]
	return manager, nil
}

const (
	DefaultProcessCeilingBytes    int64 = int64(defaults.DebugCaptureMemoryCeilingMiB) << 20
	DefaultSessionQuotaBytes      int64 = int64(defaults.DebugCaptureSessionQuotaMiB) << 20
	DefaultChunkBytes                   = defaults.DebugCaptureChunkBytes
	MinimumChunkBytes                   = defaults.DebugCaptureMinimumChunkBytes
	MaximumChunkBytes                   = defaults.DebugCaptureMaximumChunkBytes
	DefaultRecordsPerProvider           = 10
	DefaultMaxRecordsPerProvider        = defaults.DebugCaptureMaxRecordsPerProvider
	DefaultMaxActiveTraces              = defaults.DebugCaptureMaxActiveTraces
	DefaultMaxActiveRecords             = defaults.DebugCaptureMaxActiveRecords
	DefaultMaxTransitionsPerTrace       = defaults.DebugCaptureMaxTransitionsPerTrace
	DefaultMaxPendingExports            = defaults.DebugCaptureMaxPendingExports
	DefaultMaxActiveDownloads           = defaults.DebugCaptureMaxConcurrentDownloads
	DefaultPreviewBytes                 = defaults.DebugCaptureDetailPreviewBytes
	DefaultDetailEventLimit             = defaults.DebugCaptureDetailEventLimit
	DefaultExportLineBytes              = defaults.DebugCaptureExportLineBytes
	DefaultDownloadTokenTTL             = time.Duration(defaults.DebugCaptureDownloadTokenTTLSeconds) * time.Second
	DefaultListLimit                    = 50
	DefaultMaxListLimit                 = 200

	maxRetainedProviders               = 256
	maxRetainedIdentifierBytes         = 256
	maxRetainedProviderIDBytes         = 256
	maxRetainedProviderNameBytes       = 512
	maxRetainedAPITypeBytes            = 128
	maxRetainedMethodBytes             = 64
	maxRetainedURLBytes                = 8 << 10
	maxRetainedHostBytes               = 1 << 10
	maxRetainedHeaderFields            = 128
	maxRetainedHeaderValuesPerField    = 32
	maxRetainedHeaderNameBytes         = 256
	maxRetainedHeaderValueBytes        = 8 << 10
	maxRetainedHeaderBytes             = 64 << 10
	maxRetainedSensitiveHeaderNames    = 64
	maxRetainedCredentialValues        = 64
	maxRetainedCredentialValueBytes    = 4 << 10
	maxRetainedCredentialBytes         = 64 << 10
	maxRetainedProviderErrorFieldBytes = 128
	maxRetainedErrorBytes              = 2 << 10
	maxRetainedCloseReasonBytes        = 1 << 10
	maxPendingLineagesPerTrace         = 4096
	maxCursorBytes                     = 512
)

// Clock separates human-readable wall time from the monotonic elapsed-time
// domain used for security deadlines. Wall-clock corrections must never extend
// or shorten a capability token's lifetime.
type Clock interface {
	WallNow() time.Time
	MonotonicNow() time.Duration
}

// IDGenerator keeps externally visible identifiers deterministic in tests.
type IDGenerator interface {
	NewID() ([16]byte, error)
}

// Timer is the cancellation surface required by export-expiry scheduling.
type Timer interface {
	Stop() bool
}

// Scheduler schedules bounded export-expiry work without storing a context.
type Scheduler interface {
	AfterFunc(time.Duration, func()) Timer
}

// Config defines process-wide hard limits. Zero values select the frozen product
// defaults so composition code cannot accidentally disable a safety bound.
type Config struct {
	ProcessCeilingBytes       int64
	DefaultSessionQuotaBytes  int64
	ChunkBytes                int
	DefaultRecordsPerProvider int
	MaxRecordsPerProvider     int
	MaxActiveTraces           int
	MaxActiveRecords          int
	MaxTransitionsPerTrace    int
	MaxPendingExports         int
	MaxActiveDownloads        int
	PreviewBytes              int
	DetailEventLimit          int
	ExportLineBytes           int
	DownloadTokenTTL          time.Duration

	Clock       Clock
	IDGenerator IDGenerator
	Scheduler   Scheduler
	Entropy     io.Reader
	Logger      *zap.Logger
}

type normalizedConfig struct {
	processCeilingBytes       int64
	defaultSessionQuotaBytes  int64
	chunkBytes                int
	defaultRecordsPerProvider int
	maxRecordsPerProvider     int
	maxActiveTraces           int
	maxActiveRecords          int
	maxTransitionsPerTrace    int
	maxPendingExports         int
	maxActiveDownloads        int
	previewBytes              int
	detailEventLimit          int
	exportLineBytes           int
	downloadTokenTTL          time.Duration
	clock                     Clock
	idGenerator               IDGenerator
	scheduler                 Scheduler
	entropy                   io.Reader
	logger                    *zap.Logger
}

func normalizeConfig(cfg Config) (normalizedConfig, error) {
	n := normalizedConfig{
		processCeilingBytes:       defaultInt64(cfg.ProcessCeilingBytes, DefaultProcessCeilingBytes),
		defaultSessionQuotaBytes:  defaultInt64(cfg.DefaultSessionQuotaBytes, DefaultSessionQuotaBytes),
		chunkBytes:                defaultInt(cfg.ChunkBytes, DefaultChunkBytes),
		defaultRecordsPerProvider: defaultInt(cfg.DefaultRecordsPerProvider, DefaultRecordsPerProvider),
		maxRecordsPerProvider:     defaultInt(cfg.MaxRecordsPerProvider, DefaultMaxRecordsPerProvider),
		maxActiveTraces:           defaultInt(cfg.MaxActiveTraces, DefaultMaxActiveTraces),
		maxActiveRecords:          defaultInt(cfg.MaxActiveRecords, DefaultMaxActiveRecords),
		maxTransitionsPerTrace:    defaultInt(cfg.MaxTransitionsPerTrace, DefaultMaxTransitionsPerTrace),
		maxPendingExports:         defaultInt(cfg.MaxPendingExports, DefaultMaxPendingExports),
		maxActiveDownloads:        defaultInt(cfg.MaxActiveDownloads, DefaultMaxActiveDownloads),
		previewBytes:              defaultInt(cfg.PreviewBytes, DefaultPreviewBytes),
		detailEventLimit:          defaultInt(cfg.DetailEventLimit, DefaultDetailEventLimit),
		exportLineBytes:           defaultInt(cfg.ExportLineBytes, DefaultExportLineBytes),
		downloadTokenTTL:          cfg.DownloadTokenTTL,
		clock:                     cfg.Clock,
		idGenerator:               cfg.IDGenerator,
		scheduler:                 cfg.Scheduler,
		entropy:                   cfg.Entropy,
		logger:                    cfg.Logger,
	}
	if n.downloadTokenTTL == 0 {
		n.downloadTokenTTL = DefaultDownloadTokenTTL
	}
	if n.clock == nil {
		n.clock = newRealClock()
	}
	if n.idGenerator == nil {
		n.idGenerator = uuidGenerator{}
	}
	if n.scheduler == nil {
		n.scheduler = realScheduler{}
	}
	if n.entropy == nil {
		n.entropy = rand.Reader
	}
	if n.logger == nil {
		n.logger = zap.NewNop()
	}
	if err := n.validate(); err != nil {
		return normalizedConfig{}, err
	}
	return n, nil
}

func (c normalizedConfig) validate() error {
	positive := map[string]int64{
		"process_ceiling_bytes":        c.processCeilingBytes,
		"default_session_quota_bytes":  c.defaultSessionQuotaBytes,
		"chunk_bytes":                  int64(c.chunkBytes),
		"default_records_per_provider": int64(c.defaultRecordsPerProvider),
		"max_records_per_provider":     int64(c.maxRecordsPerProvider),
		"max_active_traces":            int64(c.maxActiveTraces),
		"max_active_records":           int64(c.maxActiveRecords),
		"max_transitions_per_trace":    int64(c.maxTransitionsPerTrace),
		"max_pending_exports":          int64(c.maxPendingExports),
		"max_active_downloads":         int64(c.maxActiveDownloads),
		"preview_bytes":                int64(c.previewBytes),
		"detail_event_limit":           int64(c.detailEventLimit),
		"export_line_bytes":            int64(c.exportLineBytes),
		"download_token_ttl":           int64(c.downloadTokenTTL),
	}
	for field, value := range positive {
		if value <= 0 {
			return &ValidationError{Field: field, Reason: "must be positive"}
		}
	}
	minimumHandleSlots, validHandleSlots := scanHandleSlotShape(
		1, 1, c.maxActiveTraces, c.maxActiveRecords,
	)
	if !validHandleSlots || minimumHandleSlots.charge > c.processCeilingBytes {
		return &ValidationError{
			Field:  "process_ceiling_bytes",
			Reason: "configured handle capacity must fit within process_ceiling_bytes",
		}
	}
	if c.defaultSessionQuotaBytes > c.processCeilingBytes {
		return &ValidationError{Field: "default_session_quota_bytes", Reason: "must not exceed process_ceiling_bytes"}
	}
	if c.defaultRecordsPerProvider > c.maxRecordsPerProvider {
		return &ValidationError{Field: "default_records_per_provider", Reason: "must not exceed max_records_per_provider"}
	}
	// Storage chunks and export fragments are independent bounds: the exporter
	// splits retained chunks to fit the line budget. Validation therefore requires
	// only one base64 quantum plus the largest bounded envelope.
	minimumLineBytes := minimumExportLineBytes()
	if c.exportLineBytes < minimumLineBytes {
		return &ValidationError{Field: "export_line_bytes", Reason: fmt.Sprintf("must be at least %d bytes", minimumLineBytes)}
	}
	if c.chunkBytes < MinimumChunkBytes || c.chunkBytes > MaximumChunkBytes {
		return &ValidationError{
			Field:  "chunk_bytes",
			Reason: fmt.Sprintf("must be between %d and %d bytes", MinimumChunkBytes, MaximumChunkBytes),
		}
	}
	if c.processCeilingBytes < chunkMetadataChargeBytes ||
		int64(c.chunkBytes) > c.processCeilingBytes-chunkMetadataChargeBytes {
		return &ValidationError{Field: "chunk_bytes", Reason: "chunk allocation must fit within process_ceiling_bytes"}
	}
	if int64(c.previewBytes) > c.processCeilingBytes {
		return &ValidationError{Field: "preview_bytes", Reason: "must not exceed process_ceiling_bytes"}
	}
	if int64(c.exportLineBytes) > c.processCeilingBytes {
		return &ValidationError{Field: "export_line_bytes", Reason: "must not exceed process_ceiling_bytes"}
	}
	_, temporaryCharge, validWorkspace := exportWorkspaceSizing(c.exportLineBytes)
	if !validWorkspace || temporaryCharge > c.processCeilingBytes {
		return &ValidationError{Field: "export_line_bytes", Reason: "download workspace must fit within process_ceiling_bytes"}
	}
	return nil
}

func defaultInt(value, fallback int) int {
	if value == 0 {
		return fallback
	}
	return value
}

func defaultInt64(value, fallback int64) int64 {
	if value == 0 {
		return fallback
	}
	return value
}

type realClock struct {
	origin time.Time
}

func newRealClock() realClock {
	return realClock{origin: time.Now()}
}

func (realClock) WallNow() time.Time {
	return time.Now()
}

func (clock realClock) MonotonicNow() time.Duration {
	return time.Since(clock.origin)
}

type uuidGenerator struct{}

func (uuidGenerator) NewID() ([16]byte, error) {
	return newUUID()
}

type realScheduler struct{}

func (realScheduler) AfterFunc(delay time.Duration, fn func()) Timer {
	return time.AfterFunc(delay, fn)
}
