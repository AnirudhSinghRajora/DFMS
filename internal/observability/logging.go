// Package observability provides structured logging for the DFMS.
// Uses Zap for high-performance, structured JSON logging in production
// and human-readable console output in development.
package observability

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	// AppVersion is the current application version.
	AppVersion = "0.1.0"
)

// NewLogger creates a configured Zap logger based on the environment mode.
// In production mode, it outputs JSON. In development mode, it outputs
// colored, human-readable console format.
func NewLogger(mode, serviceName string) (*zap.Logger, error) {
	var config zap.Config

	if mode == "production" {
		config = zap.NewProductionConfig()
		config.EncoderConfig.TimeKey = "timestamp"
		config.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
		config.EncoderConfig.StacktraceKey = "stacktrace"
	} else {
		config = zap.NewDevelopmentConfig()
		config.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
		config.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	}

	config.OutputPaths = []string{"stdout"}
	config.ErrorOutputPaths = []string{"stderr"}

	logger, err := config.Build(
		zap.Fields(
			zap.String("service", serviceName),
			zap.String("version", AppVersion),
			zap.String("environment", mode),
			zap.Int("pid", os.Getpid()),
		),
		zap.AddCallerSkip(0),
	)
	if err != nil {
		return nil, err
	}

	return logger, nil
}

// NewNopLogger creates a no-op logger for testing.
func NewNopLogger() *zap.Logger {
	return zap.NewNop()
}
