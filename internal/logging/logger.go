package logging

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/homelab/bootstrap-home-infra/internal/stage"
)

// NewLogger creates a structured zap logger that writes JSON to a log file
// and also emits WARN+ messages to stderr.
// runDir must be an absolute path (e.g. "/home/user/project/logs/run-20240101T120000").
func NewLogger(runDir string) (*zap.Logger, error) {
	if err := os.MkdirAll(runDir, 0750); err != nil {
		return nil, fmt.Errorf("creating log directory %q: %w", runDir, err)
	}

	logPath := filepath.Join(runDir, "bootstrap.log")
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
// In headless mode, Prompt and Confirm read from stdin.
type LogSink struct {
	logger   *zap.Logger
	headless bool
}

// NewLogSink creates a LogSink backed by the given logger.
func NewLogSink(logger *zap.Logger, headless bool) *LogSink {
	return &LogSink{logger: logger, headless: headless}
}

// StageStarted logs that a stage has begun.
func (s *LogSink) StageStarted(name string) {
	s.logger.Info("stage started", zap.String("stage", name))
}

// StageComplete logs that a stage completed successfully.
func (s *LogSink) StageComplete(name string) {
	s.logger.Info("stage complete", zap.String("stage", name))
}

// StageFailed logs that a stage failed.
func (s *LogSink) StageFailed(name, errMsg, logPath string, logLine int) {
	s.logger.Error("stage failed",
		zap.String("stage", name),
		zap.String("error", errMsg),
		zap.String("log_path", logPath),
		zap.Int("log_line", logLine),
	)
}

// Log writes a leveled message to the logger.
func (s *LogSink) Log(level, stageName, msg string, fields map[string]string) {
	zapFields := []zap.Field{zap.String("stage", stageName)}
	for k, v := range fields {
		zapFields = append(zapFields, zap.String(k, v))
	}
	switch strings.ToLower(level) {
	case "warn", "warning":
		s.logger.Warn(msg, zapFields...)
	case "error":
		s.logger.Error(msg, zapFields...)
	default:
		s.logger.Info(msg, zapFields...)
	}
}

// Prompt reads a response from stdin (headless mode).
// If masked is true, the value is sensitive and must not be echoed to logs.
func (s *LogSink) Prompt(label string, masked bool) (string, error) {
	if !s.headless {
		return "", fmt.Errorf("Prompt called on LogSink in non-headless mode; use TUISink for interactive prompts")
	}
	fi, err := os.Stdin.Stat()
	if err != nil {
		return "", fmt.Errorf("stat stdin: %w", err)
	}
	if fi.Mode()&os.ModeNamedPipe == 0 {
		return "", fmt.Errorf("prompt %q requires interactive input but --headless is set and stdin is not a pipe", label)
	}
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		return scanner.Text(), nil
	}
	return "", fmt.Errorf("EOF reading prompt %q", label)
}

// Confirm asks a yes/no question via stdin in headless mode.
func (s *LogSink) Confirm(msg string) (bool, error) {
	if !s.headless {
		return false, fmt.Errorf("Confirm called on LogSink in non-headless mode; use TUISink for interactive confirmation")
	}
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false, fmt.Errorf("stat stdin: %w", err)
	}
	if fi.Mode()&os.ModeNamedPipe == 0 {
		return false, fmt.Errorf("confirmation %q requires interactive input but --headless is set and stdin is not a pipe", msg)
	}
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
		return answer == "y" || answer == "yes", nil
	}
	return false, fmt.Errorf("EOF reading confirmation %q", msg)
}

// ShowWarningBox logs each warning line at warn level.
func (s *LogSink) ShowWarningBox(lines []string) {
	for _, line := range lines {
		s.logger.Warn(line)
	}
}

// Ensure LogSink implements stage.Sink.
var _ stage.Sink = (*LogSink)(nil)
