package pending

import (
	"errors"
	"sync"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/responseanalysis/allocation"
)

func TestRequestAccountExactLimitsAndDenialRollback(t *testing.T) {
	process, err := NewProcessBudget(64)
	if err != nil {
		t.Fatal(err)
	}
	account, err := newRequestAccount(process, 10)
	if err != nil {
		t.Fatal(err)
	}
	first, err := account.Reserve(allocation.ClassRawPrefix, 6)
	if err != nil {
		t.Fatal(err)
	}
	second, err := account.Reserve(allocation.ClassSemanticFields, 4)
	if err != nil {
		t.Fatal(err)
	}
	if used, peak := account.snapshot(); used != 10 || peak != 10 || process.Used() != 10 {
		t.Fatalf("unexpected exact-limit accounting: used=%d peak=%d process=%d", used, peak, process.Used())
	}
	_, err = account.Reserve(allocation.ClassFramingBuffer, 1)
	assertDenial(t, err, allocation.DenialRequestMemoryExhausted)
	if used, _ := account.snapshot(); used != 10 || process.Used() != 10 {
		t.Fatalf("denial mutated counters: request=%d process=%d", used, process.Used())
	}

	first.Release()
	first.Release()
	second.Release()
	if used, _ := account.snapshot(); used != 0 || process.Used() != 0 {
		t.Fatalf("release leaked counters: request=%d process=%d", used, process.Used())
	}
	account.close()
	if _, err := account.Reserve(allocation.ClassRawPrefix, 1); !errors.Is(err, ErrAccountClosed) {
		t.Fatalf("reserve after close error = %v", err)
	}
}

func TestRequestAccountProcessDenial(t *testing.T) {
	process, err := NewProcessBudget(5)
	if err != nil {
		t.Fatal(err)
	}
	account, err := newRequestAccount(process, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer account.close()
	_, err = account.Reserve(allocation.ClassDecodedBuffer, 6)
	assertDenial(t, err, allocation.DenialProcessMemoryExhausted)
	if used, peak := account.snapshot(); used != 0 || peak != 0 || process.Used() != 0 || process.Peak() != 0 {
		t.Fatalf("process denial changed accounting: request=%d/%d process=%d/%d", used, peak, process.Used(), process.Peak())
	}
}

func TestDecoderWorkingSetUsesOnlyProcessCeiling(t *testing.T) {
	const workset = gzipDecoderWorkingSetBytes
	process, err := NewProcessBudget(2 * workset)
	if err != nil {
		t.Fatal(err)
	}
	account, err := newRequestAccount(process, workset)
	if err != nil {
		t.Fatal(err)
	}
	decoder, err := account.Reserve(allocation.ClassDecoderWorkingSet, workset)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := account.Reserve(allocation.ClassRawPrefix, workset)
	if err != nil {
		t.Fatal(err)
	}
	if used, peak := account.snapshot(); used != workset || peak != workset || process.Used() != 2*workset {
		t.Fatalf("working-set accounting request=%d/%d process=%d", used, peak, process.Used())
	}
	_, err = account.Reserve(allocation.ClassRawPrefix, 1)
	assertDenial(t, err, allocation.DenialRequestMemoryExhausted)
	raw.Release()
	decoder.Release()
	account.close()
	if process.Used() != 0 {
		t.Fatalf("working-set grants leaked %d bytes", process.Used())
	}
}

func TestDecoderWorkingSetRejectsWrongSizeAndRemainsOneShotAfterRelease(t *testing.T) {
	process, err := NewProcessBudget(2 * gzipDecoderWorkingSetBytes)
	if err != nil {
		t.Fatal(err)
	}
	account, err := newRequestAccount(process, gzipDecoderWorkingSetBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer account.close()

	for _, capacity := range []int{
		-1,
		0,
		1,
		gzipDecoderWorkingSetBytes - 1,
		gzipDecoderWorkingSetBytes + 1,
	} {
		_, reserveErr := account.Reserve(allocation.ClassDecoderWorkingSet, capacity)
		if !errors.Is(reserveErr, ErrInvalidGzipDecoderWorkingSet) {
			t.Fatalf("capacity %d error = %v", capacity, reserveErr)
		}
		if _, isDenial := allocation.DenialReasonOf(reserveErr); isDenial {
			t.Fatalf("capacity %d incorrectly reported a memory denial: %v", capacity, reserveErr)
		}
		if used, peak := account.snapshot(); used != 0 || peak != 0 || process.Used() != 0 || process.Peak() != 0 {
			t.Fatalf("capacity %d mutated accounting: request=%d/%d process=%d/%d", capacity, used, peak, process.Used(), process.Peak())
		}
	}
	if _, _, reserveErr := account.reserveUpTo(
		allocation.ClassDecoderWorkingSet,
		gzipDecoderWorkingSetBytes,
		gzipDecoderWorkingSetBytes,
	); !errors.Is(reserveErr, ErrInvalidGzipDecoderWorkingSet) {
		t.Fatalf("variable-capacity working-set error = %v", reserveErr)
	}

	workset, err := account.Reserve(allocation.ClassDecoderWorkingSet, gzipDecoderWorkingSetBytes)
	if err != nil {
		t.Fatal(err)
	}
	_, err = account.Reserve(allocation.ClassDecoderWorkingSet, gzipDecoderWorkingSetBytes)
	if !errors.Is(err, ErrGzipDecoderWorkingSetExhausted) {
		t.Fatalf("concurrent working-set error = %v", err)
	}
	if used, peak := account.snapshot(); used != 0 || peak != 0 || process.Used() != gzipDecoderWorkingSetBytes || process.Peak() != gzipDecoderWorkingSetBytes {
		t.Fatalf("repeat mutated accounting: request=%d/%d process=%d/%d", used, peak, process.Used(), process.Peak())
	}

	workset.Release()
	_, err = account.Reserve(allocation.ClassDecoderWorkingSet, gzipDecoderWorkingSetBytes)
	if !errors.Is(err, ErrGzipDecoderWorkingSetExhausted) {
		t.Fatalf("post-release working-set error = %v", err)
	}
	if used, peak := account.snapshot(); used != 0 || peak != 0 || process.Used() != 0 || process.Peak() != gzipDecoderWorkingSetBytes {
		t.Fatalf("post-release repeat mutated accounting: request=%d/%d process=%d/%d", used, peak, process.Used(), process.Peak())
	}
}

func TestDecoderWorkingSetProcessDenial(t *testing.T) {
	process, err := NewProcessBudget(gzipDecoderWorkingSetBytes)
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := newRequestAccount(process, gzipDecoderWorkingSetBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.close()
	occupied, err := blocker.Reserve(allocation.ClassRawPrefix, gzipDecoderWorkingSetBytes)
	if err != nil {
		t.Fatal(err)
	}
	account, err := newRequestAccount(process, gzipDecoderWorkingSetBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer account.close()
	_, err = account.Reserve(allocation.ClassDecoderWorkingSet, gzipDecoderWorkingSetBytes)
	assertDenial(t, err, allocation.DenialProcessMemoryExhausted)
	if used, peak := account.snapshot(); used != 0 || peak != 0 || process.Used() != gzipDecoderWorkingSetBytes {
		t.Fatalf("denial mutated target account: request=%d/%d process=%d", used, peak, process.Used())
	}

	occupied.Release()
	workset, err := account.Reserve(allocation.ClassDecoderWorkingSet, gzipDecoderWorkingSetBytes)
	if err != nil {
		t.Fatalf("process denial consumed one-shot claim: %v", err)
	}
	workset.Release()
	if process.Used() != 0 {
		t.Fatalf("process denial retry leaked %d bytes", process.Used())
	}
}

func TestDecoderWorkingSetConcurrentReservationHasOneWinner(t *testing.T) {
	process, err := NewProcessBudget(2 * gzipDecoderWorkingSetBytes)
	if err != nil {
		t.Fatal(err)
	}
	account, err := newRequestAccount(process, gzipDecoderWorkingSetBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer account.close()

	start := make(chan struct{})
	results := make(chan struct {
		grant allocation.Grant
		err   error
	}, 2)
	for range 2 {
		go func() {
			<-start
			grant, reserveErr := account.Reserve(allocation.ClassDecoderWorkingSet, gzipDecoderWorkingSetBytes)
			results <- struct {
				grant allocation.Grant
				err   error
			}{grant: grant, err: reserveErr}
		}()
	}
	close(start)

	winners := 0
	losers := 0
	for range 2 {
		result := <-results
		switch {
		case result.err == nil:
			winners++
			result.grant.Release()
		case errors.Is(result.err, ErrGzipDecoderWorkingSetExhausted):
			losers++
		default:
			t.Fatalf("unexpected reservation error: %v", result.err)
		}
	}
	if winners != 1 || losers != 1 {
		t.Fatalf("winners=%d losers=%d", winners, losers)
	}
	if used, peak := account.snapshot(); used != 0 || peak != 0 || process.Used() != 0 || process.Peak() != gzipDecoderWorkingSetBytes {
		t.Fatalf("concurrent one-shot accounting: request=%d/%d process=%d/%d", used, peak, process.Used(), process.Peak())
	}
}

func TestRequestAccountCloseReleaseRaceIsIdempotent(t *testing.T) {
	tests := []struct {
		name     string
		class    allocation.Class
		capacity int
	}{
		{name: "request-charged", class: allocation.ClassRawPrefix, capacity: 8},
		{name: "process-only gzip workset", class: allocation.ClassDecoderWorkingSet, capacity: gzipDecoderWorkingSetBytes},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for iteration := range 1_000 {
				process, err := NewProcessBudget(test.capacity)
				if err != nil {
					t.Fatal(err)
				}
				account, err := newRequestAccount(process, test.capacity)
				if err != nil {
					t.Fatal(err)
				}
				grant, err := account.Reserve(test.class, test.capacity)
				if err != nil {
					t.Fatal(err)
				}
				start := make(chan struct{})
				var wait sync.WaitGroup
				wait.Add(2)
				go func() {
					defer wait.Done()
					<-start
					account.close()
				}()
				go func() {
					defer wait.Done()
					<-start
					grant.Release()
				}()
				close(start)
				wait.Wait()
				grant.Release()
				account.close()
				if process.Used() != 0 {
					t.Fatalf("iteration %d left %d bytes", iteration, process.Used())
				}
			}
		})
	}
}

func TestProcessBudgetConcurrentHardCap(t *testing.T) {
	const (
		grantBytes   = 256 * 1024
		workerCount  = 257
		processLimit = 64 * 1024 * 1024
	)
	process, err := NewProcessBudget(processLimit)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	release := make(chan struct{})
	results := make(chan bool, workerCount)
	var wait sync.WaitGroup
	wait.Add(workerCount)
	for range workerCount {
		go func() {
			defer wait.Done()
			account, accountErr := newRequestAccount(process, grantBytes)
			if accountErr != nil {
				results <- false
				return
			}
			<-start
			grant, reserveErr := account.Reserve(allocation.ClassRawPrefix, grantBytes)
			results <- reserveErr == nil
			if reserveErr == nil {
				<-release
				grant.Release()
			}
			account.close()
		}()
	}
	close(start)
	succeeded := 0
	for range workerCount {
		if <-results {
			succeeded++
		}
	}
	if succeeded != processLimit/grantBytes {
		t.Fatalf("successful reservations = %d, want %d", succeeded, processLimit/grantBytes)
	}
	if process.Used() != processLimit || process.Peak() != processLimit {
		t.Fatalf("hard cap accounting used=%d peak=%d", process.Used(), process.Peak())
	}
	close(release)
	wait.Wait()
	if process.Used() != 0 {
		t.Fatalf("concurrent grants leaked %d bytes", process.Used())
	}
}

func TestProcessBudgetValidationAndZeroGrant(t *testing.T) {
	if _, err := NewProcessBudget(0); err == nil {
		t.Fatal("expected invalid process limit")
	}
	process, _ := NewProcessBudget(1)
	account, _ := newRequestAccount(process, 1)
	grant, err := account.Reserve(allocation.ClassRawPrefix, 0)
	if err != nil || grant == nil {
		t.Fatalf("zero grant = %v, %v", grant, err)
	}
	grant.Release()
	if _, err := account.Reserve(allocation.ClassRawPrefix, -1); err == nil {
		t.Fatal("expected negative capacity failure")
	}
	account.close()
	if process.Limit() != 1 || process.Peak() != 0 {
		t.Fatalf("unexpected budget facts limit=%d peak=%d", process.Limit(), process.Peak())
	}
}

func assertDenial(t *testing.T, err error, want allocation.DenialReason) {
	t.Helper()
	reason, ok := allocation.DenialReasonOf(err)
	if !ok || reason != want {
		t.Fatalf("denial = %v (%q), want %q", err, reason, want)
	}
}
