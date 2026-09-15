package crdt

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
)

type simDelivery struct {
	from    int
	to      int
	op      Operation
	ordinal uint64
}

// simNetwork is a deterministic in-memory transport for CRDT operations. It
// keeps dependency-blocked messages queued so the test can exercise arbitrary
// network order without turning a valid operation into a malformed one.
type simNetwork struct {
	replicas    []*Document
	pending     []simDelivery
	delivered   []simDelivery
	partitioned map[int]bool
	seed        int64
	nextOrdinal uint64
}

func newSimNetwork(replicaCount int, seed int64) *simNetwork {
	if replicaCount < 1 {
		replicaCount = 1
	}
	replicas := make([]*Document, replicaCount)
	for index := range replicas {
		replicas[index] = New()
	}
	return &simNetwork{
		replicas:    replicas,
		partitioned: make(map[int]bool),
		seed:        seed,
	}
}

func (n *simNetwork) submit(from int, operation Operation) error {
	if from < 0 || from >= len(n.replicas) {
		return fmt.Errorf("invalid source replica %d", from)
	}
	if err := n.replicas[from].Apply(operation); err != nil {
		return fmt.Errorf("apply operation at source replica %d: %w", from, err)
	}
	for to := range n.replicas {
		if to == from {
			continue
		}
		n.enqueue(from, to, operation)
	}
	return nil
}

func (n *simNetwork) enqueue(from, to int, operation Operation) {
	n.nextOrdinal++
	n.pending = append(n.pending, simDelivery{
		from:    from,
		to:      to,
		op:      operation,
		ordinal: n.nextOrdinal,
	})
}

func (n *simNetwork) partition(replicas ...int) {
	n.partitioned = make(map[int]bool, len(replicas))
	for _, replica := range replicas {
		if replica >= 0 && replica < len(n.replicas) {
			n.partitioned[replica] = true
		}
	}
}

func (n *simNetwork) heal() {
	n.partitioned = make(map[int]bool)
}

func (n *simNetwork) crossesPartition(delivery simDelivery) bool {
	return n.partitioned[delivery.from] != n.partitioned[delivery.to]
}

func (n *simNetwork) dropAt(index int, redeliver bool) {
	if index < 0 || index >= len(n.pending) {
		return
	}
	delivery := n.pending[index]
	n.pending = append(n.pending[:index], n.pending[index+1:]...)
	if redeliver {
		n.pending = append(n.pending, delivery)
	}
}

func (n *simNetwork) duplicateAt(index int) {
	if index < 0 || index >= len(n.pending) {
		return
	}
	delivery := n.pending[index]
	n.nextOrdinal++
	delivery.ordinal = n.nextOrdinal
	n.pending = append(n.pending, delivery)
}

func isDeferredApplyError(err error) bool {
	return errors.Is(err, ErrMissingParent) || errors.Is(err, ErrUnknownCharacter)
}

// flush delivers pending messages in randomized passes. A message whose
// dependency is still in flight is retained for a later pass, mirroring a
// transport retry rather than silently losing valid user data.
func (n *simNetwork) flush(random *rand.Rand) error {
	for len(n.pending) > 0 {
		random.Shuffle(len(n.pending), func(i, j int) {
			n.pending[i], n.pending[j] = n.pending[j], n.pending[i]
		})

		progressed := false
		for index := 0; index < len(n.pending); {
			delivery := n.pending[index]
			if n.crossesPartition(delivery) {
				index++
				continue
			}

			err := n.replicas[delivery.to].Apply(delivery.op)
			if isDeferredApplyError(err) {
				index++
				continue
			}
			if err != nil {
				return fmt.Errorf("deliver %s from replica %d to %d: %w", formatOperation(delivery.op), delivery.from, delivery.to, err)
			}

			n.delivered = append(n.delivered, delivery)
			n.pending = append(n.pending[:index], n.pending[index+1:]...)
			progressed = true
		}

		if !progressed {
			return fmt.Errorf("network made no progress with %d pending deliveries", len(n.pending))
		}
	}
	return nil
}

func (n *simNetwork) replay() string {
	if len(n.delivered) == 0 {
		return fmt.Sprintf("seed=%d\n<empty>", n.seed)
	}
	entries := make([]string, 0, len(n.delivered))
	for index, delivery := range n.delivered {
		entries = append(entries, fmt.Sprintf("%d:%d->%d %s", index, delivery.from, delivery.to, formatOperation(delivery.op)))
	}
	return fmt.Sprintf("seed=%d\n%s", n.seed, strings.Join(entries, "\n"))
}

func (n *simNetwork) assertConverged() error {
	if len(n.pending) != 0 {
		return fmt.Errorf("%d deliveries remain pending", len(n.pending))
	}
	if len(n.replicas) < 2 {
		return nil
	}
	wantText := n.replicas[0].Text()
	wantSnapshot := n.replicas[0].Snapshot()
	for index, replica := range n.replicas[1:] {
		if got := replica.Text(); got != wantText {
			return fmt.Errorf("replica %d text = %q, want %q", index+1, got, wantText)
		}
		if got := replica.Snapshot(); !sameSnapshot(got, wantSnapshot) {
			return fmt.Errorf("replica %d snapshot diverged", index+1)
		}
	}
	return nil
}

func sameSnapshot(left, right []Char) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].ID != right[index].ID || left[index].Value != right[index].Value || left[index].Deleted != right[index].Deleted || !sameCharID(left[index].LeftID, right[index].LeftID) {
			return false
		}
	}
	return true
}

type operationGenerator struct {
	counters   []uint64
	hotParents []CharID
	valueRunes []rune
}

func newOperationGenerator(replicaCount int) *operationGenerator {
	return &operationGenerator{
		counters:   make([]uint64, replicaCount),
		valueRunes: []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 سلام世界"),
	}
}

func (g *operationGenerator) next(random *rand.Rand, replica int, document *Document) Operation {
	snapshot := document.Snapshot()
	visible := make([]CharID, 0, len(snapshot))
	for _, char := range snapshot {
		if !char.Deleted {
			visible = append(visible, char.ID)
		}
	}

	if len(snapshot) > 0 && len(visible) > 0 && random.Intn(100) < 35 {
		candidates := visible
		if random.Intn(100) < 25 {
			candidates = make([]CharID, 0, len(snapshot))
			for _, char := range snapshot {
				candidates = append(candidates, char.ID)
			}
		}
		return Operation{Type: Delete, ID: candidates[random.Intn(len(candidates))]}
	}

	g.counters[replica]++
	operation := Operation{
		Type:  Insert,
		ID:    CharID{SiteID: fmt.Sprintf("site-%d", replica), Counter: g.counters[replica]},
		Value: g.valueRunes[random.Intn(len(g.valueRunes))],
	}
	if len(snapshot) > 0 && random.Intn(100) < 80 {
		if len(g.hotParents) > 0 && random.Intn(100) < 65 {
			for attempt := 0; attempt < len(g.hotParents); attempt++ {
				parent := g.hotParents[random.Intn(len(g.hotParents))]
				if containsChar(snapshot, parent) {
					operation.LeftID = &parent
					break
				}
			}
		}
		if operation.LeftID == nil {
			parent := snapshot[random.Intn(len(snapshot))].ID
			operation.LeftID = &parent
		}
	}
	if operation.LeftID != nil {
		g.hotParents = append(g.hotParents, *operation.LeftID)
		if len(g.hotParents) > 32 {
			g.hotParents = g.hotParents[len(g.hotParents)-32:]
		}
	}
	return operation
}

func containsChar(snapshot []Char, id CharID) bool {
	for _, char := range snapshot {
		if char.ID == id {
			return true
		}
	}
	return false
}

func formatOperation(operation Operation) string {
	left := "root"
	if operation.LeftID != nil {
		left = fmt.Sprintf("%s/%d", operation.LeftID.SiteID, operation.LeftID.Counter)
	}
	return fmt.Sprintf("%s %s/%d value=%U left=%s", operation.Type, operation.ID.SiteID, operation.ID.Counter, operation.Value, left)
}

func applyWithDependencyRetry(document *Document, operations []Operation) error {
	pending := append([]Operation(nil), operations...)
	for len(pending) > 0 {
		progressed := false
		for index := 0; index < len(pending); {
			err := document.Apply(pending[index])
			if isDeferredApplyError(err) {
				index++
				continue
			}
			if err != nil {
				return err
			}
			pending = append(pending[:index], pending[index+1:]...)
			progressed = true
		}
		if !progressed {
			return fmt.Errorf("could not resolve %d operation dependencies", len(pending))
		}
	}
	return nil
}
