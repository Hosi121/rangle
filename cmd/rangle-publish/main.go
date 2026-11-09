// rangle-publish checks an asset snapshot. Catalog promotion remains an explicit
// operator action; this command has no cloud credentials or write API.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/Hosi121/rangle/internal/publication"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "rangle-publish:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	var opts publication.Options
	var receiptPath string
	var timeout time.Duration
	f := flag.NewFlagSet("rangle-publish", flag.ContinueOnError)
	f.SetOutput(stderr)
	f.StringVar(&opts.CatalogPath, "catalog", "", "candidate catalog v1 JSON (outside root)")
	f.StringVar(&opts.ApprovalsPath, "approvals", "", "approval ledger v1 JSON (outside root)")
	f.StringVar(&opts.Root, "root", "", "exact local public asset/manifest root")
	f.StringVar(&opts.Origin, "origin", "", "fixed HTTPS media origin")
	f.StringVar(&opts.PreviousCatalogPath, "previous-catalog", "", "previous catalog body for explicit retention/rollback")
	f.StringVar(&opts.PreviousSHA256, "previous-sha256", "", "pinned previous catalog body SHA256")
	f.StringVar(&receiptPath, "receipt", "", "atomic receipt output (outside root; never overwritten)")
	f.BoolVar(&opts.Remote, "remote", false, "also GET every new public file from the fixed origin")
	f.DurationVar(&timeout, "timeout", 15*time.Minute, "finite total verification deadline (max 1h)")
	if err := f.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if f.NArg() != 0 || opts.CatalogPath == "" || opts.ApprovalsPath == "" || opts.Root == "" || opts.Origin == "" || receiptPath == "" || timeout <= 0 || timeout > time.Hour {
		return fmt.Errorf("require -catalog, -approvals, -root, -origin, -receipt, no positional arguments, and timeout in (0,1h]")
	}
	rootPath, err := filepath.EvalSymlinks(opts.Root)
	if err != nil {
		return err
	}
	rootPath, err = filepath.Abs(rootPath)
	if err != nil {
		return err
	}
	outputParent, err := filepath.EvalSymlinks(filepath.Dir(receiptPath))
	if err != nil {
		return err
	}
	outputParent, err = filepath.Abs(outputParent)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(rootPath, filepath.Join(outputParent, filepath.Base(receiptPath)))
	if err != nil || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
		return fmt.Errorf("receipt must be outside publication root")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	receipt, err := publication.Verify(ctx, opts)
	if err != nil {
		return err
	}
	if err := publication.WriteReceipt(receiptPath, receipt); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "verified scope=%s catalog_sha256=%s files=%d bytes=%d retained=%d receipt=%s\n", receipt.Scope, receipt.CatalogSHA256, receipt.CheckedFiles, receipt.CheckedBytes, len(receipt.Retained), receiptPath)
	return err
}
