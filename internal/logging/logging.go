// Package logging redirige la stdlib log vers un fichier journalier en plus
// de stderr (journal systemd), avec purge des fichiers de plus de 30 jours.
// Même système que le LogService de l'appli Flutter du head unit.
package logging

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	defaultDir    = "/data/.local/share/rover-mems/logs"
	envDir        = "ROVER_LOG_DIR"
	retentionDays = 30
)

// Setup ajoute un fichier de log journalier comme sortie de la stdlib log,
// en conservant stderr (journal systemd). En cas d'échec (dossier non
// créable), les logs restent sur stderr uniquement.
func Setup() {
	dir := os.Getenv(envDir)
	if dir == "" {
		dir = defaultDir
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("logging: impossible de créer %s: %v — logs fichier désactivés", dir, err)
		return
	}
	log.SetOutput(io.MultiWriter(os.Stderr, &dailyFileWriter{dir: dir}))
	log.Printf("logging: logs fichier dans %s", dir)
	go deleteOldLogs(dir)
}

// dailyFileWriter écrit dans app_YYYY-MM-DD.log et rouvre le fichier au
// changement de date (le service tourne en continu).
type dailyFileWriter struct {
	dir  string
	mu   sync.Mutex
	day  string
	file *os.File
}

func (w *dailyFileWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	day := time.Now().Format("2006-01-02")
	if w.file == nil || day != w.day {
		if w.file != nil {
			w.file.Close()
		}
		f, err := os.OpenFile(
			filepath.Join(w.dir, fmt.Sprintf("app_%s.log", day)),
			os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			// stderr reste servi par le MultiWriter ; ne pas casser log.Printf.
			return len(p), nil
		}
		w.file, w.day = f, day
	}
	if _, err := w.file.Write(p); err != nil {
		return len(p), nil
	}
	return len(p), nil
}

func deleteOldLogs(dir string) {
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || e.IsDir() {
			continue
		}
		if info.ModTime().Before(cutoff) {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}
