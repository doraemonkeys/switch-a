package retry

import "testing"

func TestBudgetSeparatesProviderRetriesAndGlobalAttempts(t *testing.T) {
	b := NewBudget(3)
	if b.CanRetry("a", 4) {
		t.Fatal("retry without an initial attempt")
	}
	for _, id := range []string{"a", "a", "b"} {
		if !b.CanStart() {
			t.Fatal("budget exhausted early")
		}
		if err := b.Start(id); err != nil {
			t.Fatal(err)
		}
	}
	if b.CanStart() || b.CanRetry("a", 4) || b.Attempts() != 3 || b.ProviderAttempts("a") != 2 || b.ProviderAttempts("b") != 1 {
		t.Fatalf("incorrect attempt accounting: %+v", b)
	}
	for _, id := range []string{"a", "c"} {
		if err := b.Start(id); err == nil || b.Attempts() != 3 {
			t.Fatal("exhausted budget admitted or counted a dispatch")
		}
	}
}

func TestProviderBudgetStillBoundsUnlimitedGlobalAttempts(t *testing.T) {
	for _, limit := range []int{0, -1} {
		b := NewBudget(limit)
		if err := b.Start("a"); err != nil {
			t.Fatal(err)
		}
		if b.CanRetry("a", 0) || b.CanRetry("a", -1) || !b.CanRetry("a", 1) {
			t.Fatal("MaxRetries must count retries after the initial attempt")
		}
		if err := b.Start("a"); err != nil {
			t.Fatal(err)
		}
		if b.CanRetry("a", 1) || !b.CanStart() {
			t.Fatal("provider and global budgets were conflated")
		}
	}
}
