package exportregistry

import (
	"math"
	"strconv"
	"testing"
)

func TestRegistryLazyBackingAndExactOwnership(t *testing.T) {
	if _, valid := New[*int](0); valid {
		t.Fatal("New(0) accepted an invalid capacity")
	}
	if _, valid := New[*int](-1); valid {
		t.Fatal("New(-1) accepted an invalid capacity")
	}

	registry, valid := New[*int](2)
	if !valid {
		t.Fatal("New(2) rejected a valid capacity")
	}
	charge, valid := BackingChargeBytes[*int](registry.Capacity())
	if !valid || charge <= 0 {
		t.Fatalf("BackingChargeBytes() = %d, %v", charge, valid)
	}
	if registry.Capacity() != 2 || registry.Count() != 0 || registry.IsMaterialized() {
		t.Fatalf("lazy registry shape = capacity:%d count:%d materialized:%v",
			registry.Capacity(), registry.Count(), registry.IsMaterialized())
	}
	if !registry.Materialize() {
		t.Fatal("Materialize() rejected the initial allocation")
	}
	if !registry.Materialize() {
		t.Fatal("Materialize() was not idempotent")
	}

	first := 1
	second := 2
	other := 3
	if registry.Put("", &first) {
		t.Fatal("Put accepted an empty key")
	}
	if !registry.Put("first", &first) || registry.Put("first", &second) {
		t.Fatal("Put did not enforce unique keys")
	}
	if !registry.Put("second", &second) || registry.Put("full", &other) {
		t.Fatal("Put did not enforce physical capacity")
	}
	if registry.Count() != 2 || !registry.ContainsExact("first", &first) ||
		registry.ContainsExact("first", &second) {
		t.Fatal("registry ownership lookup is inconsistent")
	}
	if registry.Move("first", "renamed", &second) ||
		registry.Move("first", "second", &first) ||
		!registry.Move("first", "renamed", &first) {
		t.Fatal("Move did not enforce exact ownership and key uniqueness")
	}
	if value, found := registry.Get("renamed"); !found || value != &first {
		t.Fatalf("Get(renamed) = %p, %v", value, found)
	}
	if registry.Dematerialize() {
		t.Fatal("Dematerialize severed occupied backing")
	}
	if registry.RemoveExact("renamed", &second) ||
		!registry.RemoveExact("renamed", &first) {
		t.Fatal("RemoveExact did not enforce ownership")
	}

	key, value, occupied := registry.Entry(1)
	if !occupied || key != "second" || value != &second {
		t.Fatalf("Entry(1) = %q, %p, %v", key, value, occupied)
	}
	if !registry.RemoveAt(1) || registry.RemoveAt(1) {
		t.Fatal("RemoveAt did not clear exactly one occupied slot")
	}
	if registry.Count() != 0 || !registry.Dematerialize() || registry.IsMaterialized() {
		t.Fatal("empty arena backing was not severed")
	}
	if registry.Put("absent", &first) || registry.Dematerialize() {
		t.Fatal("unmaterialized arena accepted an operation")
	}
	if !registry.Materialize() {
		t.Fatal("rematerialization failed")
	}
	if _, found := registry.Get("second"); found {
		t.Fatal("rematerialization retained a severed reference")
	}
}

func TestRegistryRejectsChargeOverflow(t *testing.T) {
	if _, valid := BackingChargeBytes[*int](0); valid {
		t.Fatal("zero capacity produced a charge")
	}
	if strconv.IntSize == 64 {
		if _, valid := BackingChargeBytes[*int](math.MaxInt); valid {
			t.Fatal("overflowing backing charge was accepted")
		}
		if _, valid := New[*int](math.MaxInt); valid {
			t.Fatal("New accepted an overflowing arena shape")
		}
	}
}

func TestNilRegistryOperationsFailClosed(t *testing.T) {
	var registry *Registry[*int]
	value := 1
	if registry.Capacity() != 0 || registry.Count() != 0 || registry.IsMaterialized() ||
		registry.Materialize() || registry.Dematerialize() ||
		registry.Put("key", &value) || registry.ContainsExact("key", &value) ||
		registry.Move("key", "other", &value) ||
		registry.RemoveExact("key", &value) || registry.RemoveAt(0) {
		t.Fatal("nil registry operation succeeded")
	}
	if got, found := registry.Get("key"); found || got != nil {
		t.Fatalf("nil Get = %p, %v", got, found)
	}
	if key, got, found := registry.Entry(0); found || key != "" || got != nil {
		t.Fatalf("nil Entry = %q, %p, %v", key, got, found)
	}
}
