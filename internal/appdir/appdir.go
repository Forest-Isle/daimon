package appdir

import (
	"os"
	"path/filepath"
)

const (
	DirName       = ".daimon"
	LegacyDirName = ".ironclaw"
	DBName        = "daimon.db"
	LegacyDBName  = "ironclaw.db"
)

// BaseDir returns the Daimon user data directory.
func BaseDir() string {
	return filepath.Join(homeDir(), DirName)
}

// LegacyBaseDir returns the legacy IronClaw user data directory.
func LegacyBaseDir() string {
	return filepath.Join(homeDir(), LegacyDirName)
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "."
	}
	return home
}
