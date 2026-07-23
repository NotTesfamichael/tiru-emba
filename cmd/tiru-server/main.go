// Command tiru-server is the relay server for tiru-emba's cross-network
// mode: account registration/login now, with presence, message relaying,
// and organizations following in later phases. Unlike the LAN client, this
// is meant to run continuously on a machine with a public address.
package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/NotTesfamichael/tiru-emba/internal/relay"
)

func main() {
	addr := flag.String("addr", ":8443", "address to listen on")
	dbURL := flag.String("db", "", `PostgreSQL connection string, e.g. "postgres://user:pass@host:5432/dbname" (required)`)
	certFile := flag.String("tls-cert", "", "path to a TLS certificate; without it the server runs WITHOUT TLS (dev/local only)")
	keyFile := flag.String("tls-key", "", "path to the TLS certificate's private key (required alongside --tls-cert)")
	flag.Parse()

	if *dbURL == "" {
		fmt.Fprintln(os.Stderr, "error: --db is required, e.g. --db=postgres://user@localhost:5432/tiru_emba")
		os.Exit(1)
	}
	if (*certFile == "") != (*keyFile == "") {
		fmt.Fprintln(os.Stderr, "error: --tls-cert and --tls-key must be given together")
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store, err := relay.NewPGStore(ctx, *dbURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	defer store.Close()

	if err := store.Migrate(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	var tlsConfig *tls.Config
	if *certFile != "" {
		cert, err := tls.LoadX509KeyPair(*certFile, *keyFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error: load TLS certificate:", err)
			os.Exit(1)
		}
		tlsConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
	} else {
		fmt.Fprintln(os.Stderr, "warning: no --tls-cert given -- running WITHOUT TLS. Passwords and session tokens will cross the network in plaintext. Do not use this over a public network.")
	}

	auth := relay.NewAuth(store)
	orgs := relay.NewOrgs(store)
	srv, err := relay.NewServer(*addr, auth, orgs, tlsConfig)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Println("tiru-server listening on", srv.Addr())

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	if err := srv.Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
