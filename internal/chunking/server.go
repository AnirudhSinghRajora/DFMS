// Package chunking contains the gRPC server for the ChunkService.
// This is the core data-plane service: it handles CDC splitting, SHA-256
// fingerprinting, dedup checking against MinIO, and chunk storage/retrieval.
package chunking

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/AnirudhSinghRajora/DFMS/api/proto/chunkpb"
	"github.com/AnirudhSinghRajora/DFMS/internal/observability"
	"github.com/AnirudhSinghRajora/DFMS/internal/storage"
)

const (
	// maxUploadWorkers controls parallel chunk uploads to MinIO.
	// Bounded to prevent memory explosion (each in-flight chunk is ~1-4MB).
	maxUploadWorkers = 4

	// streamFrameSize is the size of byte frames in gRPC download streaming.
	streamFrameSize = 64 * 1024 // 64 KB
)

// ChunkServer implements the ChunkService gRPC interface.
type ChunkServer struct {
	pb.UnimplementedChunkServiceServer
	store   storage.ObjectStore
	chunker *Chunker
	logger  *zap.Logger
}

// NewChunkServer creates a new ChunkService gRPC server.
func NewChunkServer(store storage.ObjectStore, cfg CDCConfig, logger *zap.Logger) (*ChunkServer, error) {
	c, err := NewChunker(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create chunker: %w", err)
	}

	return &ChunkServer{
		store:   store,
		chunker: c,
		logger:  logger,
	}, nil
}

// UploadFile implements client-streaming upload. The first message must contain
// metadata (filename, user_id). Subsequent messages contain raw file bytes.
//
// The server pipes incoming bytes through the CDC splitter, hashes each chunk,
// checks MinIO for existence (dedup), and uploads new chunks concurrently.
//
// Returns chunk info (hashes, sizes, dedup stats) and the whole-file checksum.
func (s *ChunkServer) UploadFile(stream pb.ChunkService_UploadFileServer) error {
	ctx := stream.Context()

	// 1. Read metadata from the first message
	firstMsg, err := stream.Recv()
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "failed to receive metadata: %v", err)
	}

	meta := firstMsg.GetMetadata()
	if meta == nil {
		return status.Error(codes.InvalidArgument, "first message must contain metadata")
	}

	s.logger.Info("Upload started",
		zap.String("file_name", meta.FileName),
		zap.String("user_id", meta.UserId),
	)

	// 2. Create a pipe: gRPC recv goroutine writes → CDC chunker reads
	pr, pw := io.Pipe()
	fileHasher := sha256.New() // Compute whole-file checksum in parallel
	writer := io.MultiWriter(pw, fileHasher)

	// Goroutine: read from gRPC stream and write into the pipe
	var totalSize int64
	recvErrCh := make(chan error, 1)
	go func() {
		defer pw.Close()
		for {
			msg, err := stream.Recv()
			if err == io.EOF {
				recvErrCh <- nil
				return
			}
			if err != nil {
				recvErrCh <- err
				return
			}

			data := msg.GetChunkData()
			if data == nil {
				continue
			}

			n, err := writer.Write(data)
			if err != nil {
				recvErrCh <- err
				return
			}
			atomic.AddInt64(&totalSize, int64(n))
		}
	}()

	// 3. Run CDC chunker on the pipe reader
	tracer := observability.Tracer("chunking")
	ctx, cdcSpan := tracer.Start(ctx, "CDC.SplitAndUpload",
		trace.WithAttributes(attribute.String("file.name", meta.FileName)),
	)
	chunkResults := s.chunker.Split(ctx, pr)

	// 4. Process chunks: dedup check + upload new ones concurrently
	var (
		chunks    []*pb.ChunkInfo
		mu        sync.Mutex
		newCount  int32
		dedupCount int32
		wg        sync.WaitGroup
		sem       = make(chan struct{}, maxUploadWorkers) // Concurrency limiter
		uploadErr error
	)

	for result := range chunkResults {
		if result.Err != nil {
			uploadErr = result.Err
			break
		}

		// Capture loop variable
		r := result

		wg.Add(1)
		sem <- struct{}{} // Acquire worker slot

		go func() {
			defer wg.Done()
			defer func() { <-sem }() // Release worker slot

			isDuplicate := false

			// Check if chunk already exists in MinIO (dedup)
			dedupStart := time.Now()
			exists, err := s.store.ChunkExists(ctx, r.Hash)
			observability.RecordMinIOOp("exists", time.Since(dedupStart))
			if err != nil {
				s.logger.Warn("Dedup check failed, uploading anyway",
					zap.String("hash", r.Hash[:12]),
					zap.Error(err),
				)
				exists = false
			}

			if exists {
				isDuplicate = true
				atomic.AddInt32(&dedupCount, 1)
				observability.DedupHitsTotal.Inc()
			} else {
				// Upload new chunk to MinIO
				putStart := time.Now()
				reader := bytes.NewReader(r.Data)
				if err := s.store.PutChunk(ctx, r.Hash, reader, r.Size); err != nil {
					s.logger.Error("Failed to upload chunk",
						zap.String("hash", r.Hash[:12]),
						zap.Error(err),
					)
					mu.Lock()
					if uploadErr == nil {
						uploadErr = err
					}
					mu.Unlock()
					return
				}
				observability.RecordMinIOOp("put", time.Since(putStart))
				atomic.AddInt32(&newCount, 1)
				observability.DedupMissesTotal.Inc()
			}

			info := &pb.ChunkInfo{
				Hash:        r.Hash,
				Size:        r.Size,
				Index:       int32(r.Index),
				IsDuplicate: isDuplicate,
			}

			mu.Lock()
			chunks = append(chunks, info)
			mu.Unlock()
		}()
	}

	wg.Wait()

	// End the CDC span with final stats
	cdcSpan.SetAttributes(
		attribute.Int("chunks.total", len(chunks)),
		attribute.Int64("chunks.new", int64(newCount)),
		attribute.Int64("chunks.deduped", int64(dedupCount)),
	)
	cdcSpan.End()

	// Check for errors from the recv goroutine
	if recvErr := <-recvErrCh; recvErr != nil && uploadErr == nil {
		uploadErr = recvErr
	}

	if uploadErr != nil {
		s.logger.Error("Upload failed", zap.Error(uploadErr))
		return status.Errorf(codes.Internal, "upload failed: %v", uploadErr)
	}

	// Sort chunks by index (they may have been processed out of order)
	sortChunks(chunks)

	fileChecksum := hex.EncodeToString(fileHasher.Sum(nil))
	finalSize := atomic.LoadInt64(&totalSize)

	s.logger.Info("Upload completed",
		zap.String("file_name", meta.FileName),
		zap.String("checksum", fileChecksum[:12]),
		zap.Int64("size", finalSize),
		zap.Int("chunks", len(chunks)),
		zap.Int32("new", newCount),
		zap.Int32("deduped", dedupCount),
	)

	return stream.SendAndClose(&pb.UploadFileResponse{
		FileChecksum: fileChecksum,
		TotalSize:    finalSize,
		ChunkCount:   int32(len(chunks)),
		NewChunks:    newCount,
		DedupChunks:  dedupCount,
		Chunks:       chunks,
	})
}

// DownloadFile implements server-streaming download. Given an ordered list
// of chunk hashes (the file manifest), it fetches each chunk from MinIO
// and streams the bytes back to the client.
func (s *ChunkServer) DownloadFile(req *pb.DownloadFileRequest, stream pb.ChunkService_DownloadFileServer) error {
	ctx := stream.Context()

	if len(req.ChunkHashes) == 0 {
		return status.Error(codes.InvalidArgument, "chunk_hashes must not be empty")
	}

	s.logger.Info("Download started", zap.Int("chunks", len(req.ChunkHashes)))

	for i, hash := range req.ChunkHashes {
		select {
		case <-ctx.Done():
			return status.Error(codes.Canceled, "download cancelled by client")
		default:
		}

		// Fetch chunk from MinIO
		reader, _, err := s.store.GetChunk(ctx, hash)
		if err != nil {
			s.logger.Error("Failed to get chunk",
				zap.String("hash", hash[:12]),
				zap.Int("index", i),
				zap.Error(err),
			)
			return status.Errorf(codes.Internal, "failed to get chunk %d: %v", i, err)
		}

		// Stream chunk bytes in frames
		buf := make([]byte, streamFrameSize)
		for {
			n, readErr := reader.Read(buf)
			if n > 0 {
				if sendErr := stream.Send(&pb.DownloadFileResponse{
					Data:       buf[:n],
					ChunkHash:  hash,
					ChunkIndex: int32(i),
				}); sendErr != nil {
					reader.Close()
					return status.Errorf(codes.Internal, "failed to send chunk data: %v", sendErr)
				}
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				reader.Close()
				return status.Errorf(codes.Internal, "failed to read chunk %d: %v", i, readErr)
			}
		}

		reader.Close()
	}

	s.logger.Info("Download completed", zap.Int("chunks", len(req.ChunkHashes)))
	return nil
}

// DeleteChunks removes chunks from MinIO storage. This is called when
// the gateway determines a chunk's ref_count has reached 0.
func (s *ChunkServer) DeleteChunks(ctx context.Context, req *pb.DeleteChunksRequest) (*pb.DeleteChunksResponse, error) {
	var deleted, failed int32

	for _, hash := range req.ChunkHashes {
		if err := s.store.DeleteChunk(ctx, hash); err != nil {
			s.logger.Warn("Failed to delete chunk",
				zap.String("hash", hash[:12]),
				zap.Error(err),
			)
			failed++
		} else {
			deleted++
		}
	}

	return &pb.DeleteChunksResponse{
		DeletedCount: deleted,
		FailedCount:  failed,
	}, nil
}

// Health returns the health status of the ChunkService.
func (s *ChunkServer) Health(ctx context.Context, req *pb.HealthRequest) (*pb.HealthResponse, error) {
	minioHealthy := true
	if err := s.store.HealthCheck(ctx); err != nil {
		minioHealthy = false
	}

	st := "healthy"
	if !minioHealthy {
		st = "unhealthy"
	}

	return &pb.HealthResponse{
		Status:         st,
		MinioConnected: minioHealthy,
		Version:        observability.AppVersion,
	}, nil
}

// sortChunks sorts chunk info by index (insertion sort, good for small N).
func sortChunks(chunks []*pb.ChunkInfo) {
	for i := 1; i < len(chunks); i++ {
		for j := i; j > 0 && chunks[j].Index < chunks[j-1].Index; j-- {
			chunks[j], chunks[j-1] = chunks[j-1], chunks[j]
		}
	}
}
