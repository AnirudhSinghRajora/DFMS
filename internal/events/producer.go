package events

import (
	"context"
	"fmt"
	"time"

	"github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel"
	"go.uber.org/zap"
)

// Producer publishes events to Kafka topics using segmentio/kafka-go.
// It uses a batched async writer for throughput, with configurable retries.
type Producer struct {
	writers map[string]*kafka.Writer // One writer per topic for optimal batching
	brokers []string
	logger  *zap.Logger
}

// NewProducer creates a Kafka producer with writers for all DFMS topics.
func NewProducer(brokers []string, logger *zap.Logger) *Producer {
	topics := []string{TopicChunksCreated, TopicChunksDeleted, TopicFilesDeleted, TopicNodesHealth}
	writers := make(map[string]*kafka.Writer, len(topics))

	for _, topic := range topics {
		writers[topic] = &kafka.Writer{
			Addr:         kafka.TCP(brokers...),
			Topic:        topic,
			Balancer:     &kafka.Hash{}, // Partition by key (chunk hash) for ordering
			BatchTimeout: 50 * time.Millisecond,
			BatchSize:    100,
			MaxAttempts:  5,
			RequiredAcks: kafka.RequireOne, // Leader ack (balance durability vs latency)
			Async:        false,            // Synchronous for reliability
		}
	}

	return &Producer{
		writers: writers,
		brokers: brokers,
		logger:  logger,
	}
}

// Publish sends an event to the specified Kafka topic.
// The partitionKey ensures messages for the same chunk/file go to the same
// partition, preserving ordering for that entity.
func (p *Producer) Publish(ctx context.Context, topic, partitionKey, traceID string, payload interface{}) error {
	writer, ok := p.writers[topic]
	if !ok {
		return fmt.Errorf("unknown topic: %s", topic)
	}

	envelope, err := NewEnvelope(topic, traceID, payload)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}

	value, err := envelope.Marshal()
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	msg := kafka.Message{
		Key:   []byte(partitionKey),
		Value: value,
	}

	// Inject OTel trace context into Kafka headers for cross-service propagation.
	// The replication manager will extract this to continue the same trace.
	propagator := otel.GetTextMapPropagator()
	carrier := &kafkaHeaderCarrier{msg: &msg}
	propagator.Inject(ctx, carrier)

	if err := writer.WriteMessages(ctx, msg); err != nil {
		p.logger.Error("Failed to publish event",
			zap.String("topic", topic),
			zap.String("key", partitionKey),
			zap.Error(err),
		)
		return fmt.Errorf("publish to %s: %w", topic, err)
	}

	p.logger.Debug("Event published",
		zap.String("topic", topic),
		zap.String("key", partitionKey),
		zap.String("trace_id", traceID),
	)
	return nil
}

// Close shuts down all Kafka writers, flushing pending messages.
func (p *Producer) Close() error {
	var firstErr error
	for topic, w := range p.writers {
		if err := w.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close writer for %s: %w", topic, err)
		}
	}
	return firstErr
}
