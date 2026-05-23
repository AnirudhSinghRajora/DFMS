package chunking

import (
	"context"
	"fmt"
	"io"
)

// Assemble concatenates ordered chunk readers into a single output writer.
// Used during download to reconstruct the original file from its chunks.
//
// Each reader is consumed in order and closed after reading. If any reader
// fails, the operation is aborted and the error is returned.
//
// The context can be used to cancel a long-running reassembly.
func Assemble(ctx context.Context, dst io.Writer, readers []io.ReadCloser) error {
	for i, r := range readers {
		select {
		case <-ctx.Done():
			// Close remaining readers on cancellation
			for j := i; j < len(readers); j++ {
				readers[j].Close()
			}
			return ctx.Err()
		default:
		}

		_, err := io.Copy(dst, r)
		r.Close()
		if err != nil {
			// Close remaining readers on error
			for j := i + 1; j < len(readers); j++ {
				readers[j].Close()
			}
			return fmt.Errorf("failed to read chunk %d: %w", i, err)
		}
	}
	return nil
}
