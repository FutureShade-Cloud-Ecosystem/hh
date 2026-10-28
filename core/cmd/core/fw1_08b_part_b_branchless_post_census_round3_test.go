package main

// fw1_08b_part_b_branchless_post_census_round3_test.go is the red leg for S4's source census
// (FW1-08b part B round 3, item 1): a NEW branchless Go caller must FAIL the census.
//
// The leg plants branchless Go callers in a probe tree and asks the census to scan it. The census
// must name every planted caller: the draft written out at the call, the draft held in a local
// variable, the draft a helper returns, the draft behind a pointer, the draft whose branch
// field is set to an explicit empty string, and the draft a helper in ANOTHER package returns.
// A caller that names its branch must NOT be named.
//
// Nothing in the probe is compiled or run: the census reads the source. The probe tree is the shape
// a new caller arrives in, a package under core/internal with a call to the gl API.
//
// The leg is what keeps the census honest. Without it the census could quietly stop seeing a shape
// (a draft behind a pointer, a draft a helper returns) and a new branchless caller would land on
// the head office unseen.

import (
	"os"
	"path/filepath"
	"testing"
)

// fw1ProbeGoMod is the probe tree's module file. The census reads the module path to resolve a
// package's import path to its directory, so the probe needs one.
const fw1ProbeGoMod = "module github.com/FutureShade-Cloud-Ecosystem/hh/core\n\ngo 1.26\n"

// fw1ProbeBody is the probe package: a caller of the gl API in every shape a draft can arrive in.
const fw1ProbeBody = `package probe

import "context"

type ExternalJournal struct {
	Source         string
	IdempotencyKey string
	Memo           string
	BranchID       string
	Lines          []ExternalLine
}

type ExternalLine struct {
	AccountCode string
	DebitCents  int64
	CreditCents int64
}

type poster struct{}

func (p poster) PostExternal(ctx context.Context, in ExternalJournal) (string, error) { return "", nil }

func (p poster) PostExternalTx(ctx context.Context, tx any, in ExternalJournal) (string, error) {
	return "", nil
}

// A branchless caller: the draft written out at the call.
func postDirect(p poster) {
	_, _ = p.PostExternal(context.Background(), ExternalJournal{
		Source:         "ADJUSTMENT",
		IdempotencyKey: "probe-direct",
		Lines:          []ExternalLine{{AccountCode: "1000", DebitCents: 100}},
	})
}

// A branchless caller: the draft held in a local variable.
func postThroughAVariable(p poster) {
	in := ExternalJournal{
		Source:         "ADJUSTMENT",
		IdempotencyKey: "probe-variable",
	}
	_, _ = p.PostExternal(context.Background(), in)
}

// A branchless caller: the draft a helper returns.
func probeHelper() ExternalJournal {
	return ExternalJournal{
		Source:         "ADJUSTMENT",
		IdempotencyKey: "probe-helper",
	}
}

func postThroughAHelper(p poster) {
	_, _ = p.PostExternalTx(context.Background(), nil, probeHelper())
}

// A branchless caller: the draft behind a pointer.
func postThroughAPointer(p poster) {
	draft := &ExternalJournal{
		Source:         "ADJUSTMENT",
		IdempotencyKey: "probe-pointer",
	}
	_, _ = p.PostExternal(context.Background(), *draft)
}

// A branchless caller: the draft whose branch field is an explicit empty string.
// An empty branch string names no branch, so the census must name this caller
// exactly as it names a draft that leaves the field out.
func postWithAnEmptyBranch(p poster) {
	_, _ = p.PostExternal(context.Background(), ExternalJournal{
		Source:         "ADJUSTMENT",
		IdempotencyKey: "probe-empty-branch",
		BranchID:       "",
	})
}

// A caller that names its branch: the census must not name it.
func postWithABranch(p poster) {
	_, _ = p.PostExternal(context.Background(), ExternalJournal{
		Source:         "ADJUSTMENT",
		IdempotencyKey: "probe-with-branch",
		BranchID:       "5d1b3f6e-0000-4000-9000-000000000002",
	})
}
`

// fw1ProbeGLBody is the probe tree's gl package: the draft type the other packages name. The
// census resolves a draft's TYPE to its declaration, so the type has to live in a package of the
// scanned tree for a cross package helper to be followed.
const fw1ProbeGLBody = `package gl

type ExternalJournal struct {
	Source         string
	IdempotencyKey string
	Memo           string
	BranchID       string
	Lines          []ExternalLine
}

type ExternalLine struct {
	AccountCode string
	DebitCents  int64
	CreditCents int64
}
`

// fw1ProbeWarrantyBody is the probe tree's warranty package: a helper in ANOTHER package that
// returns a draft naming no branch.
const fw1ProbeWarrantyBody = `package warranty

import "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"

// ZzS4Draft returns a draft that names no branch.
func ZzS4Draft() gl.ExternalJournal {
	return gl.ExternalJournal{
		Source:         "ADJUSTMENT",
		IdempotencyKey: "probe-cross-package",
	}
}
`

// fw1ProbeLienBody is the probe tree's lien package: the caller that hands the other package's
// draft to a posting call.
const fw1ProbeLienBody = `package lien

import (
	"context"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/warranty"
)

type poster struct{}

func (p poster) PostExternal(ctx context.Context, in gl.ExternalJournal) (string, error) {
	return "", nil
}

// A branchless caller: the draft a helper in ANOTHER package returns.
func postThroughAnotherPackagesHelper(p poster) {
	_, _ = p.PostExternal(context.Background(), warranty.ZzS4Draft())
}
`

// TestFW1_08b_PartB_Round3_ANewBranchlessGoCallerFailsTheCensus plants branchless Go callers and
// proves the census names every one of them.
func TestFW1_08b_PartB_Round3_ANewBranchlessGoCallerFailsTheCensus(t *testing.T) {
	root := t.TempDir()
	probeDir := filepath.Join(root, "internal", "probe")
	if err := os.MkdirAll(probeDir, 0o755); err != nil {
		t.Fatalf("probe tree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(fw1ProbeGoMod), 0o644); err != nil {
		t.Fatalf("probe go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(probeDir, "probe.go"), []byte(fw1ProbeBody), 0o644); err != nil {
		t.Fatalf("probe body: %v", err)
	}
	glDir := filepath.Join(root, "internal", "gl")
	if err := os.MkdirAll(glDir, 0o755); err != nil {
		t.Fatalf("probe gl package: %v", err)
	}
	if err := os.WriteFile(filepath.Join(glDir, "gl.go"), []byte(fw1ProbeGLBody), 0o644); err != nil {
		t.Fatalf("probe gl body: %v", err)
	}
	warrantyDir := filepath.Join(root, "internal", "warranty")
	if err := os.MkdirAll(warrantyDir, 0o755); err != nil {
		t.Fatalf("probe warranty package: %v", err)
	}
	if err := os.WriteFile(filepath.Join(warrantyDir, "warranty.go"), []byte(fw1ProbeWarrantyBody), 0o644); err != nil {
		t.Fatalf("probe warranty body: %v", err)
	}
	lienDir := filepath.Join(root, "internal", "lien")
	if err := os.MkdirAll(lienDir, 0o755); err != nil {
		t.Fatalf("probe lien package: %v", err)
	}
	if err := os.WriteFile(filepath.Join(lienDir, "lien.go"), []byte(fw1ProbeLienBody), 0o644); err != nil {
		t.Fatalf("probe lien body: %v", err)
	}

	result, err := fw1RunBranchlessCensus(root)
	if err != nil {
		t.Fatalf("scanning the probe tree: %v", err)
	}
	for _, key := range []string{
		"core/internal/probe/probe.go|postDirect|PostExternal",
		"core/internal/probe/probe.go|postThroughAVariable|PostExternal",
		"core/internal/probe/probe.go|postThroughAHelper|PostExternalTx",
		"core/internal/probe/probe.go|postThroughAPointer|PostExternal",
		"core/internal/probe/probe.go|postWithAnEmptyBranch|PostExternal",
		"core/internal/lien/lien.go|postThroughAnotherPackagesHelper|PostExternal",
	} {
		if _, found := result.found[key]; !found {
			t.Errorf("the census did not name the planted branchless caller %s: a new branchless Go caller must fail the census", key)
		}
	}
	if _, found := result.found["core/internal/probe/probe.go|postWithABranch|PostExternal"]; found {
		t.Errorf("the census named a caller that names its own branch: the draft's branch is the branch")
	}
}
