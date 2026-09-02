package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type expectedDebugCaptureConfig struct {
	memoryCeilingBytes     int64
	maxActiveRecords       int
	maxActiveTraces        int
	maxTransitionsPerTrace int
	maxPendingExports      int
	maxConcurrentDownloads int
	detailPreviewBytes     int
	detailEventLimit       int
	downloadTokenTTL       time.Duration
	maxRecordsPerProvider  int
	chunkBytes             int
	exportLineBytes        int
}

func TestLoadDebugCaptureDefaults(t *testing.T) {
	cfg := loadDebugCaptureConfig(t, "")

	assertDebugCaptureConfig(t, cfg, expectedDebugCaptureConfig{
		memoryCeilingBytes:     int64(DefaultDebugCaptureMemoryCeilingMiB) * bytesPerMiB,
		maxActiveRecords:       DefaultDebugCaptureMaxActiveRecords,
		maxActiveTraces:        DefaultDebugCaptureMaxActiveTraces,
		maxTransitionsPerTrace: DefaultDebugCaptureMaxTransitionsPerTrace,
		maxPendingExports:      DefaultDebugCaptureMaxPendingExports,
		maxConcurrentDownloads: DefaultDebugCaptureMaxConcurrentDownloads,
		detailPreviewBytes:     DefaultDebugCaptureDetailPreviewBytes,
		detailEventLimit:       DefaultDebugCaptureDetailEventLimit,
		downloadTokenTTL:       time.Duration(DefaultDebugCaptureDownloadTokenTTLSeconds) * time.Second,
		maxRecordsPerProvider:  DefaultDebugCaptureMaxRecordsPerProvider,
		chunkBytes:             DefaultDebugCaptureChunkBytes,
		exportLineBytes:        DefaultDebugCaptureExportLineBytes,
	})
}

func TestLoadDebugCaptureFromFile(t *testing.T) {
	cfg := loadDebugCaptureConfig(t, `
debug_capture_memory_ceiling_mib: 768
debug_capture_max_active_records: 111
debug_capture_max_active_traces: 112
debug_capture_max_transitions_per_trace: 13
debug_capture_max_pending_exports: 14
debug_capture_max_concurrent_downloads: 15
debug_capture_detail_preview_bytes: 16000
debug_capture_detail_event_limit: 17
debug_capture_download_token_ttl_seconds: 18
debug_capture_max_records_per_provider: 19
debug_capture_chunk_bytes: 20000
debug_capture_export_line_bytes: 40000
`)

	assertDebugCaptureConfig(t, cfg, expectedDebugCaptureConfig{
		memoryCeilingBytes:     768 * bytesPerMiB,
		maxActiveRecords:       111,
		maxActiveTraces:        112,
		maxTransitionsPerTrace: 13,
		maxPendingExports:      14,
		maxConcurrentDownloads: 15,
		detailPreviewBytes:     16000,
		detailEventLimit:       17,
		downloadTokenTTL:       18 * time.Second,
		maxRecordsPerProvider:  19,
		chunkBytes:             20000,
		exportLineBytes:        40000,
	})
}

func TestLoadDebugCaptureFromEnvironment(t *testing.T) {
	values := map[string]string{
		EnvDebugCaptureMemoryCeilingMiB:        "1024",
		EnvDebugCaptureMaxActiveRecords:        "21",
		EnvDebugCaptureMaxActiveTraces:         "22",
		EnvDebugCaptureMaxTransitionsPerTrace:  "23",
		EnvDebugCaptureMaxPendingExports:       "24",
		EnvDebugCaptureMaxConcurrentDownloads:  "25",
		EnvDebugCaptureDetailPreviewBytes:      "26000",
		EnvDebugCaptureDetailEventLimit:        "27",
		EnvDebugCaptureDownloadTokenTTLSeconds: "28",
		EnvDebugCaptureMaxRecordsPerProvider:   "29",
		EnvDebugCaptureChunkBytes:              "30000",
		EnvDebugCaptureExportLineBytes:         "50000",
	}
	for key, value := range values {
		t.Setenv(key, value)
	}

	cfg := loadDebugCaptureConfig(t, `
debug_capture_memory_ceiling_mib: 768
debug_capture_max_active_records: 1
debug_capture_max_active_traces: 1
debug_capture_max_transitions_per_trace: 1
debug_capture_max_pending_exports: 1
debug_capture_max_concurrent_downloads: 1
debug_capture_detail_preview_bytes: 1
debug_capture_detail_event_limit: 1
debug_capture_download_token_ttl_seconds: 1
debug_capture_max_records_per_provider: 1
debug_capture_chunk_bytes: 1
debug_capture_export_line_bytes: 1
`)

	assertDebugCaptureConfig(t, cfg, expectedDebugCaptureConfig{
		memoryCeilingBytes:     1024 * bytesPerMiB,
		maxActiveRecords:       21,
		maxActiveTraces:        22,
		maxTransitionsPerTrace: 23,
		maxPendingExports:      24,
		maxConcurrentDownloads: 25,
		detailPreviewBytes:     26000,
		detailEventLimit:       27,
		downloadTokenTTL:       28 * time.Second,
		maxRecordsPerProvider:  29,
		chunkBytes:             30000,
		exportLineBytes:        50000,
	})
}

func TestLoadDebugCaptureFromJSONInteger(t *testing.T) {
	path := writeNamedConfigFile(t, "config.json", fmt.Sprintf(
		`{"admin_token":"test-token","%s":123}`,
		KeyDebugCaptureMaxActiveRecords,
	))

	cfg, err := LoadWithPath(path)
	if err != nil {
		t.Fatalf("LoadWithPath() error = %v", err)
	}
	if cfg.DebugCaptureMaxActiveRecords != 123 {
		t.Fatalf("DebugCaptureMaxActiveRecords = %d, want 123", cfg.DebugCaptureMaxActiveRecords)
	}
}

func TestLoadDebugCaptureRejectsNonIntegerJSONValues(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "fraction", value: "1.5"},
		{name: "exponent", value: "1e2"},
		{name: "boolean", value: "true"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := writeNamedConfigFile(t, "config.json", fmt.Sprintf(
				`{"admin_token":"test-token","%s":%s}`,
				KeyDebugCaptureMaxActiveRecords,
				test.value,
			))
			_, err := LoadWithPath(path)
			if err == nil {
				t.Fatalf("LoadWithPath() accepted %s=%s", KeyDebugCaptureMaxActiveRecords, test.value)
			}
			if !strings.Contains(err.Error(), KeyDebugCaptureMaxActiveRecords) {
				t.Fatalf("LoadWithPath() error = %q, want key %q", err, KeyDebugCaptureMaxActiveRecords)
			}
		})
	}
}

func TestDecodeJSONDebugCaptureIntegerPreservesExactValue(t *testing.T) {
	const exactValue int64 = 9_007_199_254_740_993
	values, err := decodeJSONDebugCaptureIntegers(fmt.Appendf(nil,
		`{"%s":%d}`,
		KeyDebugCaptureMaxActiveRecords,
		exactValue,
	))
	if err != nil {
		t.Fatalf("decodeJSONDebugCaptureIntegers() error = %v", err)
	}
	if values[KeyDebugCaptureMaxActiveRecords] != exactValue {
		t.Fatalf(
			"decoded value = %d, want exact value %d",
			values[KeyDebugCaptureMaxActiveRecords],
			exactValue,
		)
	}
}

func TestLoadDebugCaptureEnvironmentOverridesInvalidFileInteger(t *testing.T) {
	tests := []struct {
		name     string
		fileName string
		content  string
	}{
		{
			name:     "YAML octal-looking integer",
			fileName: "config.yaml",
			content:  "admin_token: test-token\n" + KeyDebugCaptureMaxActiveRecords + ": 010\n",
		},
		{
			name:     "JSON exponent",
			fileName: "config.json",
			content: fmt.Sprintf(
				`{"admin_token":"test-token","%s":1e2}`,
				KeyDebugCaptureMaxActiveRecords,
			),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(EnvDebugCaptureMaxActiveRecords, "37")
			cfg, err := LoadWithPath(writeNamedConfigFile(t, test.fileName, test.content))
			if err != nil {
				t.Fatalf("LoadWithPath() error = %v", err)
			}
			if cfg.DebugCaptureMaxActiveRecords != 37 {
				t.Fatalf("DebugCaptureMaxActiveRecords = %d, want environment value 37", cfg.DebugCaptureMaxActiveRecords)
			}
		})
	}
}

func TestLoadDebugCaptureRejectsInvalidLimits(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value int64
	}{
		{name: "ceiling below default session quota", key: KeyDebugCaptureMemoryCeilingMiB, value: DefaultDebugCaptureSessionQuotaMiB - 1},
		{name: "active records", key: KeyDebugCaptureMaxActiveRecords, value: -1},
		{name: "zero active records", key: KeyDebugCaptureMaxActiveRecords, value: 0},
		{name: "active traces", key: KeyDebugCaptureMaxActiveTraces, value: -1},
		{name: "transitions", key: KeyDebugCaptureMaxTransitionsPerTrace, value: -1},
		{name: "pending exports", key: KeyDebugCaptureMaxPendingExports, value: -1},
		{name: "concurrent downloads", key: KeyDebugCaptureMaxConcurrentDownloads, value: -1},
		{name: "preview bytes", key: KeyDebugCaptureDetailPreviewBytes, value: -1},
		{name: "event limit", key: KeyDebugCaptureDetailEventLimit, value: -1},
		{name: "token ttl", key: KeyDebugCaptureDownloadTokenTTLSeconds, value: -1},
		{name: "zero token ttl", key: KeyDebugCaptureDownloadTokenTTLSeconds, value: 0},
		{name: "records per provider", key: KeyDebugCaptureMaxRecordsPerProvider, value: -1},
		{name: "chunk bytes", key: KeyDebugCaptureChunkBytes, value: -1},
		{name: "export line bytes", key: KeyDebugCaptureExportLineBytes, value: -1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := writeConfigFile(t, fmt.Sprintf("admin_token: test-token\n%s: %d\n", test.key, test.value))
			_, err := LoadWithPath(path)
			if err == nil {
				t.Fatalf("LoadWithPath() succeeded with %s=%d", test.key, test.value)
			}
			if !strings.Contains(err.Error(), test.key) {
				t.Fatalf("LoadWithPath() error = %q, want key %q", err, test.key)
			}
		})
	}
}

func TestLoadDebugCaptureRejectsUnitConversionOverflow(t *testing.T) {
	const (
		overflowingMiB     = int64(1 << 43)
		overflowingSeconds = int64(9223372037)
	)
	tests := []struct {
		name  string
		key   string
		value int64
	}{
		{name: "MiB to bytes", key: KeyDebugCaptureMemoryCeilingMiB, value: overflowingMiB},
		{name: "seconds to duration", key: KeyDebugCaptureDownloadTokenTTLSeconds, value: overflowingSeconds},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := writeConfigFile(t, fmt.Sprintf("admin_token: test-token\n%s: %d\n", test.key, test.value))
			_, err := LoadWithPath(path)
			if err == nil {
				t.Fatalf("LoadWithPath() succeeded with overflowing %s", test.key)
			}
			if !strings.Contains(err.Error(), test.key) {
				t.Fatalf("LoadWithPath() error = %q, want key %q", err, test.key)
			}
		})
	}
}

func TestLoadDebugCaptureRejectsNonDecimalEnvironmentIntegers(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "leading-zero octal form", value: "010"},
		{name: "explicit octal form", value: "0o10"},
		{name: "fraction", value: "1.5"},
		{name: "boolean", value: "true"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(EnvDebugCaptureMaxActiveRecords, test.value)
			path := writeConfigFile(t, "admin_token: test-token\n")
			_, err := LoadWithPath(path)
			if err == nil {
				t.Fatalf("LoadWithPath() accepted %s=%q", EnvDebugCaptureMaxActiveRecords, test.value)
			}
			if !strings.Contains(err.Error(), KeyDebugCaptureMaxActiveRecords) {
				t.Fatalf("LoadWithPath() error = %q, want key %q", err, KeyDebugCaptureMaxActiveRecords)
			}
		})
	}
}

func TestLoadDebugCaptureRejectsNonDecimalYAMLIntegers(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "leading-zero octal form", value: "010"},
		{name: "explicit octal form", value: "0o10"},
		{name: "quoted octal form", value: `"010"`},
		{name: "fraction", value: "1.5"},
		{name: "boolean", value: "true"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := writeConfigFile(t, fmt.Sprintf(
				"admin_token: test-token\n%s: %s\n",
				KeyDebugCaptureMaxActiveRecords,
				test.value,
			))
			_, err := LoadWithPath(path)
			if err == nil {
				t.Fatalf("LoadWithPath() accepted %s=%s", KeyDebugCaptureMaxActiveRecords, test.value)
			}
			if !strings.Contains(err.Error(), KeyDebugCaptureMaxActiveRecords) {
				t.Fatalf("LoadWithPath() error = %q, want key %q", err, KeyDebugCaptureMaxActiveRecords)
			}
		})
	}
}

func TestLoadDebugCaptureRejectsChunkBytesOutsideCoreBounds(t *testing.T) {
	tests := []struct {
		name        string
		value       int
		environment bool
	}{
		{name: "YAML below minimum", value: MinimumDebugCaptureChunkBytes - 1},
		{name: "YAML above maximum", value: MaximumDebugCaptureChunkBytes + 1},
		{name: "environment below minimum", value: MinimumDebugCaptureChunkBytes - 1, environment: true},
		{name: "environment above maximum", value: MaximumDebugCaptureChunkBytes + 1, environment: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			content := fmt.Sprintf("admin_token: test-token\n%s: %d\n", KeyDebugCaptureChunkBytes, test.value)
			if test.environment {
				t.Setenv(EnvDebugCaptureChunkBytes, fmt.Sprint(test.value))
				content = "admin_token: test-token\n"
			}
			_, err := LoadWithPath(writeConfigFile(t, content))
			if err == nil {
				t.Fatalf("LoadWithPath() accepted %s=%d", KeyDebugCaptureChunkBytes, test.value)
			}
			if !strings.Contains(err.Error(), KeyDebugCaptureChunkBytes) {
				t.Fatalf("LoadWithPath() error = %q, want key %q", err, KeyDebugCaptureChunkBytes)
			}
		})
	}
}

func TestLoadDebugCaptureAcceptsCoreChunkByteBoundaries(t *testing.T) {
	tests := []struct {
		name        string
		value       int
		environment bool
	}{
		{name: "YAML minimum", value: MinimumDebugCaptureChunkBytes},
		{name: "YAML maximum", value: MaximumDebugCaptureChunkBytes},
		{name: "environment minimum", value: MinimumDebugCaptureChunkBytes, environment: true},
		{name: "environment maximum", value: MaximumDebugCaptureChunkBytes, environment: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			content := fmt.Sprintf("admin_token: test-token\n%s: %d\n", KeyDebugCaptureChunkBytes, test.value)
			if test.environment {
				t.Setenv(EnvDebugCaptureChunkBytes, fmt.Sprint(test.value))
				content = "admin_token: test-token\n"
			}
			cfg, err := LoadWithPath(writeConfigFile(t, content))
			if err != nil {
				t.Fatalf("LoadWithPath() error = %v", err)
			}
			if cfg.DebugCaptureChunkBytes != test.value {
				t.Fatalf("DebugCaptureChunkBytes = %d, want %d", cfg.DebugCaptureChunkBytes, test.value)
			}
		})
	}
}

func TestPositiveRuntimeIntRejectsPlatformOverflow(t *testing.T) {
	maxInt64 := int64(^uint64(0) >> 1)
	if int64(int(maxInt64)) == maxInt64 {
		t.Skip("the serialized int64 range fits in int on this platform")
	}

	_, err := positiveRuntimeInt(KeyDebugCaptureMaxActiveRecords, maxInt64)
	if err == nil {
		t.Fatal("positiveRuntimeInt() accepted a value that does not fit in int")
	}
}

func loadDebugCaptureConfig(t *testing.T, extra string) *Config {
	t.Helper()
	path := writeConfigFile(t, "admin_token: test-token\n"+extra)
	cfg, err := LoadWithPath(path)
	if err != nil {
		t.Fatalf("LoadWithPath() error = %v", err)
	}
	return cfg
}

func writeConfigFile(t *testing.T, content string) string {
	t.Helper()
	return writeNamedConfigFile(t, "config.yaml", content)
}

func writeNamedConfigFile(t *testing.T, fileName, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), fileName)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config file: %v", err)
	}
	return path
}

func assertDebugCaptureConfig(t *testing.T, cfg *Config, want expectedDebugCaptureConfig) {
	t.Helper()
	if cfg.DebugCaptureMemoryCeilingBytes != want.memoryCeilingBytes {
		t.Errorf("DebugCaptureMemoryCeilingBytes = %d, want %d", cfg.DebugCaptureMemoryCeilingBytes, want.memoryCeilingBytes)
	}
	if cfg.DebugCaptureMaxActiveRecords != want.maxActiveRecords {
		t.Errorf("DebugCaptureMaxActiveRecords = %d, want %d", cfg.DebugCaptureMaxActiveRecords, want.maxActiveRecords)
	}
	if cfg.DebugCaptureMaxActiveTraces != want.maxActiveTraces {
		t.Errorf("DebugCaptureMaxActiveTraces = %d, want %d", cfg.DebugCaptureMaxActiveTraces, want.maxActiveTraces)
	}
	if cfg.DebugCaptureMaxTransitionsPerTrace != want.maxTransitionsPerTrace {
		t.Errorf("DebugCaptureMaxTransitionsPerTrace = %d, want %d", cfg.DebugCaptureMaxTransitionsPerTrace, want.maxTransitionsPerTrace)
	}
	if cfg.DebugCaptureMaxPendingExports != want.maxPendingExports {
		t.Errorf("DebugCaptureMaxPendingExports = %d, want %d", cfg.DebugCaptureMaxPendingExports, want.maxPendingExports)
	}
	if cfg.DebugCaptureMaxConcurrentDownloads != want.maxConcurrentDownloads {
		t.Errorf("DebugCaptureMaxConcurrentDownloads = %d, want %d", cfg.DebugCaptureMaxConcurrentDownloads, want.maxConcurrentDownloads)
	}
	if cfg.DebugCaptureDetailPreviewBytes != want.detailPreviewBytes {
		t.Errorf("DebugCaptureDetailPreviewBytes = %d, want %d", cfg.DebugCaptureDetailPreviewBytes, want.detailPreviewBytes)
	}
	if cfg.DebugCaptureDetailEventLimit != want.detailEventLimit {
		t.Errorf("DebugCaptureDetailEventLimit = %d, want %d", cfg.DebugCaptureDetailEventLimit, want.detailEventLimit)
	}
	if cfg.DebugCaptureDownloadTokenTTL != want.downloadTokenTTL {
		t.Errorf("DebugCaptureDownloadTokenTTL = %s, want %s", cfg.DebugCaptureDownloadTokenTTL, want.downloadTokenTTL)
	}
	if cfg.DebugCaptureMaxRecordsPerProvider != want.maxRecordsPerProvider {
		t.Errorf("DebugCaptureMaxRecordsPerProvider = %d, want %d", cfg.DebugCaptureMaxRecordsPerProvider, want.maxRecordsPerProvider)
	}
	if cfg.DebugCaptureChunkBytes != want.chunkBytes {
		t.Errorf("DebugCaptureChunkBytes = %d, want %d", cfg.DebugCaptureChunkBytes, want.chunkBytes)
	}
	if cfg.DebugCaptureExportLineBytes != want.exportLineBytes {
		t.Errorf("DebugCaptureExportLineBytes = %d, want %d", cfg.DebugCaptureExportLineBytes, want.exportLineBytes)
	}
}
