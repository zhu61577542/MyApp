package logging

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Level string

const (
	Debug Level = "debug"
	Info  Level = "info"
	Warn  Level = "warn"
	Error Level = "error"
)

func ParseLevel(value string) (Level, error) {
	switch Level(strings.ToLower(strings.TrimSpace(value))) {
	case Debug:
		return Debug, nil
	case Info:
		return Info, nil
	case Warn:
		return Warn, nil
	case Error:
		return Error, nil
	default:
		return "", fmt.Errorf("日志级别无效: %q", value)
	}
}

func (level Level) rank() int {
	switch level {
	case Debug:
		return 0
	case Info:
		return 1
	case Warn:
		return 2
	case Error:
		return 3
	default:
		return 1
	}
}

type Settings struct {
	Enabled bool
	Level   Level
}

func DefaultSettings() Settings {
	return Settings{Enabled: true, Level: Info}
}

type Logger struct {
	mu      sync.Mutex
	file    *os.File
	path    string
	enabled bool
	level   Level
	streams []*streamWriter
}

func Open(path string, settings Settings) (*Logger, error) {
	if settings.Level == "" {
		settings.Level = Info
	}
	if _, err := ParseLevel(string(settings.Level)); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(0600); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return &Logger{file: file, path: path, enabled: settings.Enabled, level: settings.Level}, nil
}

func (logger *Logger) Path() string {
	if logger == nil {
		return ""
	}
	return logger.path
}

func (logger *Logger) Close() error {
	if logger == nil {
		return nil
	}
	logger.mu.Lock()
	streams := append([]*streamWriter(nil), logger.streams...)
	logger.mu.Unlock()
	for _, stream := range streams {
		stream.Flush()
	}
	logger.mu.Lock()
	defer logger.mu.Unlock()
	if logger.file == nil {
		return nil
	}
	err := logger.file.Close()
	logger.file = nil
	return err
}

func (logger *Logger) Log(level Level, message string) {
	if logger == nil || level.rank() < logger.level.rank() || !logger.enabled {
		return
	}
	logger.mu.Lock()
	defer logger.mu.Unlock()
	if logger.file == nil {
		return
	}
	message = strings.TrimRight(message, "\r\n")
	if message == "" {
		return
	}
	message = redact(message)
	message = strings.NewReplacer("\r", "\\r", "\n", "\\n").Replace(message)
	_, _ = fmt.Fprintf(logger.file, "%s [%s] %s\n", time.Now().Format(time.RFC3339Nano), level, message)
}

func redact(message string) string {
	if index := strings.Index(message, "配对码:"); index >= 0 {
		return message[:index] + "配对码: [已隐藏]"
	}
	return message
}

func (logger *Logger) Stream(level Level) io.Writer {
	stream := &streamWriter{logger: logger, level: level}
	if logger != nil {
		logger.mu.Lock()
		logger.streams = append(logger.streams, stream)
		logger.mu.Unlock()
	}
	return stream
}

type streamWriter struct {
	logger *Logger
	level  Level
	mu     sync.Mutex
	buf    string
}

func (writer *streamWriter) Write(data []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	writer.buf += string(data)
	for {
		index := strings.IndexByte(writer.buf, '\n')
		if index < 0 {
			break
		}
		writer.logger.Log(writer.level, writer.buf[:index])
		writer.buf = writer.buf[index+1:]
	}
	return len(data), nil
}

func (writer *streamWriter) Flush() {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.buf != "" {
		writer.logger.Log(writer.level, writer.buf)
		writer.buf = ""
	}
}

func DefaultPath() string {
	directory, err := os.UserConfigDir()
	if err != nil {
		return filepath.Join(".", "MyApp", "logs", "myapp.log")
	}
	return filepath.Join(directory, "MyApp", "logs", "myapp.log")
}

func Export(source, destination string) error {
	if strings.TrimSpace(source) == "" || strings.TrimSpace(destination) == "" {
		return errors.New("日志导出路径不能为空")
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return err
	}
	return os.WriteFile(destination, data, 0600)
}
