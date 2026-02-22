package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolveMigrationsDir_FromRepoRoot(t *testing.T) {
	cmdDir, repoRoot := testPaths(t)
	_ = cmdDir
	withWorkingDir(t, repoRoot)

	dir, err := resolveMigrationsDir()
	if err != nil {
		t.Fatalf("resolve migrations dir from repo root: %v", err)
	}

	assertMigrationsDir(t, dir)
}

func TestResolveMigrationsDir_FromSeedAdminDir(t *testing.T) {
	cmdDir, _ := testPaths(t)
	withWorkingDir(t, cmdDir)

	dir, err := resolveMigrationsDir()
	if err != nil {
		t.Fatalf("resolve migrations dir from cmd/seedAdmin: %v", err)
	}

	assertMigrationsDir(t, dir)
}

func testPaths(t *testing.T) (cmdDir string, repoRoot string) {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime caller unavailable")
	}
	cmdDir = filepath.Dir(file)
	repoRoot = filepath.Clean(filepath.Join(cmdDir, "..", ".."))
	return cmdDir, repoRoot
}

func withWorkingDir(t *testing.T, dir string) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir to %s: %v", dir, err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(wd)
	})
}

func assertMigrationsDir(t *testing.T, dir string) {
	t.Helper()
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat migrations dir: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected directory, got file: %s", dir)
	}
	if !strings.HasSuffix(filepath.ToSlash(dir), "infrastructure/sqlite/migrations") {
		t.Fatalf("unexpected migrations path: %s", dir)
	}
}
