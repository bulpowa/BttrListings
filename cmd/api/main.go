package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"OlxScraper/internal/api/router"
	"OlxScraper/internal/auth"
	sqlcDb "OlxScraper/internal/db"
	"OlxScraper/internal/llm"
	"OlxScraper/internal/repository"
	"OlxScraper/internal/scraper"
	"OlxScraper/internal/service"
	"OlxScraper/internal/worker"

	"github.com/caarlos0/env/v6"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

type Config struct {
	DatabaseURL string `env:"DATABASE_URL,required"`
	JWTSecret   string `env:"JWT_SECRET,required"`
	OllamaHost  string `env:"OLLAMA_HOST,required"`
	OllamaModel string `env:"OLLAMA_MODEL" envDefault:"gemma4:27b"`
	Port        string `env:"PORT" envDefault:"8080"`
	ScraperURLs string `env:"SCRAPER_URLS" envDefault:""`
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

	// sqlc queries use database/sql interface via pgx stdlib adapter.
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	queries := sqlcDb.New(sqlDB)

	// Set up repositories and services.
	repo := repository.New(queries, pool)
	jwtService := auth.NewJWTService(cfg.JWTSecret)

	// Set up River workers.
	workers := river.NewWorkers()
	river.AddWorker(workers, worker.NewEnrichListingWorker(repo, ollamaClient))

	riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 2},
		},
		Workers: workers,
	})
	if err != nil {
		log.Fatalf("river client: %v", err)
	}

	// EnqueueFn wires River into the service layer without importing River there.
	enqueueFn := func(ctx context.Context, listingID int64) error {
		_, err := riverClient.Insert(ctx, worker.EnrichListingArgs{ListingID: listingID}, &river.InsertOpts{
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

	// Start Echo HTTP server.
	e := router.New(svc, jwtService, ollamaClient)

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

