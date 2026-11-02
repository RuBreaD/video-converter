package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"videojobs/internal/config"
	"videojobs/internal/httpapi"
	"videojobs/internal/jobs"
)

func main() {
	cfg, err := config.FromEnv()
	if err != nil {
		log.Fatal(err)
	}
	service, err := jobs.NewService(cfg)
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{Addr: cfg.Addr, Handler: httpapi.NewHandler(service, cfg.MaxUpload), ReadHeaderTimeout: 10 * time.Second}
	serverErrors := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()
	log.Printf("listening on %s", cfg.Addr)
	signalContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case <-signalContext.Done():
	case err := <-serverErrors:
		log.Printf("HTTP server: %v", err)
	}
	service.StopAccepting()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("HTTP shutdown: %v", err)
		if closeErr := server.Close(); closeErr != nil {
			log.Printf("HTTP close: %v", closeErr)
		}
	}
	service.Shutdown()
}
