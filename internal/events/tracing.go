package events

import (
	"context"

	"github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// kafkaHeaderCarrier adapts kafka.Message headers to OTel's TextMapCarrier
// interface, enabling W3C trace context propagation through Kafka messages.
//
// This is the bridge between OTel's propagation API and Kafka's header format.
// The producer injects trace headers, and the consumer extracts them to
// continue the same distributed trace across service boundaries.
type kafkaHeaderCarrier struct {
	msg *kafka.Message
}

// Get returns the value for the given key from Kafka headers.
func (c *kafkaHeaderCarrier) Get(key string) string {
	for _, h := range c.msg.Headers {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}

// Set adds or overwrites a Kafka header with the given key-value pair.
func (c *kafkaHeaderCarrier) Set(key, value string) {
	// Overwrite if exists
	for i, h := range c.msg.Headers {
		if h.Key == key {
			c.msg.Headers[i].Value = []byte(value)
			return
		}
	}
	c.msg.Headers = append(c.msg.Headers, kafka.Header{
		Key:   key,
		Value: []byte(value),
	})
}

// Keys returns all header keys present in the Kafka message.
func (c *kafkaHeaderCarrier) Keys() []string {
	keys := make([]string, len(c.msg.Headers))
	for i, h := range c.msg.Headers {
		keys[i] = h.Key
	}
	return keys
}

// ExtractTraceContext extracts OTel trace context from a Kafka message,
// returning a context that carries the parent span. This allows consumers
// to create child spans that connect back to the producer's trace.
//
// Usage in consumer:
//
//	ctx := events.ExtractTraceContext(context.Background(), msg)
//	ctx, span := tracer.Start(ctx, "ProcessChunkCreated")
//	defer span.End()
func ExtractTraceContext(ctx context.Context, msg kafka.Message) context.Context {
	propagator := otel.GetTextMapPropagator()
	carrier := &kafkaHeaderCarrier{msg: &msg}
	return propagator.Extract(ctx, carrier)
}

// Ensure kafkaHeaderCarrier implements the interface at compile time.
var _ propagation.TextMapCarrier = (*kafkaHeaderCarrier)(nil)
