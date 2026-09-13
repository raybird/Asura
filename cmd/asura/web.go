package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"asura/internal/web"
)

// handleWeb 處理 asura web CLI 子命令
func handleWeb(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("web", flag.ContinueOnError)
	fs.SetOutput(stderr)

	listenAddr := fs.String("listen", "127.0.0.1:8080", "address to listen on (default: loopback 127.0.0.1:8080)")
	originFlag := fs.String("origin", "", "allowed HTTP origin (default: http://<listen-addr>)")
	keyFlag := fs.String("key", "", "access key for web authentication (or ASURA_WEB_KEY env)")
	sockPath := fs.String("socket", DefaultSocketPath(), "path to daemon unix socket")

	if err := fs.Parse(args); err != nil {
		return 1
	}

	key := *keyFlag
	if key == "" {
		key = os.Getenv("ASURA_WEB_KEY")
	}
	if key == "" {
		fmt.Fprintln(stderr, "error: access key is required (specify via -key or ASURA_WEB_KEY environment variable)")
		return 1
	}

	origin := *originFlag
	if origin == "" {
		origin = "http://" + *listenAddr
	}

	serverHandler, err := web.NewServer(web.Config{
		Key:        key,
		Origin:     origin,
		SocketPath: *sockPath,
	})
	if err != nil {
		fmt.Fprintf(stderr, "error: failed to initialize web server: %v\n", err)
		return 1
	}

	httpServer := &http.Server{
		Addr:         *listenAddr,
		Handler:      serverHandler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	l, err := net.Listen("tcp", *listenAddr)
	if err != nil {
		fmt.Fprintf(stderr, "error: failed to listen on %s: %v\n", *listenAddr, err)
		return 1
	}

	fmt.Fprintf(stdout, "Asura web console listening on http://%s (origin: %s)\n", l.Addr().String(), origin)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	errChan := make(chan error, 1)
	go func() {
		if err := httpServer.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errChan <- err
		}
	}()

	select {
	case sig := <-sigChan:
		fmt.Fprintf(stdout, "\nReceived signal %s, shutting down web server...\n", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(ctx); err != nil {
			fmt.Fprintf(stderr, "error during shutdown: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "Web server stopped gracefully.")
		return 0
	case err := <-errChan:
		fmt.Fprintf(stderr, "error: web server failure: %v\n", err)
		return 1
	}
}
