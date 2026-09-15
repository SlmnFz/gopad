package crdt

import (
	"container/heap"
	"errors"
	"fmt"
	"sort"
)

// OperationType identifies the mutation represented by an Operation.
type OperationType string

const (
	Insert OperationType = "insert"
	Delete OperationType = "delete"
)

// CharID is the stable identity of one character in a document replica.
type CharID struct {
	SiteID  string `json:"siteID"`
	Counter uint64 `json:"counter"`
}

// Char is one character in the CRDT structure. Deleted characters remain in
// the structure as tombstones so later operations can still reference them.
type Char struct {
	ID      CharID  `json:"id"`
	Value   rune    `json:"value"`
	LeftID  *CharID `json:"leftID,omitempty"`
	RightID *CharID `json:"rightID,omitempty"`
	Deleted bool    `json:"deleted"`
}

// Operation is a CRDT mutation received from a replica.
type Operation struct {
	Type    OperationType `json:"type"`
	ID      CharID        `json:"id"`
	Value   rune          `json:"value,omitempty"`
	LeftID  *CharID       `json:"leftID,omitempty"`
	RightID *CharID       `json:"rightID,omitempty"`
}

var (
	ErrInvalidOperation     = errors.New("invalid operation")
	ErrMissingParent        = errors.New("missing parent")
	ErrUnknownCharacter     = errors.New("unknown character")
	ErrConflictingDuplicate = errors.New("conflicting duplicate character")
)

// ApplyError is a typed error returned when an operation cannot be applied.
// Callers can use errors.Is with one of the exported sentinel errors above.
type ApplyError struct {
	Cause  error
	ID     CharID
	Parent *CharID
}

func (e *ApplyError) Error() string {
	switch {
	case e.Parent != nil:
		return fmt.Sprintf("%v: character %s/%d references missing parent %s/%d", e.Cause, e.ID.SiteID, e.ID.Counter, e.Parent.SiteID, e.Parent.Counter)
	default:
		return fmt.Sprintf("%v: character %s/%d", e.Cause, e.ID.SiteID, e.ID.Counter)
	}
}

func (e *ApplyError) Unwrap() error {
	return e.Cause
}

// Document is an in-memory RGA-style sequence CRDT.
//
// Document is intentionally not internally synchronized. A room owner should
// serialize access to it, which keeps mutation and traversal predictable.
type Document struct {
	chars map[CharID]Char
}

// New returns an empty CRDT document.
func New() *Document {
	return &Document{chars: make(map[CharID]Char)}
}

// Apply applies an operation once. Reapplying the same operation is
// idempotent; reusing an ID with different content is rejected.
func (d *Document) Apply(op Operation) error {
	if d == nil {
		return &ApplyError{Cause: ErrInvalidOperation, ID: op.ID}
	}
	if op.ID.SiteID == "" || op.ID.Counter == 0 {
		return &ApplyError{Cause: ErrInvalidOperation, ID: op.ID}
	}

	switch op.Type {
	case Insert:
		return d.applyInsert(op)
	case Delete:
		return d.applyDelete(op.ID)
	default:
		return &ApplyError{Cause: ErrInvalidOperation, ID: op.ID}
	}
}

func (d *Document) applyInsert(op Operation) error {
	if existing, ok := d.chars[op.ID]; ok {
		if existing.Value == op.Value && sameCharID(existing.LeftID, op.LeftID) && sameCharID(existing.RightID, op.RightID) {
			return nil
		}
		return &ApplyError{Cause: ErrConflictingDuplicate, ID: op.ID}
	}
	if op.LeftID != nil {
		if op.LeftID.SiteID == "" || op.LeftID.Counter == 0 || op.ID == *op.LeftID {
			return &ApplyError{Cause: ErrInvalidOperation, ID: op.ID, Parent: cloneCharID(op.LeftID)}
		}
		if _, ok := d.chars[*op.LeftID]; !ok {
			return &ApplyError{Cause: ErrMissingParent, ID: op.ID, Parent: cloneCharID(op.LeftID)}
		}
	}
	if op.RightID != nil {
		if op.RightID.SiteID == "" || op.RightID.Counter == 0 || op.ID == *op.RightID || sameCharID(op.LeftID, op.RightID) {
			return &ApplyError{Cause: ErrInvalidOperation, ID: op.ID, Parent: cloneCharID(op.RightID)}
		}
	}

	d.chars[op.ID] = Char{
		ID:      op.ID,
		Value:   op.Value,
		LeftID:  cloneCharID(op.LeftID),
		RightID: cloneCharID(op.RightID),
		Deleted: false,
	}
	return nil
}

func (d *Document) applyDelete(id CharID) error {
	char, ok := d.chars[id]
	if !ok {
		return &ApplyError{Cause: ErrUnknownCharacter, ID: id}
	}
	if char.Deleted {
		return nil
	}
	char.Deleted = true
	d.chars[id] = char
	return nil
}

// Compact permanently removes the supplied tombstones from the CRDT
// structure. Live and unknown IDs are ignored so a stale compaction request
// cannot affect the document or fail a whole batch.
//
// Callers must only pass tombstones that have been present in a previously
// successful snapshot. This heuristic safety window leaves one complete
// snapshot interval for in-flight operations to arrive; it is a practical
// latency margin, not a causal-stability proof.
func (d *Document) Compact(ids []CharID) (purged int, err error) {
	if d == nil {
		return 0, ErrInvalidOperation
	}
	for _, id := range ids {
		char, ok := d.chars[id]
		if !ok || !char.Deleted {
			continue
		}
		delete(d.chars, id)
		purged++
	}
	return purged, nil
}

// Text returns the visible document text, excluding tombstones.
func (d *Document) Text() string {
	var text []rune
	for _, char := range d.Snapshot() {
		if !char.Deleted {
			text = append(text, char.Value)
		}
	}
	return string(text)
}

// Snapshot returns the complete ordered CRDT structure, including tombstones.
// Returned values are copies and can be safely modified by the caller.
func (d *Document) Snapshot() []Char {
	if d == nil || len(d.chars) == 0 {
		return nil
	}

	children := make(map[CharID][]Char)
	var root CharID
	for _, char := range d.chars {
		parent := root
		if char.LeftID != nil {
			parent = *char.LeftID
		}
		children[parent] = append(children[parent], cloneChar(char))
	}
	for parent := range children {
		sort.Slice(children[parent], func(i, j int) bool {
			return lessCharID(children[parent][i].ID, children[parent][j].ID)
		})
	}

	base := make([]Char, 0, len(d.chars))
	var visit func(CharID)
	visit = func(parent CharID) {
		for _, char := range children[parent] {
			base = append(base, char)
			visit(char.ID)
		}
	}
	visit(root)

	baseIndex := make(map[CharID]int, len(base))
	indegree := make(map[CharID]int, len(base))
	edges := make(map[CharID][]CharID, len(base))
	for index, char := range base {
		baseIndex[char.ID] = index
		indegree[char.ID] = 0
		edges[char.ID] = nil
	}
	addEdge := func(from, to CharID) {
		if from == to {
			return
		}
		if _, ok := indegree[from]; !ok {
			return
		}
		if _, ok := indegree[to]; !ok {
			return
		}
		for _, existing := range edges[from] {
			if existing == to {
				return
			}
		}
		edges[from] = append(edges[from], to)
		indegree[to]++
	}
	for _, char := range base {
		if char.LeftID != nil {
			addEdge(*char.LeftID, char.ID)
		}
		if char.RightID != nil {
			addEdge(char.ID, *char.RightID)
		}
	}

	ready := &charIDHeap{items: make([]CharID, 0, len(base)), ranks: baseIndex}
	heap.Init(ready)
	for _, char := range base {
		if indegree[char.ID] == 0 {
			heap.Push(ready, char.ID)
		}
	}
	ordered := make([]Char, 0, len(base))
	seen := make(map[CharID]struct{}, len(base))
	for ready.Len() > 0 {
		id := heap.Pop(ready).(CharID)
		seen[id] = struct{}{}
		ordered = append(ordered, cloneChar(d.chars[id]))
		for _, child := range edges[id] {
			indegree[child]--
			if indegree[child] == 0 {
				heap.Push(ready, child)
			}
		}
	}

	// A malformed or mutually contradictory pair of right anchors should not
	// make the document disappear. Keep the deterministic legacy order for any
	// cycle members; valid operations never take this path.
	if len(ordered) != len(base) {
		for _, char := range base {
			if _, ok := seen[char.ID]; ok {
				continue
			}
			ordered = append(ordered, cloneChar(char))
		}
	}
	return ordered
}

func lessCharID(left, right CharID) bool {
	if left.Counter != right.Counter {
		return left.Counter < right.Counter
	}
	return left.SiteID < right.SiteID
}

func sameCharID(left, right *CharID) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func cloneCharID(id *CharID) *CharID {
	if id == nil {
		return nil
	}
	copy := *id
	return &copy
}

func cloneChar(char Char) Char {
	char.LeftID = cloneCharID(char.LeftID)
	char.RightID = cloneCharID(char.RightID)
	return char
}

type charIDHeap struct {
	items []CharID
	ranks map[CharID]int
}

func (h charIDHeap) Len() int { return len(h.items) }

func (h charIDHeap) Less(i, j int) bool {
	return h.ranks[h.items[i]] < h.ranks[h.items[j]]
}

func (h charIDHeap) Swap(i, j int) {
	h.items[i], h.items[j] = h.items[j], h.items[i]
}

func (h *charIDHeap) Push(value any) {
	h.items = append(h.items, value.(CharID))
}

func (h *charIDHeap) Pop() any {
	last := len(h.items) - 1
	value := h.items[last]
	h.items = h.items[:last]
	return value
}
