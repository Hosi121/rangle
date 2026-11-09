package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIRequiresFiniteExplicitInputs(t *testing.T) {
	for _, args := range [][]string{nil, {"-timeout", "0"}, {"-timeout", "2h"}, {"unknown"}} {
		if err := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
			t.Errorf("accepted arguments %v", args)
		}
	}
	if err := run(context.Background(), []string{"-h"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
}

func TestCLIOutputAndFailureAreAtomic(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "public")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	catalog := filepath.Join(dir, "catalog.json")
	ledger := filepath.Join(dir, "approvals.json")
	receipt := filepath.Join(dir, "receipt.json")
	if err := os.WriteFile(catalog, []byte(`{"version":1,"clips":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ledger, []byte(`{"version":1,"clips":[],"retained":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"-catalog", catalog, "-approvals", ledger, "-root", root, "-origin", "https://media.example.com", "-receipt", receipt}
	var out bytes.Buffer
	if err := run(context.Background(), args, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "verified scope=local catalog_sha256=") {
		t.Fatalf("missing success summary %s", out.String())
	}
	before, err := os.ReadFile(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "research.npz"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run(context.Background(), args, &out, &bytes.Buffer{}); err == nil {
		t.Fatal("unexpected publication success")
	}
	if out.Len() != 0 {
		t.Fatal("failure printed success")
	}
	after, _ := os.ReadFile(receipt)
	if !bytes.Equal(before, after) {
		t.Fatal("failure overwrote earlier receipt")
	}
	args[len(args)-1] = filepath.Join(root, "receipt.json")
	if err := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatal("allowed receipt contamination of public root")
	}
	if _, err := os.Stat(filepath.Join(root, "receipt.json")); !os.IsNotExist(err) {
		t.Fatal("wrote receipt to public root")
	}
}
