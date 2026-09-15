package crdt

import (
	"math/rand"
	"os"
	"strconv"
	"testing"
	"time"
)

type faultMode string

const (
	faultReorder   faultMode = "reorder"
	faultRedeliver faultMode = "drop-redeliver"
	faultDuplicate faultMode = "duplicate"
	faultPartition faultMode = "partition-heal"
)

func TestProperty_NReplicasConverge(t *testing.T) {
	iterations := propertyIterations(t)
	seed := propertySeed()
	random := rand.New(rand.NewSource(seed))
	t.Logf("property seed=%d iterations=%d", seed, iterations)

	for iteration := 0; iteration < iterations; iteration++ {
		scenarioSeed := random.Int63()
		scenarioRandom := rand.New(rand.NewSource(scenarioSeed))
		replicaCount := 2 + scenarioRandom.Intn(4)
		opCount := 10 + scenarioRandom.Intn(191)
		mode := []faultMode{faultReorder, faultRedeliver, faultDuplicate, faultPartition}[scenarioRandom.Intn(4)]
		network := newSimNetwork(replicaCount, scenarioSeed)
		generator := newOperationGenerator(replicaCount)

		if err := runScenario(network, generator, scenarioRandom, opCount, mode); err != nil {
			t.Fatalf("iteration=%d mode=%s seed=%d scenarioSeed=%d: %v\nreplay:\n%s", iteration, mode, seed, scenarioSeed, err, network.replay())
		}
		if err := network.assertConverged(); err != nil {
			t.Fatalf("iteration=%d mode=%s seed=%d scenarioSeed=%d: %v\nreplay:\n%s", iteration, mode, seed, scenarioSeed, err, network.replay())
		}
	}
}

func runScenario(network *simNetwork, generator *operationGenerator, random *rand.Rand, operationCount int, mode faultMode) error {
	partitionAt := operationCount / 2
	if mode == faultPartition {
		members := []int{random.Intn(len(network.replicas))}
		for len(members) == 1 && len(network.replicas) > 2 {
			candidate := random.Intn(len(network.replicas))
			if candidate != members[0] {
				members = append(members, candidate)
			}
		}
		network.partition(members...)
	}

	for index := 0; index < operationCount; index++ {
		if mode == faultPartition && index == partitionAt {
			network.heal()
			if err := network.flush(random); err != nil {
				return err
			}
		}

		from := random.Intn(len(network.replicas))
		operation := generator.next(random, from, network.replicas[from])
		if err := network.submit(from, operation); err != nil {
			return err
		}

		if mode != faultPartition && (index%8 == 7 || index == operationCount-1) {
			injectFault(network, random, mode)
			if err := network.flush(random); err != nil {
				return err
			}
		}
	}

	network.heal()
	if mode == faultPartition || len(network.pending) > 0 {
		injectFault(network, random, mode)
		return network.flush(random)
	}
	return nil
}

func injectFault(network *simNetwork, random *rand.Rand, mode faultMode) {
	if len(network.pending) == 0 {
		return
	}
	switch mode {
	case faultRedeliver:
		for count := 0; count < maxInt(1, len(network.pending)/12); count++ {
			network.dropAt(random.Intn(len(network.pending)), true)
		}
	case faultDuplicate:
		for count := 0; count < maxInt(1, len(network.pending)/12); count++ {
			network.duplicateAt(random.Intn(len(network.pending)))
		}
	}
}

func propertyIterations(t *testing.T) int {
	if value := os.Getenv("GOPAD_PROPERTY_ITERS"); value != "" {
		iterations, err := strconv.Atoi(value)
		if err == nil && iterations > 0 {
			return iterations
		}
		t.Fatalf("invalid GOPAD_PROPERTY_ITERS=%q", value)
	}
	if testing.Short() {
		return 50
	}
	return 500
}

func propertySeed() int64 {
	if value := os.Getenv("GOPAD_PROPERTY_SEED"); value != "" {
		if seed, err := strconv.ParseInt(value, 10, 64); err == nil {
			return seed
		}
	}
	return time.Now().UnixNano()
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
