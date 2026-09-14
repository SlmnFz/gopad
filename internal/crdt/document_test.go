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
