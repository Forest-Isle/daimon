package evolution

import (
	"os"
	"path/filepath"
	"time"
)

// CleanupTrajectories removes trajectory files older than the retention period.
// Files are identified by their date-stamped filename (YYYY-MM-DD.jsonl).
func CleanupTrajectories(dir string, retention time.Duration) (removed int, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	cutoff := time.Now().Add(-retention)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if filepath.Ext(name) != ".jsonl" {
			continue
		}

		// Parse date from filename: YYYY-MM-DD.jsonl
		dateStr := name[:len(name)-len(".jsonl")]
		fileDate, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			continue
		}

		if fileDate.Before(cutoff) {
			path := filepath.Join(dir, name)
			if err := os.Remove(path); err == nil {
				removed++
			}
		}
	}

	return removed, nil
}

// CompactTrajectories removes trajectory files older than detailDays.
func CompactTrajectories(dir string, detailDays int) error {
	_, err := CleanupTrajectories(dir, time.Duration(detailDays*24)*time.Hour)
	return err
}
