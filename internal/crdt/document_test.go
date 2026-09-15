package crdt

import (
	"errors"
	"reflect"
	"testing"
)

func TestDocument_ConcurrentSiblingsConverge(t *testing.T) {
	a := Operation{Type: Insert, ID: CharID{SiteID: "a", Counter: 1}, Value: 'A'}
	b := Operation{Type: Insert, ID: CharID{SiteID: "b", Counter: 1}, Value: 'B'}

	first, second := New(), New()
	for _, op := range []Operation{a, b} {
		if err := first.Apply(op); err != nil {
			t.Fatal(err)
		}
	}
	for _, op := range []Operation{b, a} {
		if err := second.Apply(op); err != nil {
			t.Fatal(err)
		}
	}

	if first.Text() != second.Text() || first.Text() != "AB" {
		t.Fatalf("text mismatch: %q != %q", first.Text(), second.Text())
	}
}

func TestDocument_InsertAfterAndTombstoneDelete(t *testing.T) {
	document := New()
	a := CharID{SiteID: "site", Counter: 1}
	b := CharID{SiteID: "site", Counter: 2}
	c := CharID{SiteID: "site", Counter: 3}

	for _, op := range []Operation{
		{Type: Insert, ID: a, Value: 'A'},
		{Type: Insert, ID: b, Value: 'B', LeftID: &a},
		{Type: Insert, ID: c, Value: 'C', LeftID: &b},
	} {
		if err := document.Apply(op); err != nil {
			t.Fatal(err)
		}
	}
	if got := document.Text(); got != "ABC" {
		t.Fatalf("text before delete = %q, want %q", got, "ABC")
	}

	if err := document.Apply(Operation{Type: Delete, ID: b}); err != nil {
		t.Fatal(err)
	}
	if got := document.Text(); got != "AC" {
		t.Fatalf("text after delete = %q, want %q", got, "AC")
	}

	snapshot := document.Snapshot()
	if len(snapshot) != 3 || !snapshot[1].Deleted {
		t.Fatalf("snapshot = %#v, want three chars with a tombstone", snapshot)
	}
}

func TestDocument_CompactRemovesOnlyTombstones(t *testing.T) {
	document := New()
	liveID := CharID{SiteID: "site", Counter: 1}
	deletedID := CharID{SiteID: "site", Counter: 2}
	if err := document.Apply(Operation{Type: Insert, ID: liveID, Value: 'A'}); err != nil {
		t.Fatal(err)
	}
	if err := document.Apply(Operation{Type: Insert, ID: deletedID, Value: 'B', LeftID: &liveID}); err != nil {
		t.Fatal(err)
	}
	if err := document.Apply(Operation{Type: Delete, ID: deletedID}); err != nil {
		t.Fatal(err)
	}
	before := document.Text()
	purged, err := document.Compact([]CharID{liveID, deletedID, {SiteID: "missing", Counter: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if purged != 1 {
		t.Fatalf("purged = %d, want 1", purged)
	}
	if document.Text() != before {
		t.Fatalf("text after compaction = %q, want %q", document.Text(), before)
	}
	if len(document.Snapshot()) != 1 {
		t.Fatalf("snapshot length = %d, want one live character", len(document.Snapshot()))
	}
	if err := document.Apply(Operation{Type: Insert, ID: CharID{SiteID: "late", Counter: 1}, Value: 'C', LeftID: &deletedID}); !errors.Is(err, ErrMissingParent) {
		t.Fatalf("insert after purged anchor error = %v, want missing parent", err)
	}
}

func TestDocument_RightAnchorPreservesMiddleInsertion(t *testing.T) {
	document := New()
	a := CharID{SiteID: "base", Counter: 1}
	b := CharID{SiteID: "base", Counter: 2}
	inserted := CharID{SiteID: "editor", Counter: 10}

	for _, op := range []Operation{
		{Type: Insert, ID: a, Value: 'A'},
		{Type: Insert, ID: b, Value: 'B', LeftID: &a},
		{Type: Insert, ID: inserted, Value: 'X', LeftID: &a, RightID: &b},
	} {
		if err := document.Apply(op); err != nil {
			t.Fatal(err)
		}
	}

	if got := document.Text(); got != "AXB" {
		t.Fatalf("text = %q, want %q", got, "AXB")
	}
	snapshot := document.Snapshot()
	if snapshot[2].RightID != nil {
		t.Fatalf("right anchor leaked to the final character: %#v", snapshot[2].RightID)
	}
}

func TestDocument_RightAnchorsConvergeRegardlessOfArrivalOrder(t *testing.T) {
	a := CharID{SiteID: "base", Counter: 1}
	b := CharID{SiteID: "base", Counter: 2}
	first := Operation{Type: Insert, ID: CharID{SiteID: "z", Counter: 10}, Value: 'X', LeftID: &a, RightID: &b}
	second := Operation{Type: Insert, ID: CharID{SiteID: "a", Counter: 10}, Value: 'Y', LeftID: &a, RightID: &b}

	left, right := New(), New()
	for _, op := range []Operation{
		{Type: Insert, ID: a, Value: 'A'},
		{Type: Insert, ID: b, Value: 'B', LeftID: &a},
		first,
		second,
	} {
		if err := left.Apply(op); err != nil {
			t.Fatal(err)
		}
	}
	for _, op := range []Operation{
		{Type: Insert, ID: a, Value: 'A'},
		{Type: Insert, ID: b, Value: 'B', LeftID: &a},
		second,
		first,
	} {
		if err := right.Apply(op); err != nil {
			t.Fatal(err)
		}
	}

	if left.Text() != "AYXB" || right.Text() != left.Text() {
		t.Fatalf("texts = %q/%q, want %q", left.Text(), right.Text(), "AYXB")
	}
	if !reflect.DeepEqual(left.Snapshot(), right.Snapshot()) {
		t.Fatalf("snapshots diverged: %#v != %#v", left.Snapshot(), right.Snapshot())
	}
}

func TestDocument_DuplicateIDsAreIdempotentButConflictsFail(t *testing.T) {
	document := New()
	op := Operation{Type: Insert, ID: CharID{SiteID: "site", Counter: 1}, Value: 'A'}

	if err := document.Apply(op); err != nil {
		t.Fatal(err)
	}
	if err := document.Apply(op); err != nil {
		t.Fatalf("duplicate operation should be idempotent: %v", err)
	}
	if got := len(document.Snapshot()); got != 1 {
		t.Fatalf("snapshot length = %d, want 1", got)
	}

	err := document.Apply(Operation{Type: Insert, ID: op.ID, Value: 'B'})
	if !errors.Is(err, ErrConflictingDuplicate) {
		t.Fatalf("error = %v, want conflicting duplicate", err)
	}
	var applyErr *ApplyError
	if !errors.As(err, &applyErr) {
		t.Fatalf("error %T, want *ApplyError", err)
	}
}

func TestDocument_MissingParentIsTypedError(t *testing.T) {
	document := New()
	parent := CharID{SiteID: "parent", Counter: 1}
	child := Operation{Type: Insert, ID: CharID{SiteID: "child", Counter: 1}, Value: 'C', LeftID: &parent}

	err := document.Apply(child)
	if !errors.Is(err, ErrMissingParent) {
		t.Fatalf("error = %v, want missing parent", err)
	}
	var applyErr *ApplyError
	if !errors.As(err, &applyErr) || applyErr.Parent == nil || *applyErr.Parent != parent {
		t.Fatalf("error = %#v, want typed parent details", err)
	}
	if got := document.Snapshot(); len(got) != 0 {
		t.Fatalf("snapshot after rejected operation = %#v, want empty", got)
	}

	if err := document.Apply(Operation{Type: Insert, ID: parent, Value: 'P'}); err != nil {
		t.Fatal(err)
	}
	if err := document.Apply(child); err != nil {
		t.Fatal(err)
	}
	if got := document.Text(); got != "PC" {
		t.Fatalf("text = %q, want %q", got, "PC")
	}
}

func TestDocument_UnknownDeleteFails(t *testing.T) {
	err := New().Apply(Operation{Type: Delete, ID: CharID{SiteID: "missing", Counter: 1}})
	if !errors.Is(err, ErrUnknownCharacter) {
		t.Fatalf("error = %v, want unknown character", err)
	}
}

func TestDocument_UnicodeRunesAndSnapshotCopies(t *testing.T) {
	document := New()
	first := CharID{SiteID: "site", Counter: 1}
	second := CharID{SiteID: "site", Counter: 2}
	if err := document.Apply(Operation{Type: Insert, ID: first, Value: '你'}); err != nil {
		t.Fatal(err)
	}
	if err := document.Apply(Operation{Type: Insert, ID: second, Value: 'é', LeftID: &first}); err != nil {
		t.Fatal(err)
	}
	if got := document.Text(); got != "你é" {
		t.Fatalf("text = %q, want %q", got, "你é")
	}

	snapshot := document.Snapshot()
	snapshot[0].Value = 'X'
	snapshot[0].LeftID = &CharID{SiteID: "mutated", Counter: 99}
	if got := document.Text(); got != "你é" {
		t.Fatalf("mutating snapshot changed document text to %q", got)
	}
	if !reflect.DeepEqual(document.Snapshot()[0].LeftID, (*CharID)(nil)) {
		t.Fatalf("root left ID was mutated: %#v", document.Snapshot()[0].LeftID)
	}
}
