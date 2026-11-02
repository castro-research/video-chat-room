package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"kurz/server/internal/platform/config"
	"kurz/server/internal/platform/db"
	"kurz/server/internal/platform/objstore"
	"kurz/server/internal/server"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := db.Migrate(ctx, cfg.DatabaseURL); err != nil {
		return err
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	var opts []server.Option
	if cfg.S3.Enabled() {
		store, err := objstore.NewS3(ctx, objstore.S3Config{Endpoint: cfg.S3.Endpoint, PublicEndpoint: cfg.S3.PublicEndpoint,
			Bucket: cfg.S3.Bucket, AccessKey: cfg.S3.AccessKey, SecretKey: cfg.S3.SecretKey, Region: cfg.S3.Region})
		if err != nil {
			return err
		}
		opts = append(opts, server.WithObjectStore(store))
		slog.Info("attachments enabled", "endpoint", cfg.S3.Endpoint, "bucket", cfg.S3.Bucket)
	} else {
		slog.Info("attachments disabled (KURZ_S3_ENDPOINT not set)")
	}
	if cfg.Voice.Enabled() {
		slog.Info("voice enabled", "sfu", cfg.Voice.SFUURL)
	} else {
		slog.Info("voice disabled (KURZ_SECRET and KURZ_SFU_URL not set)")
	}
	app := server.NewApp(cfg, pool, opts...)
	workersCtx, stopWorkers := context.WithCancel(context.Background())
	workersDone := make(chan struct{})
	go func() { app.Run(workersCtx); close(workersDone) }()

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           app.Handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	slog.Info("listening", "addr", cfg.Addr)

	var serveErr error
	select {
	case serveErr = <-errc:
	case <-ctx.Done():
	}
	stop() // devolve os sinais ao padrão: um segundo Ctrl+C mata o processo durante o drain
	// 1) os workers param primeiro: o gateway fecha os WebSockets (1001) para o Shutdown não esperar por eles
	// e o relay termina a transação em curso; 2) o HTTP drena as requisições restantes.
	stopWorkers()
	<-workersDone
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return serveErr
	}
	return nil
}
