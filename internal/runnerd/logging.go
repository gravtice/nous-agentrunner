package runnerd

import (
	"log"
	"os"
	"path/filepath"
	"time"
)

var runnerdLogFile *os.File

func setupLogging(cfg Config) {
	if runnerdLogFile != nil {
		return
	}
	path := filepath.Join(cfg.Paths.LogsDir, "runnerd.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	runnerdLogFile = f

	_, _ = f.WriteString("\n--- runnerd start " + time.Now().Format(time.RFC3339) + " ---\n")
	log.SetOutput(f)
}
