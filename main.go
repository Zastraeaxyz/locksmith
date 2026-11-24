package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"locksmith/internal/analytics"
	"locksmith/internal/bot"
	"locksmith/internal/config"
	"locksmith/internal/cryptobox"
	"locksmith/internal/feature"
	"locksmith/internal/store"
)

func main() {
	if err := run(); err != nil {
		slog.Error("locksmith failed", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer st.Close()

	box, err := cryptobox.LoadOrCreate(cfg.StoreKey, filepath.Join(filepath.Dir(cfg.DatabasePath), "locksmith.key"))
	if err != nil {
		return fmt.Errorf("loading store key: %w", err)
	}

	b, err := bot.New(cfg.Token, cfg.GuildID, log)
	if err != nil {
		return err
	}

	if tracker := analytics.New(analytics.Options{
		SiteID:   cfg.UmamiSiteID,
		Endpoint: cfg.UmamiEndpoint,
		Logger:   log,
	}); tracker != nil {
		defer tracker.Close()
		b.SetTracker(tracker)
		log.Info("anonymous analytics enabled", "endpoint", cfg.UmamiEndpoint)
	} else {
		log.Info("anonymous analytics disabled")
	}

	feat := feature.New(feature.Options{
		Store:          st,
		Box:            box,
		BaseURL:        cfg.BaseURL,
		Logger:         log,
		NotifyInterval: cfg.NotifyInterval,
	})
	feat.Register(b)

	go feat.NotifyLoop(ctx, b.Session)

	health := newHealthServer(cfg.Port, log)
	health.start()
	defer health.stop()

	log.Info("starting locksmith", "guild_id", cfg.GuildID, "database", cfg.DatabasePath)
	return b.Run(ctx)
}

type healthServer struct {
	srv *http.Server
	log *slog.Logger
}

func newHealthServer(port string, log *slog.Logger) *healthServer {
	mux := http.NewServeMux()
	ok := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}
	mux.HandleFunc("/health", ok)
	mux.HandleFunc("/", ok)
	return &healthServer{
		srv: &http.Server{
			Addr:              ":" + port,
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
		},
		log: log,
	}
}

func (h *healthServer) start() {
	go func() {
		if err := h.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			h.log.Warn("health server stopped", "err", err)
		}
	}()
}

func (h *healthServer) stop() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = h.srv.Shutdown(ctx)
}
