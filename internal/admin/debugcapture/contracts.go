package debugcapture

import (
	"context"
	"io"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"

	"go.uber.org/zap"
)

const (
	// JSON control requests carry only IDs and scalar limits. A 64 KiB cap keeps
	// malformed selections bounded without constraining legitimate provider sets.
	maxJSONBodyBytes int64 = 64 << 10
	// A capability token is a few dozen bytes; 4 KiB leaves ample form-encoding
	// overhead while preventing the unauthenticated endpoint from buffering more.
	maxDownloadFormBytes int64 = 4 << 10
	// Record pagination has only three bounded scalar parameters. Rejecting a
	// larger query before url.ParseQuery prevents malformed authenticated traffic
	// from expanding into an unbounded map of decoded strings.
	maxRecordListQueryBytes  = 4 << 10
	contentTypeJSON          = "application/json"
	contentTypeNDJSON        = "application/x-ndjson"
	downloadTokenField       = "download_token"
	exportDownloadPathPrefix = "/admin/api/debug-capture/exports/"
)

// ProviderCatalog is intentionally limited to the one operation needed to turn
// caller-supplied IDs into safe capture identities. Credentials never cross this
// boundary into the capture manager.
type ProviderCatalog interface {
	ListProviders(context.Context) ([]model.Provider, error)
}

// CaptureSessions is the admin-owned view of session lifecycle operations.
type CaptureSessions interface {
	Start(requestcapture.StartRequest) (requestcapture.SessionInfo, error)
	Stop(sessionID string) error
	OpenStatus(context.Context) (requestcapture.StatusLease, error)
}

// CaptureQueries is kept separate from lifecycle control so handler tests can
// fault query leases without granting mutation capabilities.
type CaptureQueries interface {
	OpenRecordPage(context.Context, string, requestcapture.ListQuery) (*requestcapture.RecordPageLease, error)
	OpenRecordDetail(context.Context, string, string, int) (*requestcapture.RecordDetailLease, error)
}

// CaptureExports binds snapshot acquisition to the creating request so a client
// disconnect can cancel the copy before a pending capability is published. The
// returned Download is already claimed, which lets HTTP headers be committed
// only after the single-use token has been consumed successfully.
type CaptureExports interface {
	CreateExport(context.Context, string, requestcapture.ExportRequest) (requestcapture.ExportTicket, error)
	AcceptDownload(exportID, rawToken string) (requestcapture.Download, error)
}

// DownloadStreamer separates HTTP transport behavior from the opaque claimed
// download. Tests can exercise streaming and disconnect handling without a
// constructor for the core type's private lease state.
type DownloadStreamer interface {
	Stream(context.Context, requestcapture.Download, io.Writer) error
}

type coreDownloadStreamer struct{}

func (coreDownloadStreamer) Stream(ctx context.Context, download requestcapture.Download, destination io.Writer) error {
	return download.WriteTo(ctx, destination)
}

type Config struct {
	Providers ProviderCatalog
	Sessions  CaptureSessions
	Queries   CaptureQueries
	Exports   CaptureExports
	Streamer  DownloadStreamer
	Logger    *zap.Logger
}

type Handler struct {
	providers ProviderCatalog
	sessions  CaptureSessions
	queries   CaptureQueries
	exports   CaptureExports
	streamer  DownloadStreamer
	logger    *zap.Logger
}

func NewHandler(cfg Config) *Handler {
	logger := cfg.Logger
	if logger == nil {
		logger = zap.NewNop()
	}
	streamer := cfg.Streamer
	if streamer == nil {
		streamer = coreDownloadStreamer{}
	}
	return &Handler{
		providers: cfg.Providers,
		sessions:  cfg.Sessions,
		queries:   cfg.Queries,
		exports:   cfg.Exports,
		streamer:  streamer,
		logger:    logger,
	}
}

type StartSessionRequest struct {
	ProviderIDs                 []string `json:"provider_ids"`
	CompletedRecordsPerProvider int      `json:"completed_records_per_provider,omitempty"`
	RetainedBytesLimit          int64    `json:"retained_bytes_limit,omitempty"`
	AcknowledgeRawPayloadRisk   bool     `json:"acknowledge_raw_payload_risk"`
}

// ExportDownloadGrant is the Admin-owned capability representation. The core
// returns domain facts only; keeping the HTTP route here prevents a lower layer
// from choosing or injecting a browser navigation target.
type ExportDownloadGrant struct {
	ExportID      string    `json:"export_id"`
	SessionID     string    `json:"session_id"`
	RecordCount   int       `json:"record_count"`
	ExpiresAt     time.Time `json:"expires_at"`
	DownloadPath  string    `json:"download_path"`
	DownloadToken string    `json:"download_token"`
}
