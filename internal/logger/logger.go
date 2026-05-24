package logger

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/rs/zerolog"
)

var Log zerolog.Logger

func Setup(logDir string) error {
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}
	logPath := filepath.Join(logDir, "app.log")

	// Rotating file writer
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}

	// Multi-writer: console + file
	consoleWriter := zerolog.ConsoleWriter{
		Out:        os.Stdout,
		TimeFormat: "2006-01-02 15:04:05",
	}
	multi := zerolog.MultiLevelWriter(consoleWriter, fileWriter{file: file})

	zerolog.TimeFieldFormat = "2006-01-02 15:04:05"
	Log = zerolog.New(multi).With().Timestamp().Logger()

	return nil
}

type fileWriter struct {
	file *os.File
}

func (w fileWriter) Write(p []byte) (int, error) {
	return w.file.Write(p)
}

func (w fileWriter) WriteLevel(level zerolog.Level, p []byte) (int, error) {
	return w.file.Write(p)
}

// Rotate closes current file and opens new one
func Rotate(logDir string) error {
	logPath := filepath.Join(logDir, "app.log")
	ts := time.Now().Format("20060102_150405")
	archivePath := filepath.Join(logDir, fmt.Sprintf("app_%s.log", ts))
	if err := os.Rename(logPath, archivePath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	consoleWriter := zerolog.ConsoleWriter{
		Out:        os.Stdout,
		TimeFormat: "2006-01-02 15:04:05",
	}
	multi := zerolog.MultiLevelWriter(consoleWriter, fileWriter{file: file})
	Log = zerolog.New(multi).With().Timestamp().Logger()
	return nil
}

// Ensure io.Closer for cleanup
func Close() {
	Log.Info().Msg("logger closed")
}

func init() {
	Log = zerolog.New(io.Discard).With().Timestamp().Logger()
}

