package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/lelag/gonote/internal/cli"
	"github.com/lelag/gonote/internal/store"
)

// version is set by the build with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	s, err := store.Default()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Impossible d’ouvrir les dossiers de configuration.")
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	a := cli.New(os.Stdin, os.Stdout, os.Stderr, s)
	a.Version = version
	err = a.Command().ExecuteContext(ctx)
	closeErr := a.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "  ✕ "+cli.FriendlyError(err))
		os.Exit(1)
	}
}
