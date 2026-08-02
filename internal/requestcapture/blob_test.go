package requestcapture

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"sync"
	"testing"
)

func TestBlobChunkBoundariesAndChecksum(t *testing.T) {
	manager := newTestManager(t, func(cfg *Config) {
		cfg.ChunkBytes = MinimumChunkBytes
		cfg.ExportLineBytes = 4096
	})
	startTestSession(t, manager, 2, 1<<20, "selected")
	session := manager.active.Load()
	session.mu.Lock()
	baseCharge := session.chargedBytes
	for _, size := range []int{0, 1, 3, 4, 5, 9} {
		payload := bytes.Repeat([]byte{byte(size + 1)}, size)
		value, complete := newImmutableBlobLocked(session, payload)
		if !complete {
			session.mu.Unlock()
			t.Fatalf("size %d was incomplete", size)
		}
		preview := previewBlob(value, size+1)
		decoded, err := base64.StdEncoding.DecodeString(preview.DataBase64)
		if err != nil {
			session.mu.Unlock()
			t.Fatalf("size %d base64 error = %v", size, err)
		}
		if !bytes.Equal(decoded, payload) || preview.CapturedBytes != int64(size) || preview.Truncated {
			session.mu.Unlock()
			t.Fatalf("size %d preview = %#v data %v", size, preview, decoded)
		}
		sum := sha256.Sum256(payload)
		if preview.ChecksumSHA256 != hex.EncodeToString(sum[:]) {
			session.mu.Unlock()
			t.Fatalf("size %d checksum = %q", size, preview.ChecksumSHA256)
		}
		if value != nil {
			wantChunks := (size + manager.cfg.chunkBytes - 1) / manager.cfg.chunkBytes
			if value.chunkCount != wantChunks {
				session.mu.Unlock()
				t.Fatalf("size %d chunks = %d, want %d", size, value.chunkCount, wantChunks)
			}
		}
		releaseBlobLocked(value)
	}
	if session.chargedBytes != baseCharge {
		session.mu.Unlock()
		t.Fatalf("blob charges leaked: before=%d after=%d", baseCharge, session.chargedBytes)
	}
	session.mu.Unlock()
}

func TestBlobSnapshotViewIsImmutableAndPinned(t *testing.T) {
	manager := newTestManager(t, func(cfg *Config) {
		cfg.ChunkBytes = MinimumChunkBytes
		cfg.ExportLineBytes = 4096
	})
	startTestSession(t, manager, 2, 1<<20, "selected")
	session := manager.active.Load()
	session.mu.Lock()
	builder := blobBuilder{}
	if captured := builder.appendLocked(session, []byte("abc")); captured != 3 {
		session.mu.Unlock()
		t.Fatalf("captured = %d", captured)
	}
	view := snapshotBlobLocked(builder.value)
	manager.mu.Lock()
	pinnedAfterSnapshot := manager.processPinned
	manager.mu.Unlock()
	if captured := builder.appendLocked(session, []byte("def")); captured != 3 {
		session.mu.Unlock()
		t.Fatalf("second captured = %d", captured)
	}
	snapshotPreview := previewView(view, 64)
	livePreview := previewBlob(builder.value, 64)
	if snapshotPreview.DataBase64 != base64.StdEncoding.EncodeToString([]byte("abc")) {
		session.mu.Unlock()
		t.Fatalf("snapshot changed: %#v", snapshotPreview)
	}
	if livePreview.DataBase64 != base64.StdEncoding.EncodeToString([]byte("abcdef")) {
		session.mu.Unlock()
		t.Fatalf("live blob = %#v", livePreview)
	}
	if pinnedAfterSnapshot == 0 {
		session.mu.Unlock()
		t.Fatal("snapshot did not pin chunk memory")
	}
	releaseBlobLocked(builder.value)
	session.mu.Unlock()

	// The view remains readable after the live blob owner releases.
	if got := previewView(view, 64).DataBase64; got != base64.StdEncoding.EncodeToString([]byte("abc")) {
		t.Fatalf("released live owner changed view: %q", got)
	}
	view.release()
	manager.mu.Lock()
	pinnedAfterRelease := manager.processPinned
	manager.mu.Unlock()
	if pinnedAfterRelease != 0 {
		t.Fatalf("pinned bytes after release = %d", pinnedAfterRelease)
	}
}

func TestBlobQuerySnapshotsDoNotSealOrFragmentMutableTail(t *testing.T) {
	manager := newTestManager(t, func(cfg *Config) {
		cfg.ChunkBytes = MinimumChunkBytes
	})
	startTestSession(t, manager, 2, 1<<20, "selected")
	session := manager.active.Load()

	manager.mu.Lock()
	baseline := session.chargedBytes
	manager.mu.Unlock()
	builder := blobBuilder{}
	for index := 0; index < 2*MinimumChunkBytes; index++ {
		session.mu.Lock()
		if captured := builder.appendLocked(session, []byte{byte(index)}); captured != 1 {
			session.mu.Unlock()
			t.Fatalf("append %d captured = %d", index, captured)
		}
		view := snapshotBlobPrefixLocked(builder.value, 64)
		session.mu.Unlock()

		preview := previewView(view, 64)
		if preview.CapturedBytes != int64(index+1) {
			view.release()
			t.Fatalf("query %d captured bytes = %d", index, preview.CapturedBytes)
		}
		view.release()
	}

	session.mu.Lock()
	value := builder.value
	if value.chunkCount != 2 {
		session.mu.Unlock()
		t.Fatalf("query polling fragmented payload into %d chunks, want 2", value.chunkCount)
	}
	if value.last == nil || value.last.sealed {
		session.mu.Unlock()
		t.Fatal("query polling sealed the mutable tail")
	}
	session.mu.Unlock()

	manager.mu.Lock()
	chargedDelta := session.chargedBytes - baseline
	manager.mu.Unlock()
	wantCharge := blobBaseChargeBytes + checksumStateChargeBytes +
		2*(int64(MinimumChunkBytes)+chunkMetadataChargeBytes)
	if chargedDelta != wantCharge {
		t.Fatalf("retained blob charge = %d, want %d", chargedDelta, wantCharge)
	}

	session.mu.Lock()
	releaseBlobLocked(value)
	session.mu.Unlock()
	manager.mu.Lock()
	afterRelease := session.chargedBytes
	manager.mu.Unlock()
	if afterRelease != baseline {
		t.Fatalf("blob release retained %d bytes, want baseline %d", afterRelease, baseline)
	}
}

func TestBlobQueryViewHeaderIsRaceFreeDuringAppend(t *testing.T) {
	manager := newTestManager(t, func(cfg *Config) {
		cfg.ChunkBytes = MinimumChunkBytes
	})
	startTestSession(t, manager, 2, 1<<20, "selected")
	session := manager.active.Load()
	prefix := bytes.Repeat([]byte{0x5a}, 64)

	session.mu.Lock()
	builder := blobBuilder{}
	if captured := builder.appendLocked(session, prefix); captured != len(prefix) {
		session.mu.Unlock()
		t.Fatalf("prefix captured = %d", captured)
	}
	view := snapshotBlobPrefixLocked(builder.value, len(prefix))
	session.mu.Unlock()

	var wait sync.WaitGroup
	wait.Add(1)
	previewErr := make(chan string, 1)
	go func() {
		defer wait.Done()
		want := base64.StdEncoding.EncodeToString(prefix)
		for range 4096 {
			if got := previewView(view, len(prefix)).DataBase64; got != want {
				previewErr <- got
				return
			}
		}
	}()
	for range 1024 {
		session.mu.Lock()
		if captured := builder.appendLocked(session, []byte{0x6b}); captured != 1 {
			session.mu.Unlock()
			t.Fatalf("concurrent append captured = %d", captured)
		}
		session.mu.Unlock()
	}
	wait.Wait()
	close(previewErr)
	if got, failed := <-previewErr; failed {
		t.Fatalf("query view changed during append: %q", got)
	}

	view.release()
	session.mu.Lock()
	releaseBlobLocked(builder.value)
	session.mu.Unlock()
}

func TestExportBlobFreezeSealsOnlyCapturedPrefix(t *testing.T) {
	manager := newTestManager(t, func(cfg *Config) {
		cfg.ChunkBytes = MinimumChunkBytes
	})
	startTestSession(t, manager, 2, 1<<20, "selected")
	session := manager.active.Load()

	session.mu.Lock()
	builder := blobBuilder{}
	if captured := builder.appendLocked(session, []byte("abc")); captured != 3 {
		session.mu.Unlock()
		t.Fatalf("initial captured = %d", captured)
	}
	source, err := freezeBlobPrefixLocked(builder.value)
	if err != nil {
		session.mu.Unlock()
		t.Fatalf("freezeBlobPrefixLocked() error = %v", err)
	}
	manager.mu.Lock()
	pinnedBeforeAppend := manager.processPinned
	manager.mu.Unlock()
	if captured := builder.appendLocked(session, []byte("def")); captured != 3 {
		session.mu.Unlock()
		t.Fatalf("post-freeze captured = %d", captured)
	}
	if builder.value.chunkCount != 2 {
		session.mu.Unlock()
		t.Fatalf("post-freeze chunks = %d, want 2", builder.value.chunkCount)
	}
	manager.mu.Lock()
	pinnedAfterAppend := manager.processPinned
	manager.mu.Unlock()
	if pinnedAfterAppend != pinnedBeforeAppend {
		session.mu.Unlock()
		t.Fatalf("post-freeze append changed pinned bytes from %d to %d", pinnedBeforeAppend, pinnedAfterAppend)
	}
	session.mu.Unlock()

	state := &exportState{phase: exportPhaseAcquiring, done: make(chan struct{})}
	view, err := source.materialize(
		newExportCancellationProbe(context.Background(), state),
	)
	if err != nil {
		t.Fatalf("materialize() error = %v", err)
	}
	session.mu.Lock()
	releaseBlobLocked(builder.value)
	session.mu.Unlock()
	if got := previewView(view, 64).DataBase64; got != base64.StdEncoding.EncodeToString([]byte("abc")) {
		t.Fatalf("export prefix changed after live append/release: %q", got)
	}
	view.release()

	manager.mu.Lock()
	pinnedAfterRelease := manager.processPinned
	manager.mu.Unlock()
	if pinnedAfterRelease != 0 {
		t.Fatalf("export prefix release retained %d pinned bytes", pinnedAfterRelease)
	}
}

func TestBlobInvariantFaultsFailClosedWithoutPanicking(t *testing.T) {
	manager := newTestManager(t, nil)
	startTestSession(t, manager, 2, 1<<20, "selected")
	session := manager.active.Load()

	session.mu.Lock()
	value, complete := newImmutableBlobLocked(session, []byte("payload"))
	if !complete || value == nil || value.first == nil {
		session.mu.Unlock()
		t.Fatal("failed to create invariant test blob")
	}
	chunk := value.first

	value.chunkCount++
	queryView := snapshotBlobLocked(value)
	value.chunkCount--
	if !errors.Is(queryView.failure, ErrInternalFailure) {
		session.mu.Unlock()
		t.Fatalf("corrupt query view failure = %v", queryView.failure)
	}
	if refs, pins := chunk.refs.Load(), chunk.pins.Load(); refs != 1 || pins != 0 {
		session.mu.Unlock()
		t.Fatalf("query rollback ownership refs=%d pins=%d", refs, pins)
	}

	chunk.refs.Store(0)
	_, err := freezeBlobPrefixLocked(value)
	chunk.refs.Store(1)
	if !errors.Is(err, ErrInternalFailure) {
		session.mu.Unlock()
		t.Fatalf("corrupt export freeze error = %v", err)
	}
	releaseBlobLocked(value)
	session.mu.Unlock()
	queryView.release()

	manager.mu.Lock()
	pinned := manager.processPinned
	manager.mu.Unlock()
	if pinned != 0 {
		t.Fatalf("fault cleanup retained %d pinned bytes", pinned)
	}
}

func TestMemoryReservationIsAtomicAtBothLimits(t *testing.T) {
	manager := newTestManager(t, nil)
	startTestSession(t, manager, 2, 1<<20, "selected")
	session := manager.active.Load()
	session.mu.Lock()
	sessionBefore := session.chargedBytes
	manager.mu.Lock()
	processBefore := manager.processCharged
	manager.mu.Unlock()
	if session.reserveLocked(session.quotaBytes, false) {
		session.mu.Unlock()
		t.Fatal("reservation unexpectedly succeeded")
	}
	if session.chargedBytes != sessionBefore {
		session.mu.Unlock()
		t.Fatalf("session charge changed: before=%d after=%d", sessionBefore, session.chargedBytes)
	}
	manager.mu.Lock()
	processAfter := manager.processCharged
	manager.mu.Unlock()
	session.mu.Unlock()
	if processAfter != processBefore {
		t.Fatalf("process charge changed: before=%d after=%d", processBefore, processAfter)
	}
}

func TestBlobReferenceReleasesOnlyAtLastOwner(t *testing.T) {
	manager := newTestManager(t, nil)
	startTestSession(t, manager, 2, 1<<20, "selected")
	session := manager.active.Load()
	session.mu.Lock()
	base := session.chargedBytes
	value, complete := newImmutableBlobLocked(session, []byte("shared"))
	if !complete || !retainBlobLocked(value) {
		session.mu.Unlock()
		t.Fatal("failed to create shared blob")
	}
	charged := session.chargedBytes
	releaseBlobLocked(value)
	if session.chargedBytes != charged {
		session.mu.Unlock()
		t.Fatal("first release freed shared bytes")
	}
	releaseBlobLocked(value)
	if session.chargedBytes != base {
		session.mu.Unlock()
		t.Fatalf("last release did not free bytes: got=%d want=%d", session.chargedBytes, base)
	}
	// A released object cannot be resurrected.
	if retainBlobLocked(value) {
		session.mu.Unlock()
		t.Fatal("retain after zero succeeded")
	}
	session.mu.Unlock()
}
