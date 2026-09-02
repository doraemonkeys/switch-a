package codexheaders

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

const fixtureVersionDirectory = "codex-desktop-0.150.0-alpha.8"

var confirmedFixturePaths = []string{
	"response.create.client_metadata.session_id",
	"response.create.client_metadata.thread_id",
	"response.create.client_metadata[\"x-codex-window-id\"]",
	"response.create.client_metadata[\"x-codex-turn-metadata\"]",
	"response.create.previous_response_id",
	"codex.response.metadata.headers[\"x-codex-turn-state\"]",
	"response.created.response.id",
	"response.in_progress.response.id",
	"response.completed.response.id",
	"http_sse.response.metadata.response_id",
}

type fixtureManifest struct {
	SchemaVersion int `json:"schema_version"`
	Codex         struct {
		Version string `json:"version"`
	} `json:"codex"`
	OfficialSource struct {
		Tag    string   `json:"tag"`
		Commit string   `json:"commit"`
		Files  []string `json:"files"`
	} `json:"official_source"`
	CaptureEvidence struct {
		SourceIsGitignored bool `json:"source_is_gitignored"`
		Handshake          struct {
			StatusCode             int  `json:"status_code"`
			TurnStateHeaderPresent bool `json:"turn_state_header_present"`
		} `json:"handshake"`
	} `json:"capture_evidence"`
	Fixtures []struct {
		File              string `json:"file"`
		WireFormat        string `json:"wire_format"`
		EvidenceKind      string `json:"evidence_kind"`
		Direction         string `json:"direction"`
		EventType         string `json:"event_type"`
		MessageSequence   int    `json:"message_sequence"`
		CapturedFrameSize int    `json:"captured_frame_size"`
		FixtureSHA256     string `json:"fixture_sha256"`
		CapturedFrameHash string `json:"captured_frame_sha256"`
		SourceFile        string `json:"source_file"`
		SourceSymbol      string `json:"source_symbol"`
	} `json:"fixtures"`
	ConfirmedPaths    []string `json:"confirmed_paths"`
	ConfirmedAbsences []string `json:"confirmed_absences"`
	UnconfirmedPaths  []string `json:"unconfirmed_paths"`
	EvidenceGaps      []string `json:"evidence_gaps"`
}

type responseCreateFixture struct {
	Type               string            `json:"type"`
	Generate           *bool             `json:"generate"`
	PreviousResponseID *string           `json:"previous_response_id"`
	ClientMetadata     map[string]string `json:"client_metadata"`
}

type responseMetadataFixture struct {
	Type    string            `json:"type"`
	Headers map[string]string `json:"headers"`
}

type responseReferenceFixture struct {
	Type     string `json:"type"`
	Response struct {
		ID string `json:"id"`
	} `json:"response"`
}

func TestFixtureFilesUseLF(t *testing.T) {
	fixtureFiles := 0
	err := filepath.WalkDir("testdata", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		fixtureFiles++
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// Fixture digests encode wire bytes, so checkout translation must not make
		// protocol evidence depend on the developer's operating system.
		if bytes.IndexByte(raw, '\r') >= 0 {
			return fmt.Errorf("%s contains a carriage-return byte", filepath.ToSlash(path))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if fixtureFiles == 0 {
		t.Fatal("fixture tree is empty")
	}
}

func TestCodexDesktop0150Alpha8FixtureContract(t *testing.T) {
	manifest := loadFixtureManifest(t)
	if manifest.SchemaVersion != 2 {
		t.Fatalf("schema version = %d, want 2", manifest.SchemaVersion)
	}
	if manifest.Codex.Version != "0.150.0-alpha.8" {
		t.Fatalf("Codex version = %q", manifest.Codex.Version)
	}
	if manifest.OfficialSource.Tag != "rust-v0.150.0-alpha.8" ||
		manifest.OfficialSource.Commit != "fcbdb57851be70192fd0c21faa9e529146e93ff1" {
		t.Fatalf("official source = %#v", manifest.OfficialSource)
	}
	if !manifest.CaptureEvidence.SourceIsGitignored {
		t.Fatal("capture must remain provenance only; committed tests must use testdata")
	}
	if manifest.CaptureEvidence.Handshake.StatusCode != 101 ||
		manifest.CaptureEvidence.Handshake.TurnStateHeaderPresent {
		t.Fatalf("handshake evidence = %#v", manifest.CaptureEvidence.Handshake)
	}
	if !reflect.DeepEqual(manifest.ConfirmedPaths, confirmedFixturePaths) {
		t.Fatalf("confirmed paths = %#v, want %#v", manifest.ConfirmedPaths, confirmedFixturePaths)
	}
	if !contains(manifest.ConfirmedAbsences, "second response.create.client_metadata[\"x-codex-turn-state\"]") {
		t.Fatalf("confirmed absences = %#v", manifest.ConfirmedAbsences)
	}
	for _, unsupported := range []string{"response.inject.response_id", "response.append", "http_json.response.id"} {
		if !contains(manifest.UnconfirmedPaths, unsupported) {
			t.Fatalf("unconfirmed paths %q missing %q", manifest.UnconfirmedPaths, unsupported)
		}
	}
	for _, gap := range []string{
		"target-version response.inject interaction capture",
		"target-version non-streaming HTTP JSON response capture or source path",
	} {
		if !contains(manifest.EvidenceGaps, gap) {
			t.Fatalf("evidence gaps %q missing %q", manifest.EvidenceGaps, gap)
		}
	}

	warmup := decodeFixture[responseCreateFixture](t, "ws-client-response-create-warmup.json")
	second := decodeFixture[responseCreateFixture](t, "ws-client-response-create-second.json")
	metadata := decodeFixture[responseMetadataFixture](t, "ws-server-codex-response-metadata.json")

	if warmup.Type != "response.create" || warmup.Generate == nil || *warmup.Generate {
		t.Fatalf("warmup envelope = %#v", warmup)
	}
	if second.Type != "response.create" || second.PreviousResponseID == nil || *second.PreviousResponseID == "" {
		t.Fatalf("second response.create envelope = %#v", second)
	}
	assertConfirmedClientMetadata(t, warmup.ClientMetadata)
	assertConfirmedClientMetadata(t, second.ClientMetadata)
	for _, key := range []string{"session_id", "thread_id", "x-codex-window-id"} {
		if warmup.ClientMetadata[key] != second.ClientMetadata[key] {
			t.Fatalf("%s changed within one turn: warmup=%q second=%q", key, warmup.ClientMetadata[key], second.ClientMetadata[key])
		}
	}
	if _, present := second.ClientMetadata["x-codex-turn-state"]; present {
		t.Fatal("target capture confirms that the same-turn second response.create has no x-codex-turn-state")
	}
	if metadata.Type != "codex.response.metadata" || metadata.Headers["x-codex-turn-state"] == "" {
		t.Fatalf("response metadata fixture = %#v", metadata)
	}

	const capturedResponseID = "resp_0f3fd2b96e949a05016a8ee314f00087d0b428d9d5010d2a40"
	for file, eventType := range map[string]string{
		"ws-server-response-created.json":     eventResponseCreated,
		"ws-server-response-in-progress.json": eventResponseInProgress,
		"ws-server-response-completed.json":   eventResponseCompleted,
	} {
		fixture := decodeFixture[responseReferenceFixture](t, file)
		if fixture.Type != eventType || fixture.Response.ID != capturedResponseID {
			t.Fatalf("%s response reference = %#v", file, fixture)
		}
	}

	for file, eventType := range map[string]string{
		"http-sse-response-created.txt":   eventResponseCreated,
		"http-sse-response-completed.txt": eventResponseCompleted,
		"http-sse-response-metadata.txt":  eventResponseMetadata,
	} {
		raw := readFixture(t, file)
		scan := ScanServerSSE(raw, false)
		messages := scan.Messages()
		if scan.ConsumedBytes() != len(raw) || len(messages) != 1 || messages[0].EventType() != eventType {
			t.Fatalf("%s scan consumed=%d messages=%#v", file, scan.ConsumedBytes(), messages)
		}
		result := DecideServerMessage(messages[0], fixedLookup(OwnerCurrent))
		decision := requireOnlyDecision(t, result)
		if decision.Field() != FieldResponseReference || decision.Action() != ActionForward {
			t.Fatalf("%s decision = %#v", file, decision)
		}
		if !bytes.Equal(result.ReplayBytes(), raw) || &result.ReplayBytes()[0] != &raw[0] {
			t.Fatalf("%s did not retain exact fixture bytes", file)
		}
	}
}

func TestCodexDesktop0150Alpha8FixturesReplayByteForByte(t *testing.T) {
	manifest := loadFixtureManifest(t)
	for _, fixture := range manifest.Fixtures {
		t.Run(fixture.File, func(t *testing.T) {
			raw := readFixture(t, fixture.File)
			if fixture.Direction != "client_to_upstream" && fixture.Direction != "upstream_to_client" {
				t.Fatalf("fixture direction = %q", fixture.Direction)
			}
			switch fixture.WireFormat {
			case "websocket_text":
				eventType, replay, err := observeFixtureWithoutReencoding(raw)
				if err != nil {
					t.Fatal(err)
				}
				if eventType != fixture.EventType {
					t.Fatalf("event type = %q, want %q", eventType, fixture.EventType)
				}
				if !bytes.Equal(replay, raw) || len(raw) > 0 && &replay[0] != &raw[0] {
					t.Fatal("fixture replay replaced or changed the original wire buffer")
				}
			case "sse_event":
				scan := ScanServerSSE(raw, false)
				messages := scan.Messages()
				if scan.ConsumedBytes() != len(raw) || len(messages) != 1 || messages[0].EventType() != fixture.EventType {
					t.Fatalf("SSE fixture scan = consumed %d messages %#v", scan.ConsumedBytes(), messages)
				}
				if replay := messages[0].ReplayBytes(); !bytes.Equal(replay, raw) || &replay[0] != &raw[0] {
					t.Fatal("SSE fixture replay replaced or changed the original wire buffer")
				}
			default:
				t.Fatalf("fixture wire format = %q", fixture.WireFormat)
			}
			digest := sha256.Sum256(raw)
			if got := hex.EncodeToString(digest[:]); got != fixture.FixtureSHA256 {
				t.Fatalf("fixture sha256 = %s, want %s", got, fixture.FixtureSHA256)
			}
			switch fixture.EvidenceKind {
			case "capture":
				if fixture.MessageSequence <= 0 || fixture.CapturedFrameSize <= 0 || len(fixture.CapturedFrameHash) != sha256.Size*2 || fixture.SourceFile != "" || fixture.SourceSymbol != "" {
					t.Fatalf("incomplete capture provenance = %#v", fixture)
				}
			case "official_source":
				if fixture.MessageSequence != 0 || fixture.CapturedFrameSize != 0 || fixture.CapturedFrameHash != "" || fixture.SourceFile == "" || fixture.SourceSymbol == "" || !contains(manifest.OfficialSource.Files, fixture.SourceFile) {
					t.Fatalf("incomplete source provenance = %#v", fixture)
				}
			default:
				t.Fatalf("fixture evidence kind = %q", fixture.EvidenceKind)
			}
		})
	}
}

func observeFixtureWithoutReencoding(raw []byte) (string, []byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var envelope struct {
		Type string `json:"type"`
	}
	if err := decoder.Decode(&envelope); err != nil {
		return "", nil, fmt.Errorf("decode fixture envelope: %w", err)
	}
	if envelope.Type == "" {
		return "", nil, fmt.Errorf("fixture envelope has no event type")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return "", nil, fmt.Errorf("fixture has trailing JSON: %v", err)
	}
	// The observer returns the caller-owned bytes because normalization would
	// invalidate opaque state and make replay behavior depend on JSON encoding.
	return envelope.Type, raw, nil
}

func assertConfirmedClientMetadata(t *testing.T, metadata map[string]string) {
	t.Helper()
	for _, key := range []string{"session_id", "thread_id", "x-codex-window-id", "x-codex-turn-metadata"} {
		if metadata[key] == "" {
			t.Fatalf("client_metadata[%q] is absent or empty", key)
		}
	}
}

func loadFixtureManifest(t *testing.T) fixtureManifest {
	t.Helper()
	return decodeFixture[fixtureManifest](t, "manifest.json")
}

func decodeFixture[T any](t *testing.T, name string) T {
	t.Helper()
	raw := readFixture(t, name)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var value T
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("%s has trailing JSON: %v", name, err)
	}
	return value
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("testdata", fixtureVersionDirectory, name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	return raw
}

func contains(values []string, want string) bool {
	return slices.Contains(values, want)
}
