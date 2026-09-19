// Command server runs the cosmological redshift / Hubble-distance
// computation service.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"cosmocalc/internal/api"
	"cosmocalc/internal/store"
)

func main() {
	logger := log.New(os.Stdout, "cosmocalc ", log.LstdFlags|log.LUTC)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	var st store.Store
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		logger.Println("DATABASE_URL not set: falling back to in-memory store (history is not persisted)")
		st = store.NewMemStore()
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		pg, err := store.NewPostgresStore(ctx, dsn)
		cancel()
		if err != nil {
			logger.Fatalf("connect to database: %v", err)
		}
		logger.Println("connected to PostgreSQL")
		st = pg
	}
	defer st.Close()

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           api.NewServer(st),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		logger.Printf("listening on :%s", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatalf("http server: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	logger.Println("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}
