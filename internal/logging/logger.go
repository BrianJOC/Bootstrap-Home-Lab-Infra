package logging

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/homelab/bootstrap-home-infra/internal/stage"
)

// NewLogger creates a structured zap logger that writes JSON to a log file
// and also emits WARN+ messages to stderr.
func NewLogger(runDir string) (*zap.Logger, error) {
	logDir := filepath.Join("logs", runDir)
	if err := os.MkdirAll(logDir, 0750); err != nil {
		return nil, fmt.Errorf("creating log directory %q: %w", logDir, err)
	}

	logPath := filepath.Join(logDir, "bootstrap.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("opening log file %q: %w", logPath, err)
	}

	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.TimeKey = "ts"
	encoderCfg.MessageKey = "msg"
	encoderCfg.LevelKey = "level"
	encoderCfg.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderCfg.EncodeLevel = zapcore.LowercaseLevelEncoder

	fileEncoder := zapcore.NewJSONEncoder(encoderCfg)
	stderrEncoder := zapcore.NewJSONEncoder(encoderCfg)

	fileCore := zapcore.NewCore(
		fileEncoder,
		zapcore.AddSync(logFile),
		zapcore.DebugLevel,
	)

	stderrCore := zapcore.NewCore(
		stderrEncoder,
		zapcore.AddSync(os.Stderr),
		zapcore.WarnLevel,
	)

	core := zapcore.NewTee(fileCore, stderrCore)
	logger := zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1))

	return logger, nil
}

// LogSink implements stage.Sink using a zap logger.
// In headless mode, Prompt and Verify read from stdin.
// In TUI mode these methods are overridden by TUISink.
type LogSink struct {
	logger   *zap.Logger
	headless bool
}

// NewLogSink creates a LogSink backed by the given logger.
func NewLogSink(logger *zap.Logger, headless bool) *LogSink {
	return &LogSink{logger: logger, headless: headless}
}

// Info logs an informational message.
func (s *LogSink) Info(stageName, msg string) {
	s.logger.Info(msg, zap.String("stage", stageName))
}

// Warn logs a warning message.
func (s *LogSink) Warn(stageName, msg string) {
	s.logger.Warn(msg, zap.String("stage", stageName))
}

// Error logs an error message.
func (s *LogSink) Error(stageName, msg string, err error) {
	s.logger.Error(msg, zap.String("stage", stageName), zap.Error(err))
}

// Prompt reads a response from stdin (headless mode).
// In headless mode, prompts are read from stdin if it is a pipe.
// Returns an error if stdin is not a pipe.
func (s *LogSink) Prompt(stageName, question string) (string, error) {
	if s.headless {
		// In headless mode, read from stdin.
		fi, err := os.Stdin.Stat()
		if err != nil {
			return "", fmt.Errorf("stat stdin: %w", err)
		}
		if fi.Mode()&os.ModeNamedPipe == 0 {
			return "", fmt.Errorf("stage %q requires interactive input but --headless is set and stdin is not a pipe", stageName)
		}
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			return scanner.Text(), nil
		}
		return "", fmt.Errorf("EOF reading prompt for stage %q", stageName)
	}
	return "", fmt.Errorf("Prompt called on LogSink in non-headless mode; use TUISink for interactive prompts")
}

// Verify asks a yes/no question via stdin in headless mode.
func (s *LogSink) Verify(stageName, message string) (bool, error) {
	if s.headless {
		fi, err := os.Stdin.Stat()
		if err != nil {
			return false, fmt.Errorf("stat stdin: %w", err)
		}
		if fi.Mode()&os.ModeNamedPipe == 0 {
			return false, fmt.Errorf("stage %q requires verification but --headless is set and stdin is not a pipe", stageName)
		}
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			answer := scanner.Text()
			return answer == "y" || answer == "yes", nil
		}
		return false, fmt.Errorf("EOF reading verification for stage %q", stageName)
	}
	return false, fmt.Errorf("Verify called on LogSink in non-headless mode; use TUISink for interactive verification")
}

// Ensure LogSink implements stage.Sink.
var _ stage.Sink = (*LogSink)(nil)
