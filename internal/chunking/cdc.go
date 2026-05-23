// Package chunking implements Content-Defined Chunking (CDC) for splitting files
// into variable-size blocks that are stable under modification. This is the
// foundation of DFMS's deduplication: modifying one byte in a file only affects
// 1-2 chunks rather than every chunk after the modification point.
package chunking

import (
	"context"
	"crypto/rand"
	"io"

	"github.com/restic/chunker"
)

// ChunkResult represents a single chunk produced by the CDC splitter.
type ChunkResult struct {
	Data  []byte // Raw chunk bytes
	Size  int64  // Chunk size in bytes
	Index int    // Position in file (0-based)
	Hash  string // SHA-256 hex digest
	Err   error  // Non-nil if an error occurred during chunking
}

// CDCConfig holds the parameters for Content-Defined Chunking.
type CDCConfig struct {
	MinSize int // Minimum chunk size in bytes (default 256KB)
	AvgSize int // Average/target chunk size in bytes (default 1MB)
	MaxSize int // Maximum chunk size in bytes (default 4MB)
}

// DefaultConfig returns the default CDC configuration.
// These values balance dedup effectiveness with chunk count overhead:
//   - 256KB min prevents excessive tiny chunks
//   - 1MB avg gives good dedup granularity
//   - 4MB max bounds memory usage per chunk
func DefaultConfig() CDCConfig {
	return CDCConfig{
		MinSize: 256 * 1024,     // 256 KB
		AvgSize: 1 * 1024 * 1024, // 1 MB
		MaxSize: 4 * 1024 * 1024, // 4 MB
	}
}

// Chunker splits an input stream into variable-size chunks using the
// Rabin fingerprinting algorithm from restic/chunker.
type Chunker struct {
	config CDCConfig
	pol    chunker.Pol // Rabin polynomial for fingerprinting
}

// NewChunker creates a new CDC chunker with the given configuration.
// It generates a random Rabin polynomial to seed the fingerprinting.
func NewChunker(cfg CDCConfig) (*Chunker, error) {
	pol, err := chunker.RandomPolynomial()
	if err != nil {
		// Fallback: use a fixed polynomial if random generation fails
		// (only happens if crypto/rand is broken)
		var seed [8]byte
		if _, readErr := rand.Read(seed[:]); readErr != nil {
			return nil, readErr
		}
		pol = chunker.Pol(0x3DA3358B4DC173)
	}

	return &Chunker{
		config: cfg,
		pol:    pol,
	}, err
}

// Split reads from the input reader and produces chunks on a channel.
// The channel is closed when the reader is fully consumed or an error occurs.
// Cancelling the context stops chunking and the goroutine exits cleanly.
//
// Each ChunkResult includes the raw bytes, size, index, and SHA-256 hash.
// If an error occurs, a single ChunkResult with Err set is sent, then the
// channel is closed.
//
// The caller MUST drain the channel to avoid goroutine leaks.
func (c *Chunker) Split(ctx context.Context, r io.Reader) <-chan ChunkResult {
	results := make(chan ChunkResult, 4) // Buffer 4 chunks for pipeline efficiency

	go func() {
		defer close(results)

		chnkr := chunker.NewWithBoundaries(r, c.pol, uint(c.config.MinSize), uint(c.config.MaxSize))
		buf := make([]byte, c.config.MaxSize)
		index := 0

		for {
			// Check for context cancellation before each chunk
			select {
			case <-ctx.Done():
				results <- ChunkResult{Err: ctx.Err()}
				return
			default:
			}

			chunk, err := chnkr.Next(buf)
			if err == io.EOF {
				return // All data consumed
			}
			if err != nil {
				results <- ChunkResult{Err: err}
				return
			}

			// Copy chunk data since the buffer is reused by chunker.Next()
			data := make([]byte, chunk.Length)
			copy(data, chunk.Data)

			hash := SHA256Hash(data)

			select {
			case results <- ChunkResult{
				Data:  data,
				Size:  int64(chunk.Length),
				Index: index,
				Hash:  hash,
			}:
			case <-ctx.Done():
				results <- ChunkResult{Err: ctx.Err()}
				return
			}

			index++
		}
	}()

	return results
}
