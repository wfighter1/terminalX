// Command tx-relay is the terminalX relay: pairing, auth, routing, metadata
// and the hosted web console. Run with --admin-password (or TX_ADMIN_PASSWORD).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/wfighter1/terminalX/internal/relay"
	"github.com/wfighter1/terminalX/internal/relay/store"
	"github.com/wfighter1/terminalX/internal/webdist"
)

// version is stamped by the build (-ldflags "-X main.version=v0.1.0").
var version = "dev"

func main() {
	if err := run(); err != nil {
		slog.Error("tx-relay exited", "err", err)
		os.Exit(1)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func run() error {
	var (
		listen    = flag.String("listen", envOr("TX_LISTEN", ":8080"), "listen address")
		dataDir   = flag.String("data", envOr("TX_DATA", "./data"), "data directory (holds relay.db)")
		adminPW   = flag.String("admin-password", os.Getenv("TX_ADMIN_PASSWORD"), "console password (or TX_ADMIN_PASSWORD)")
		webDir    = flag.String("web-dir", os.Getenv("TX_WEB_DIR"), "directory with the built web console (default: embedded)")
		publicURL = flag.String("public-url", os.Getenv("TX_PUBLIC_URL"), "public base URL, e.g. https://tx.example.com (used in notifications)")
		origins   = flag.String("allow-origin", os.Getenv("TX_ALLOW_ORIGIN"), "extra allowed WebSocket origins for /ws/client, comma separated (e.g. localhost:5173)")
		logLevel  = flag.String("log-level", envOr("TX_LOG_LEVEL", "info"), "debug | info | warn | error")
	)
	flag.Parse()

	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(*logLevel)); err != nil {
		return fmt.Errorf("bad --log-level %q: %w", *logLevel, err)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
	slog.SetDefault(logger)

	if *adminPW == "" {
		return errors.New("--admin-password (or TX_ADMIN_PASSWORD) is required")
	}

	var webFS fs.FS
	if *webDir != "" {
		if st, err := os.Stat(*webDir); err != nil || !st.IsDir() {
			return fmt.Errorf("--web-dir %q is not a directory", *webDir)
		}
		webFS = os.DirFS(*webDir)
		logger.Info("serving web console from directory", "dir", *webDir)
	} else {
		webFS = webdist.FS()
	}

	st, err := store.Open(filepath.Join(*dataDir, "relay.db"))
	if err != nil {
		return err
	}
	defer st.Close()

	var allowed []string
	for _, o := range strings.Split(*origins, ",") {
		if o = strings.TrimSpace(o); o != "" {
			allowed = append(allowed, o)
		}
	}
	srv, err := relay.New(relay.Config{
		AdminPassword:  *adminPW,
		PublicURL:      *publicURL,
		WebFS:          webFS,
		AllowedOrigins: allowed,
		Logger:         logger,
	}, st)
	if err != nil {
		return err
	}
	srv.Start()
	defer srv.Close()

	hs := &http.Server{
		Addr:              *listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)
	go func() {
		logger.Info("tx-relay listening", "version", version, "addr", *listen, "data", *dataDir, "public_url", *publicURL)
		if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()
	select {
	case err := <-errc:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
		logger.Info("shutting down")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := hs.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}
