// Package replication provides consistent hashing and chunk replication
// for distributing data across storage nodes in the DFMS cluster.
package replication

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
	"sync"
)

// Node represents a storage node in the cluster.
type Node struct {
	ID       string
	Name     string
	Endpoint string
	Weight   int // Higher weight = more virtual nodes = more data
	Status   string
}

// virtualNode maps a point on the hash ring to a physical node.
type virtualNode struct {
	hash   uint32
	nodeID string
}

// Ring implements a consistent hash ring with virtual nodes.
// It distributes keys (chunk hashes) across physical nodes with minimal
// redistribution when nodes are added or removed.
//
// Virtual nodes improve distribution uniformity: each physical node
// gets `virtualNodesPerNode * (weight/100)` points on the ring.
type Ring struct {
	mu                  sync.RWMutex
	vnodes              []virtualNode   // Sorted by hash
	nodes               map[string]Node // nodeID → Node
	virtualNodesPerNode int
}

// NewRing creates a consistent hash ring from the given nodes.
// virtualNodesPerNode controls distribution granularity (150 is recommended).
func NewRing(nodes []Node, virtualNodesPerNode int) *Ring {
	r := &Ring{
		nodes:               make(map[string]Node),
		virtualNodesPerNode: virtualNodesPerNode,
	}

	for _, n := range nodes {
		r.nodes[n.ID] = n
	}

	r.rebuild()
	return r
}

// GetNodes returns up to `count` distinct physical nodes responsible for the
// given key. The first node is the "primary" and subsequent nodes are replicas.
//
// If fewer than `count` healthy nodes exist, it returns all available healthy nodes.
func (r *Ring) GetNodes(key string, count int) []Node {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.vnodes) == 0 {
		return nil
	}

	hash := hashKey(key)
	idx := r.search(hash)

	seen := make(map[string]bool)
	var result []Node

	for i := 0; i < len(r.vnodes) && len(result) < count; i++ {
		vn := r.vnodes[(idx+i)%len(r.vnodes)]
		if seen[vn.nodeID] {
			continue
		}
		node := r.nodes[vn.nodeID]
		if node.Status != "healthy" {
			continue // Skip unhealthy nodes
		}
		seen[vn.nodeID] = true
		result = append(result, node)
	}

	return result
}

// AddNode adds a node to the ring dynamically without service restart.
func (r *Ring) AddNode(node Node) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.nodes[node.ID] = node
	r.rebuild()
}

// RemoveNode removes a node from the ring.
func (r *Ring) RemoveNode(nodeID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.nodes, nodeID)
	r.rebuild()
}

// UpdateNodeStatus updates a node's health status in the ring.
func (r *Ring) UpdateNodeStatus(nodeID, status string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if node, ok := r.nodes[nodeID]; ok {
		node.Status = status
		r.nodes[nodeID] = node
	}
}

// NodeCount returns the number of physical nodes in the ring.
func (r *Ring) NodeCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.nodes)
}

// GetAllNodes returns all nodes in the ring.
func (r *Ring) GetAllNodes() []Node {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]Node, 0, len(r.nodes))
	for _, n := range r.nodes {
		result = append(result, n)
	}
	return result
}

// rebuild reconstructs the virtual node array from the current node map.
// Must be called with the write lock held.
func (r *Ring) rebuild() {
	r.vnodes = nil

	for _, node := range r.nodes {
		// Weighted virtual nodes: higher weight → more ring positions
		weight := node.Weight
		if weight <= 0 {
			weight = 100
		}
		numVNodes := r.virtualNodesPerNode * weight / 100

		for i := 0; i < numVNodes; i++ {
			key := fmt.Sprintf("%s:%d", node.ID, i)
			h := hashKey(key)
			r.vnodes = append(r.vnodes, virtualNode{hash: h, nodeID: node.ID})
		}
	}

	// Sort by hash for binary search
	sort.Slice(r.vnodes, func(i, j int) bool {
		return r.vnodes[i].hash < r.vnodes[j].hash
	})
}

// search finds the first virtual node with hash >= the given hash.
// Uses binary search for O(log n) lookup.
func (r *Ring) search(hash uint32) int {
	idx := sort.Search(len(r.vnodes), func(i int) bool {
		return r.vnodes[i].hash >= hash
	})
	if idx >= len(r.vnodes) {
		return 0 // Wrap around to beginning of ring
	}
	return idx
}

// hashKey produces a 32-bit hash from a string key using SHA-256
// truncated to 4 bytes. SHA-256 provides excellent uniformity.
func hashKey(key string) uint32 {
	h := sha256.Sum256([]byte(key))
	return binary.BigEndian.Uint32(h[:4])
}
