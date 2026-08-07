package logger

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/rs/zerolog"
	zlog "github.com/rs/zerolog/log"
)

var Log zerolog.Logger

func Setup(logDir string) error {
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}
	logPath := filepath.Join(logDir, "app.log")

	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}

	zerolog.TimeFieldFormat = "2006-01-02 15:04:05"
	fw := fileWriter{file: file}
	l := zerolog.New(fw).With().Timestamp().Logger()
	Log = l
	zlog.Logger = l // replace global logger so all packages' log.Info/Warn/Error go to file

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
	fw := fileWriter{file: file}
	l := zerolog.New(fw).With().Timestamp().Logger()
	Log = l
	zlog.Logger = l
	return nil
}

func Close() {
	Log.Info().Msg("logger closed")
}

func init() {
	Log = zerolog.New(io.Discard).With().Timestamp().Logger()
}
