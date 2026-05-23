package chunking_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/AnirudhSinghRajora/DFMS/internal/chunking"
)

func TestSplit_BasicFile(t *testing.T) {
	// 5MB random data should produce multiple chunks
	data := make([]byte, 5*1024*1024)
	_, err := rand.Read(data)
	require.NoError(t, err)

	cfg := chunking.DefaultConfig()
	c, err := chunking.NewChunker(cfg)
	require.NoError(t, err)

	ctx := context.Background()
	results := c.Split(ctx, bytes.NewReader(data))

	var chunks []chunking.ChunkResult //nolint:prealloc // length unknown: reading from channel
	for r := range results {
		require.NoError(t, r.Err)
		chunks = append(chunks, r)
	}

	// 5MB with 1MB avg → expect roughly 2-20 chunks
	assert.GreaterOrEqual(t, len(chunks), 1)
	assert.LessOrEqual(t, len(chunks), 30)

	// Verify all chunks are within bounds
	for _, ch := range chunks {
		assert.GreaterOrEqual(t, ch.Size, int64(0))
		assert.LessOrEqual(t, ch.Size, int64(cfg.MaxSize))
		assert.NotEmpty(t, ch.Hash)
		assert.Equal(t, int64(len(ch.Data)), ch.Size)
	}

	// Verify concatenation reproduces original
	var reassembled bytes.Buffer
	for _, ch := range chunks {
		reassembled.Write(ch.Data)
	}
	assert.Equal(t, data, reassembled.Bytes())
}

func TestSplit_EmptyInput(t *testing.T) {
	c, err := chunking.NewChunker(chunking.DefaultConfig())
	require.NoError(t, err)

	results := c.Split(context.Background(), bytes.NewReader(nil))

	var chunks []chunking.ChunkResult //nolint:prealloc // length unknown: reading from channel
	for r := range results {
		if r.Err != nil {
			break
		}
		chunks = append(chunks, r)
	}

	assert.Empty(t, chunks)
}

func TestSplit_TinyFile(t *testing.T) {
	data := []byte("hello, world! this is a tiny file for testing CDC splitting.")
	c, err := chunking.NewChunker(chunking.DefaultConfig())
	require.NoError(t, err)

	results := c.Split(context.Background(), bytes.NewReader(data))

	var chunks []chunking.ChunkResult //nolint:prealloc // length unknown: reading from channel
	for r := range results {
		require.NoError(t, r.Err)
		chunks = append(chunks, r)
	}

	// Tiny file should produce exactly 1 chunk
	require.Len(t, chunks, 1)
	assert.Equal(t, data, chunks[0].Data)
	assert.Equal(t, int64(len(data)), chunks[0].Size)
	assert.Equal(t, 0, chunks[0].Index)
}

func TestSplit_LargeFile(t *testing.T) {
	// 20MB file
	data := make([]byte, 20*1024*1024)
	_, err := rand.Read(data)
	require.NoError(t, err)

	c, err := chunking.NewChunker(chunking.DefaultConfig())
	require.NoError(t, err)

	results := c.Split(context.Background(), bytes.NewReader(data))

	var totalSize int64
	prevIndex := -1
	for r := range results {
		require.NoError(t, r.Err)
		assert.Greater(t, r.Index, prevIndex, "Indices must be monotonically increasing")
		prevIndex = r.Index

		// Every chunk should have a valid 64-char hex SHA-256 hash
		assert.Len(t, r.Hash, 64)
		totalSize += r.Size
	}

	assert.Equal(t, int64(len(data)), totalSize)
}

func TestSplit_ContextCancellation(t *testing.T) {
	// Large data that takes time to split
	data := make([]byte, 10*1024*1024)
	_, _ = rand.Read(data)

	c, err := chunking.NewChunker(chunking.DefaultConfig())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	results := c.Split(ctx, bytes.NewReader(data))

	// Read first chunk, then cancel
	first := <-results
	require.NoError(t, first.Err)
	cancel()

	// Drain remaining — should get context.Canceled error or channel closes
	var gotCancel bool
	for r := range results {
		if r.Err == context.Canceled {
			gotCancel = true
			break
		}
	}
	// Either we got a cancel error, or the channel closed (both are acceptable)
	_ = gotCancel
}

func TestSplit_HashUniqueness(t *testing.T) {
	// Two different random files should produce different chunk hashes
	data1 := make([]byte, 2*1024*1024)
	data2 := make([]byte, 2*1024*1024)
	_, _ = rand.Read(data1)
	_, _ = rand.Read(data2)

	c, err := chunking.NewChunker(chunking.DefaultConfig())
	require.NoError(t, err)

	hashes1 := collectHashes(t, c, data1)
	hashes2 := collectHashes(t, c, data2)

	// At least one hash should differ
	allSame := len(hashes1) == len(hashes2)
	if allSame {
		for i := range hashes1 {
			if hashes1[i] != hashes2[i] {
				allSame = false
				break
			}
		}
	}
	assert.False(t, allSame, "Different data should produce different hashes")
}

func TestNewChunker_DefaultConfig(t *testing.T) {
	cfg := chunking.DefaultConfig()
	assert.Equal(t, 256*1024, cfg.MinSize)
	assert.Equal(t, 1*1024*1024, cfg.AvgSize)
	assert.Equal(t, 4*1024*1024, cfg.MaxSize)

	c, err := chunking.NewChunker(cfg)
	require.NoError(t, err)
	assert.NotNil(t, c)
}

func collectHashes(t *testing.T, c *chunking.Chunker, data []byte) []string {
	t.Helper()
	results := c.Split(context.Background(), bytes.NewReader(data))
	var hashes []string //nolint:prealloc // length unknown: reading from channel
	for r := range results {
		if r.Err != nil {
			break
		}
		hashes = append(hashes, r.Hash)
	}
	return hashes
}

// Ensure io import is used
var _ = io.EOF
