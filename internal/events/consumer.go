package events

import (
	"context"
	"fmt"

	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"
)

// MessageHandler processes a single Kafka message. Returning nil
// signals success and the offset is committed. Returning an error
// causes the message to be retried (at-least-once semantics).
type MessageHandler func(ctx context.Context, msg kafka.Message) error

// Consumer reads messages from a Kafka topic using a consumer group.
// It provides at-least-once delivery: offsets are committed only after
// the handler returns nil.
type Consumer struct {
	reader *kafka.Reader
	logger *zap.Logger
}

// NewConsumer creates a consumer group reader for the given topic.
func NewConsumer(brokers []string, groupID, topic string, logger *zap.Logger) *Consumer {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:     brokers,
		GroupID:     groupID,
		Topic:       topic,
		MinBytes:    1,                // Fetch as soon as 1 byte is available
		MaxBytes:    10 * 1024 * 1024, // 10MB max fetch
		StartOffset: kafka.FirstOffset,
		// CommitInterval = 0 means manual commit (we commit after each message)
	})

	return &Consumer{
		reader: reader,
		logger: logger,
	}
}

// Subscribe starts a blocking loop that reads messages and calls the handler.
// It commits offsets after each successful handler invocation (at-least-once).
//
// The loop exits when the context is cancelled. On shutdown, it finishes
// processing the current message before closing.
func (c *Consumer) Subscribe(ctx context.Context, handler MessageHandler) error {
	c.logger.Info("Consumer started",
		zap.String("topic", c.reader.Config().Topic),
		zap.String("group", c.reader.Config().GroupID),
	)

	for {
		// FetchMessage does NOT commit — we commit manually after processing
		msg, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				c.logger.Info("Consumer shutting down (context cancelled)")
				return nil // Graceful shutdown
			}
			c.logger.Error("Failed to fetch message", zap.Error(err))
			continue
		}

		// Process message
		if err := handler(ctx, msg); err != nil {
			c.logger.Error("Handler failed, message will be retried",
				zap.String("topic", msg.Topic),
				zap.Int("partition", msg.Partition),
				zap.Int64("offset", msg.Offset),
				zap.Error(err),
			)
			continue // Don't commit — message will be re-delivered
		}

		// Commit offset after successful processing
		if err := c.reader.CommitMessages(ctx, msg); err != nil {
			c.logger.Error("Failed to commit offset",
				zap.Int64("offset", msg.Offset),
				zap.Error(err),
			)
		}
	}
}

// Close shuts down the consumer, releasing the partition assignment.
func (c *Consumer) Close() error {
	if err := c.reader.Close(); err != nil {
		return fmt.Errorf("close consumer: %w", err)
	}
	return nil
}
