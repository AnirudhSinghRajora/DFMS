package replication_test

import (
	"fmt"
	"math"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/AnirudhSinghRajora/DFMS/internal/replication"
)

func healthyNodes(count int) []replication.Node {
	nodes := make([]replication.Node, count)
	for i := 0; i < count; i++ {
		nodes[i] = replication.Node{
			ID:       fmt.Sprintf("node-%d", i),
			Name:     fmt.Sprintf("minio-%d", i),
			Endpoint: fmt.Sprintf("minio-%d:9000", i),
			Weight:   100,
			Status:   "healthy",
		}
	}
	return nodes
}

func TestGetNodes_ReturnsCorrectCount(t *testing.T) {
	ring := replication.NewRing(healthyNodes(5), 150)

	nodes := ring.GetNodes("test-key", 3)
	assert.Len(t, nodes, 3)

	// All nodes should be distinct
	seen := make(map[string]bool)
	for _, n := range nodes {
		assert.False(t, seen[n.ID], "Duplicate node returned")
		seen[n.ID] = true
	}
}

func TestGetNodes_SkipsUnhealthyNodes(t *testing.T) {
	nodes := healthyNodes(3)
	ring := replication.NewRing(nodes, 150)

	// Mark one node as unhealthy
	ring.UpdateNodeStatus("node-1", "unhealthy")

	result := ring.GetNodes("any-key", 3)

	// Should only get 2 healthy nodes
	assert.Len(t, result, 2)
	for _, n := range result {
		assert.Equal(t, "healthy", n.Status)
		assert.NotEqual(t, "node-1", n.ID)
	}
}

func TestGetNodes_EmptyRing(t *testing.T) {
	ring := replication.NewRing(nil, 150)
	assert.Nil(t, ring.GetNodes("key", 3))
}

func TestGetNodes_MoreRequestedThanExist(t *testing.T) {
	ring := replication.NewRing(healthyNodes(2), 150)

	nodes := ring.GetNodes("key", 5)
	assert.Len(t, nodes, 2) // Can't return more than exist
}

func TestGetNodes_SingleNode(t *testing.T) {
	ring := replication.NewRing(healthyNodes(1), 150)

	nodes := ring.GetNodes("key", 3)
	assert.Len(t, nodes, 1)
	assert.Equal(t, "node-0", nodes[0].ID)
}

func TestDistribution_Uniformity(t *testing.T) {
	const numNodes = 4
	const numKeys = 10000
	const tolerance = 0.35 // No node should get > 35% of keys

	ring := replication.NewRing(healthyNodes(numNodes), 150)

	distribution := make(map[string]int)
	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("chunk-hash-%d", i)
		nodes := ring.GetNodes(key, 1)
		require.Len(t, nodes, 1)
		distribution[nodes[0].ID]++
	}

	expectedPerNode := float64(numKeys) / float64(numNodes)
	for nodeID, count := range distribution {
		ratio := float64(count) / float64(numKeys)
		t.Logf("Node %s: %d keys (%.1f%%)", nodeID, count, ratio*100)
		assert.Less(t, ratio, tolerance,
			"Node %s has too many keys (%.1f%%, max %.0f%%)", nodeID, ratio*100, tolerance*100)
		assert.Greater(t, float64(count), expectedPerNode*0.5,
			"Node %s has too few keys (expected ~%.0f, got %d)", nodeID, expectedPerNode, count)
	}
}

func TestAddNode_MinimalRedistribution(t *testing.T) {
	const numKeys = 10000

	// Start with 3 nodes
	ring := replication.NewRing(healthyNodes(3), 150)

	// Record initial assignments
	initial := make(map[string]string) // key → nodeID
	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("key-%d", i)
		nodes := ring.GetNodes(key, 1)
		initial[key] = nodes[0].ID
	}

	// Add a 4th node
	ring.AddNode(replication.Node{
		ID:       "node-3",
		Name:     "minio-3",
		Endpoint: "minio-3:9000",
		Weight:   100,
		Status:   "healthy",
	})

	// Count how many keys changed owners
	changed := 0
	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("key-%d", i)
		nodes := ring.GetNodes(key, 1)
		if nodes[0].ID != initial[key] {
			changed++
		}
	}

	redistPct := float64(changed) / float64(numKeys)
	t.Logf("Redistribution: %d/%d keys changed (%.1f%%)", changed, numKeys, redistPct*100)

	// With consistent hashing, adding 1 of 4 nodes should move ~25% of keys.
	// We allow up to 40% to account for virtual node variance.
	assert.Less(t, redistPct, 0.40,
		"Too much redistribution: %.1f%% keys moved (expected < 40%%)", redistPct*100)
}

func TestRemoveNode(t *testing.T) {
	ring := replication.NewRing(healthyNodes(4), 150)

	assert.Equal(t, 4, ring.NodeCount())

	ring.RemoveNode("node-2")
	assert.Equal(t, 3, ring.NodeCount())

	// Keys that were on node-2 should now go to other nodes
	for i := 0; i < 100; i++ {
		nodes := ring.GetNodes(fmt.Sprintf("key-%d", i), 1)
		require.Len(t, nodes, 1)
		assert.NotEqual(t, "node-2", nodes[0].ID)
	}
}

func TestWeightedNodes(t *testing.T) {
	const numKeys = 10000

	nodes := []replication.Node{
		{ID: "heavy", Name: "heavy", Endpoint: "heavy:9000", Weight: 200, Status: "healthy"},
		{ID: "normal", Name: "normal", Endpoint: "normal:9000", Weight: 100, Status: "healthy"},
	}

	ring := replication.NewRing(nodes, 150)

	distribution := make(map[string]int)
	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("weighted-key-%d", i)
		result := ring.GetNodes(key, 1)
		distribution[result[0].ID]++
	}

	heavyPct := float64(distribution["heavy"]) / float64(numKeys)
	normalPct := float64(distribution["normal"]) / float64(numKeys)

	t.Logf("Heavy: %.1f%%, Normal: %.1f%%", heavyPct*100, normalPct*100)

	// Heavy node (200 weight) should get approximately 2x more keys
	ratio := heavyPct / normalPct
	assert.Greater(t, ratio, 1.5, "Heavy node should get significantly more keys")
	assert.Less(t, ratio, 3.0, "Ratio should be approximately 2:1")
}

func TestConcurrentAccess(t *testing.T) {
	ring := replication.NewRing(healthyNodes(5), 150)

	var wg sync.WaitGroup
	const goroutines = 100

	// Mix of reads and writes
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				key := fmt.Sprintf("concurrent-key-%d-%d", id, j)
				nodes := ring.GetNodes(key, 3)
				_ = nodes
			}
		}(i)
	}

	// Concurrent writes
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			ring.AddNode(replication.Node{
				ID:       fmt.Sprintf("dynamic-%d", i),
				Name:     fmt.Sprintf("dynamic-%d", i),
				Endpoint: fmt.Sprintf("dynamic-%d:9000", i),
				Weight:   100,
				Status:   "healthy",
			})
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 5; i++ {
			ring.UpdateNodeStatus(fmt.Sprintf("node-%d", i), "healthy")
		}
	}()

	wg.Wait() // If there's a race condition, -race will catch it
}

func TestUpdateNodeStatus(t *testing.T) {
	ring := replication.NewRing(healthyNodes(3), 150)

	// All healthy → should get 3 nodes
	nodes := ring.GetNodes("key", 3)
	assert.Len(t, nodes, 3)

	// Mark all unhealthy
	ring.UpdateNodeStatus("node-0", "unhealthy")
	ring.UpdateNodeStatus("node-1", "unhealthy")
	ring.UpdateNodeStatus("node-2", "unhealthy")

	nodes = ring.GetNodes("key", 3)
	assert.Empty(t, nodes)

	// Recover one
	ring.UpdateNodeStatus("node-1", "healthy")
	nodes = ring.GetNodes("key", 3)
	assert.Len(t, nodes, 1)
	assert.Equal(t, "node-1", nodes[0].ID)
}

func TestGetAllNodes(t *testing.T) {
	ring := replication.NewRing(healthyNodes(3), 150)

	all := ring.GetAllNodes()
	assert.Len(t, all, 3)
}

func TestNodeCount(t *testing.T) {
	ring := replication.NewRing(healthyNodes(5), 150)
	assert.Equal(t, 5, ring.NodeCount())

	ring.RemoveNode("node-0")
	assert.Equal(t, 4, ring.NodeCount())
}

func TestConsistentMapping(t *testing.T) {
	// Same key should always map to the same node
	ring := replication.NewRing(healthyNodes(5), 150)

	key := "consistent-key"
	nodes1 := ring.GetNodes(key, 1)
	nodes2 := ring.GetNodes(key, 1)

	require.Len(t, nodes1, 1)
	require.Len(t, nodes2, 1)
	assert.Equal(t, nodes1[0].ID, nodes2[0].ID)
}

func TestDefaultWeight(t *testing.T) {
	// Nodes with weight 0 should be treated as weight 100
	nodes := []replication.Node{
		{ID: "zero-weight", Name: "zw", Endpoint: "zw:9000", Weight: 0, Status: "healthy"},
		{ID: "default", Name: "d", Endpoint: "d:9000", Weight: 100, Status: "healthy"},
	}

	ring := replication.NewRing(nodes, 150)

	// Both should participate — distribution shouldn't be extreme
	distribution := make(map[string]int)
	for i := 0; i < 1000; i++ {
		result := ring.GetNodes(fmt.Sprintf("k%d", i), 1)
		distribution[result[0].ID]++
	}

	assert.Greater(t, distribution["zero-weight"], 0, "Zero-weight node should still get keys")

	// Both should get roughly equal distribution (both effectively weight 100)
	diff := math.Abs(float64(distribution["zero-weight"]-distribution["default"])) / 1000.0
	assert.Less(t, diff, 0.15, "Weight-0 and weight-100 should distribute similarly")
}
