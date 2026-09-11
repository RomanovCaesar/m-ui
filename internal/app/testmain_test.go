package app

import (
	"os"
	"path/filepath"
	"testing"
)

// Frontend contract tests read the checked-in files directly. After moving
// the application package under internal/app, restore the repository root as
// their working directory so those checks keep testing the real web assets.
func TestMain(m *testing.M) {
	workingDir, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	repositoryRoot := filepath.Clean(filepath.Join(workingDir, "..", ".."))
	if err := os.Chdir(repositoryRoot); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}
