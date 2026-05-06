package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/nrhtr/spruce/internal/config"
	"github.com/nrhtr/spruce/internal/db"
	dbgen "github.com/nrhtr/spruce/internal/db/generated"
	"github.com/nrhtr/spruce/internal/evaluator"
	"github.com/nrhtr/spruce/internal/notifier"
	"github.com/nrhtr/spruce/internal/platform"
	"github.com/nrhtr/spruce/internal/platform/buyee"
	"github.com/nrhtr/spruce/internal/platform/ebay"
	"github.com/nrhtr/spruce/internal/platform/facebook"
	"github.com/nrhtr/spruce/internal/platform/gumtree"
	"github.com/nrhtr/spruce/internal/scanner"
	"github.com/nrhtr/spruce/internal/web/handlers"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	if err := run(log); err != nil {
		log.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := config.Load()

	sqlDB, err := db.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer sqlDB.Close()

	if err := db.Migrate(sqlDB); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	log.Info("database ready", "path", cfg.DBPath)

	queries := dbgen.New(sqlDB)

	platforms := []platform.Platform{
		ebay.New(cfg.EbayClientID, cfg.EbayClientSecret, cfg.EbayMarketplace),
		buyee.New(),
		gumtree.New(),
		facebook.New(),
	}

	eval := evaluator.New(cfg, log)
	scnr := scanner.New(sqlDB, queries, platforms, eval, log)

	loc, err := time.LoadLocation(cfg.DigestTZ)
	if err != nil {
		log.Warn("invalid timezone, using UTC", "tz", cfg.DigestTZ, "error", err)
		loc = time.UTC
	}

	notif := notifier.New(queries, cfg, log)

	// Load email templates into the notifier.
	h, err := handlers.New(sqlDB, queries, scnr, log, loc)
	if err != nil {
		return fmt.Errorf("parse templates: %w", err)
	}
	notif.SetTemplates(h.EmailTemplates())

	// Cron jobs.
	c := cron.New(cron.WithLocation(loc))

	c.AddFunc(cfg.ScanCron, func() {
		log.Info("cron: starting scan")
		scnr.RunAll(ctx)
	})

	c.AddFunc(fmt.Sprintf("0 %d * * *", cfg.DigestHour), func() {
		log.Info("cron: sending digest")
		if err := notif.SendDigest(ctx); err != nil {
			log.Error("digest", "error", err)
		}
	})

	c.AddFunc("*/15 * * * *", func() {
		if err := notif.CheckUrgent(ctx); err != nil {
			log.Error("urgent check", "error", err)
		}
	})

	c.Start()
	defer c.Stop()

	// HTTP server.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", h.Dashboard)
	mux.HandleFunc("GET /searches", h.ListSearches)
	mux.HandleFunc("GET /searches/new", h.NewSearchForm)
	mux.HandleFunc("GET /searches/partial", h.SearchesPartial)
	mux.HandleFunc("POST /searches", h.CreateSearch)
	mux.HandleFunc("GET /searches/{id}/edit", h.EditSearchForm)
	mux.HandleFunc("POST /searches/{id}", h.UpdateSearch)
	mux.HandleFunc("POST /searches/{id}/scan", h.TriggerScan)
	mux.HandleFunc("POST /searches/{id}/delete", h.DeleteSearch)
	mux.HandleFunc("GET /listings", h.ListListings)
	mux.HandleFunc("GET /listings/{id}", h.GetListing)
	mux.HandleFunc("POST /listings/{id}/mute", h.MuteListing)
	mux.HandleFunc("POST /listings/{id}/unmute", h.UnmuteListing)
	mux.HandleFunc("POST /listings/{id}/bids", h.CreateBid)
	mux.HandleFunc("POST /bids/{id}", h.UpdateBid)
	mux.HandleFunc("GET /bids", h.ListBids)
	mux.HandleFunc("GET /scan-runs", h.ListScanRuns)
	mux.HandleFunc("GET /scan-runs/partial", h.ScanRunsPartial)
	mux.HandleFunc("GET /images/{hash}", h.ProxyImage)

	if cfg.DevMode {
		mux.HandleFunc("GET /debug/email/digest", h.DebugEmailDigest)
		mux.HandleFunc("GET /debug/email/urgent", h.DebugEmailUrgent)
		log.Info("dev mode enabled", "routes", []string{"/debug/email/digest", "/debug/email/urgent"})
	}

	srv := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	go func() {
		log.Info("listening", "addr", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("server", "error", err)
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")

	shutCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return srv.Shutdown(shutCtx)
}
