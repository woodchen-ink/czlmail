// Command gateway is the czlmail JMAP gateway: it connects users' IMAP, Gmail and
// Microsoft mailboxes and serves them to czlmail as one JMAP server.
//
//	gateway              run the server
//	gateway rotate-key   re-encrypt stored secrets (GATEWAY_OLD_MASTER_KEY → GATEWAY_MASTER_KEY)
//	gateway version      print the version
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/woodchen-ink/czlmail/gateway/initapp"
)

// version is injected at build time with -ldflags "-X main.version=2026.12.0".
var version = "dev"

func main() {
	log := initapp.NewLogger()

	cmd := ""
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	var err error
	switch cmd {
	case "":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		err = initapp.Serve(ctx, log, version)
	case "rotate-key":
		err = initapp.RotateKey(log)
	case "version":
		fmt.Println(version)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q (commands: rotate-key, version)\n", cmd)
		os.Exit(2)
	}
	if err != nil {
		log.Error("gateway stopped", "err", err)
		os.Exit(1)
	}
}
