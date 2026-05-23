package chunking_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/AnirudhSinghRajora/DFMS/internal/chunking"
)

func TestAssemble_Ordered(t *testing.T) {
	chunk1 := []byte("Hello, ")
	chunk2 := []byte("World!")
	chunk3 := []byte(" Testing assembly.")

	readers := []io.ReadCloser{
		io.NopCloser(bytes.NewReader(chunk1)),
		io.NopCloser(bytes.NewReader(chunk2)),
		io.NopCloser(bytes.NewReader(chunk3)),
	}

	var buf bytes.Buffer
	err := chunking.Assemble(context.Background(), &buf, readers)
	require.NoError(t, err)

	assert.Equal(t, "Hello, World! Testing assembly.", buf.String())
}

func TestAssemble_SingleChunk(t *testing.T) {
	data := []byte("single chunk content")
	readers := []io.ReadCloser{
		io.NopCloser(bytes.NewReader(data)),
	}

	var buf bytes.Buffer
	err := chunking.Assemble(context.Background(), &buf, readers)
	require.NoError(t, err)

	assert.Equal(t, data, buf.Bytes())
}

func TestAssemble_EmptyChunks(t *testing.T) {
	var buf bytes.Buffer
	err := chunking.Assemble(context.Background(), &buf, nil)
	require.NoError(t, err)
	assert.Empty(t, buf.Bytes())
}

func TestAssemble_EmptyReaderList(t *testing.T) {
	var buf bytes.Buffer
	err := chunking.Assemble(context.Background(), &buf, []io.ReadCloser{})
	require.NoError(t, err)
	assert.Empty(t, buf.Bytes())
}

// errorReader always returns an error on Read.
type errorReader struct{ err error }

func (r *errorReader) Read([]byte) (int, error) { return 0, r.err }
func (r *errorReader) Close() error             { return nil }

// trackCloser tracks whether Close was called.
type trackCloser struct {
	io.Reader
	closed bool
}

func (tc *trackCloser) Close() error {
	tc.closed = true
	return nil
}

func TestAssemble_ReaderError(t *testing.T) {
	chunk1 := io.NopCloser(bytes.NewReader([]byte("good chunk")))
	badChunk := &errorReader{err: fmt.Errorf("disk I/O failure")}
	chunk3 := &trackCloser{Reader: bytes.NewReader([]byte("never read"))}

	readers := []io.ReadCloser{chunk1, badChunk, chunk3}

	var buf bytes.Buffer
	err := chunking.Assemble(context.Background(), &buf, readers)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read chunk 1")
	// Verify remaining readers were closed
	assert.True(t, chunk3.closed, "Remaining readers should be closed on error")
}

func TestAssemble_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	chunk1 := &trackCloser{Reader: bytes.NewReader([]byte("data1"))}
	chunk2 := &trackCloser{Reader: bytes.NewReader([]byte("data2"))}

	readers := []io.ReadCloser{chunk1, chunk2}

	var buf bytes.Buffer
	err := chunking.Assemble(ctx, &buf, readers)

	assert.ErrorIs(t, err, context.Canceled)
	// Both readers should be closed
	assert.True(t, chunk1.closed)
	assert.True(t, chunk2.closed)
}

func TestAssemble_LargeReassembly(t *testing.T) {
	// Simulate reassembling a chunked file
	const numChunks = 10
	const chunkSize = 1024

	var expected bytes.Buffer
	readers := make([]io.ReadCloser, numChunks)

	for i := 0; i < numChunks; i++ {
		data := bytes.Repeat([]byte{byte(i)}, chunkSize)
		expected.Write(data)
		readers[i] = io.NopCloser(bytes.NewReader(data))
	}

	var result bytes.Buffer
	err := chunking.Assemble(context.Background(), &result, readers)
	require.NoError(t, err)

	assert.Equal(t, expected.Bytes(), result.Bytes())
}
