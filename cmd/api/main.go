package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"OlxScraper/internal/alert"
	"OlxScraper/internal/api/router"
	"OlxScraper/internal/auth"
	"OlxScraper/internal/llm"
	"OlxScraper/internal/repository"
	"OlxScraper/internal/scraper"
	"OlxScraper/internal/service"
	"OlxScraper/internal/worker"

	"github.com/caarlos0/env/v6"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"riverqueue.com/riverui"
)

type Config struct {
	DatabaseURL     string `env:"DATABASE_URL,required"`
	JWTSecret       string `env:"JWT_SECRET,required"`
	OllamaHost      string `env:"OLLAMA_HOST" envDefault:"http://localhost:11434"`
	OllamaModel     string `env:"OLLAMA_MODEL" envDefault:"gemma4:27b"`
	Port            string `env:"PORT" envDefault:"8080"`
	ScraperURLs     string `env:"SCRAPER_URLS" envDefault:""`
	AlertWebhookURL string `env:"ALERT_WEBHOOK_URL" envDefault:""`
}

// defaultComponentSeeds is the baseline set of components to keep prices fresh for.
// Add entries here to bootstrap the component price DB on first run.
var defaultComponentSeeds = []worker.ComponentSeed{
	{Name: "RTX 4060", Category: "gpu"},
	{Name: "RTX 4060 Ti", Category: "gpu"},
	{Name: "RTX 4070", Category: "gpu"},
	{Name: "RTX 3060", Category: "gpu"},
	{Name: "RTX 3070", Category: "gpu"},
	{Name: "RTX 3080", Category: "gpu"},
	{Name: "iPhone 13 128GB", Category: "phone"},
	{Name: "iPhone 14 128GB", Category: "phone"},
	{Name: "iPhone 15 128GB", Category: "phone"},
	{Name: "MacBook Air M1", Category: "laptop"},
	{Name: "MacBook Air M2", Category: "laptop"},
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := godotenv.Load(); err != nil {
		log.Printf("WARN: unable to load .env file: %v", err)
	}

	cfg := Config{}
	if err := env.Parse(&cfg); err != nil {
		log.Fatalf("unable to parse environment variables: %v", err)
	}

	// Connect to Postgres.
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("unable to connect to database: %v", err)
	}
	defer pool.Close()

	// Run application migrations.
	if err := runMigrations(cfg.DatabaseURL); err != nil {
		log.Fatalf("migration failed: %v", err)
	}

	// Run River internal migrations (creates river_job, river_queue, etc.).
	riverMigrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		log.Fatalf("river migrator init: %v", err)
	}
	if _, err := riverMigrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		log.Fatalf("river migration failed: %v", err)
	}

	// Create Ollama client and do a non-blocking health check.
	ollamaClient := llm.NewOllamaClient(cfg.OllamaHost, cfg.OllamaModel)
	pingCtx, pingCancel := context.WithTimeout(ctx, 5*time.Second)
	if err := ollamaClient.Ping(pingCtx); err != nil {
		log.Printf("WARN: Ollama at %s not reachable: %v — jobs will retry when it comes up", cfg.OllamaHost, err)
	} else {
		log.Printf("Ollama at %s is reachable (model: %s)", cfg.OllamaHost, cfg.OllamaModel)
	}
	pingCancel()

	// Set up repositories and services.
	repo := repository.New(pool)
	jwtService := auth.NewJWTService(cfg.JWTSecret)

	// Set up River workers.
	notifier := alert.New(cfg.AlertWebhookURL)
	workers := river.NewWorkers()
	enrichWorker := worker.NewEnrichListingWorker(repo, ollamaClient, notifier)
	river.AddWorker(workers, enrichWorker)
	river.AddWorker(workers, worker.NewScrapeComponentPriceWorker(repo))

	// insertComponentJob is defined before the River client so RefreshStaleComponentPricesWorker
	// can close over riverClient — by the time jobs run, riverClient will be set.
	var riverClient *river.Client[pgx.Tx]
	insertComponentJob := func(ctx context.Context, args worker.ScrapeComponentPriceArgs) error {
		_, err := riverClient.Insert(ctx, args, &river.InsertOpts{
			UniqueOpts: river.UniqueOpts{ByArgs: true},
		})
		return err
	}
	river.AddWorker(workers, worker.NewRefreshStaleComponentPricesWorker(
		repo, insertComponentJob, defaultComponentSeeds, 6*time.Hour,
	))

	// Wire component job insertion into the enrichment worker now that we have the closure.
	enrichWorker.WithInsertComponentJobFn(insertComponentJob)

	riverClient, err = river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues: map[string]river.QueueConfig{
			// Enrichment is LLM-bound: 2 workers matches typical local LLM concurrency.
			worker.QueueEnrich: {MaxWorkers: 2},
			// Component scraping is I/O-bound (HTTP): more parallelism is fine.
			river.QueueDefault: {MaxWorkers: 3},
		},
		Workers: workers,
		PeriodicJobs: []*river.PeriodicJob{
			river.NewPeriodicJob(
				river.PeriodicInterval(6*time.Hour),
				func() (river.JobArgs, *river.InsertOpts) {
					return worker.RefreshStaleComponentPricesArgs{}, nil
				},
				&river.PeriodicJobOpts{RunOnStart: true},
			),
		},
	})
	if err != nil {
		log.Fatalf("river client: %v", err)
	}

	// EnqueueFn wires River into the service layer without importing River there.
	enqueueFn := func(ctx context.Context, listingID int64) error {
		_, err := riverClient.Insert(ctx, worker.EnrichListingArgs{ListingID: listingID}, &river.InsertOpts{
			Queue:      worker.QueueEnrich,
			UniqueOpts: river.UniqueOpts{ByArgs: true},
		})
		return err
	}

	svc := service.New(repo, jwtService, enqueueFn)

	// Start River workers.
	if err := riverClient.Start(ctx); err != nil {
		log.Fatalf("river start: %v", err)
	}

	// Start scraper goroutine if URLs are configured.
	if cfg.ScraperURLs != "" {
		sc := scraper.New(parseURLs(cfg.ScraperURLs), repo, riverClient, pool)
		go sc.Run(ctx)
	} else {
		log.Println("WARN: SCRAPER_URLS not set — scraper disabled")
	}

	// Set up River UI dashboard.
	riverUI, err := riverui.NewHandler(&riverui.HandlerOpts{
		Endpoints: riverui.NewEndpoints(riverClient, nil),
		Logger:    slog.Default(),
		Prefix:    "/riverui",
	})
	if err != nil {
		log.Fatalf("river ui: %v", err)
	}
	if err := riverUI.Start(ctx); err != nil {
		log.Fatalf("river ui start: %v", err)
	}

	// Start Echo HTTP server.
	e := router.New(svc, jwtService, ollamaClient, riverUI)

	go func() {
		log.Printf("starting server on :%s", cfg.Port)
		if err := e.Start(":" + cfg.Port); err != nil && err != http.ErrServerClosed {
			log.Printf("echo error: %v", err)
		}
	}()

	// Block until SIGTERM / Ctrl-C.
	<-ctx.Done()
	log.Println("shutdown signal received")

	// Graceful HTTP shutdown.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := e.Shutdown(shutdownCtx); err != nil {
		log.Printf("echo shutdown: %v", err)
	}

	// Graceful River shutdown — waits for in-progress jobs to finish.
	riverStopCtx, riverStopCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer riverStopCancel()
	if err := riverClient.Stop(riverStopCtx); err != nil {
		log.Printf("river stop: %v", err)
	}
}

func runMigrations(databaseURL string) error {
	// golang-migrate pgx/v5 driver needs "pgx5://" scheme.
	migrateURL := strings.Replace(databaseURL, "postgres://", "pgx5://", 1)
	migrateURL = strings.Replace(migrateURL, "postgresql://", "pgx5://", 1)

	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	migrationsPath := "file://" + filepath.ToSlash(filepath.Join(wd, "internal/db/migrations"))

	m, err := migrate.New(migrationsPath, migrateURL)
	if err != nil {
		return err
	}
	defer m.Close()

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return err
	}
	return nil
}

func parseURLs(s string) []string {
	var urls []string
	for _, u := range strings.Split(s, ",") {
		u = strings.TrimSpace(u)
		if u != "" {
			urls = append(urls, u)
		}
	}
	return urls
}

