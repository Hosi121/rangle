// rangle-admin provisions the reserved administrator with trusted credentials.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/Hosi121/rangle/internal/community"
)

func run() error {
	project := flag.String("project", "", "Google Cloud project")
	database := flag.String("database", "", "Existing community database")
	flag.Parse()
	if *project == "" || *database == "" || flag.NArg() != 0 {
		return fmt.Errorf("set -project and -database; pass the password as one stdin line")
	}
	password, err := bufio.NewReader(io.LimitReader(os.Stdin, 514)).ReadString('\n')
	if err != nil && err != io.EOF {
		return fmt.Errorf("read password: %w", err)
	}
	password = strings.TrimSuffix(strings.TrimSuffix(password, "\n"), "\r")
	a, err := community.NewAdministrator(password)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	store, err := community.NewFirestoreStore(ctx, *project, *database)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.CreateAccount(ctx, a); err != nil {
		return fmt.Errorf("create administrator without replacing an existing account: %w", err)
	}
	slog.Info("administrator created", "username", a.User.ID)
	return nil
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	if err := run(); err != nil {
		slog.Error("administrator provisioning failed", "error", err)
		os.Exit(1)
	}
}
