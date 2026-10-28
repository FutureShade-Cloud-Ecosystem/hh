package main

// finance_reset_dump_dir_test.go pins the one thing about the reset's dump
// file that has to hold on every box: it is written on disk, under a
// directory the reset is given, and never on the shared memory tmpfs.
//
// The shared memory tmpfs is sized by the platform, not by the box: Docker's
// default is 64 MB and the world's decoded dump is larger than that, so a
// reset that wrote its dump there refused on a box that was big enough to
// serve the world.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFinanceResetDumpDirectory(t *testing.T) {
	// The default is on disk and never the shared memory tmpfs.
	def := financeResetDumpDirectory(func(string) string { return "" })
	if def != financeResetDumpDir {
		t.Fatalf("the default dump directory is %q, want %q", def, financeResetDumpDir)
	}
	if strings.HasPrefix(def, "/dev/shm") {
		t.Fatalf("the default dump directory is on the shared memory tmpfs: %q", def)
	}
	if !filepath.IsAbs(def) {
		t.Fatalf("the default dump directory is not an absolute path: %q", def)
	}

	// The environment names the directory when it names one.
	named := financeResetDumpDirectory(func(k string) string {
		if k == envFinanceResetDir {
			return "/srv/box/reset"
		}
		return ""
	})
	if named != "/srv/box/reset" {
		t.Fatalf("the named dump directory is %q, want the one the environment named", named)
	}

	// A blank name is the default, not the current directory: a relative
	// path would land the world's data wherever the boot happened to be.
	blank := financeResetDumpDirectory(func(k string) string {
		if k == envFinanceResetDir {
			return "   "
		}
		return ""
	})
	if blank != financeResetDumpDir {
		t.Fatalf("a blank dump directory is %q, want the default %q", blank, financeResetDumpDir)
	}
}

func TestFinanceResetWriteDump(t *testing.T) {
	dir := t.TempDir()
	dump := []byte("the world's dump bytes")

	path, cleanup, err := financeResetWriteDump(dir, dump)
	if err != nil {
		t.Fatalf("writing the dump: %v", err)
	}
	defer cleanup()
	if filepath.Dir(path) != dir {
		t.Fatalf("the dump file is %q, want it under %q", path, dir)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the dump file: %v", err)
	}
	if string(got) != string(dump) {
		t.Fatalf("the dump file holds %q, want the dump", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the dump file's mode: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0600 {
		t.Fatalf("the dump file's mode is %o, want 600", mode)
	}

	// A directory that cannot hold the file is a refusal, and the file is
	// not left behind.
	blocked := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0600); err != nil {
		t.Fatalf("the blocked parent: %v", err)
	}
	if _, _, err := financeResetWriteDump(filepath.Join(blocked, "reset"), dump); err == nil {
		t.Fatalf("the dump was written into a directory that cannot be made")
	}

	// The remover takes the file away.
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the dump file is still there after the remover ran: %v", err)
	}
}
