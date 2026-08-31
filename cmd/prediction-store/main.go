// Command prediction-store serves the calibration ledger on :8313.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	predictionstore "github.com/kayushkin/prediction-store"
)

func main() {
	addr := os.Getenv("PREDICTION_STORE_ADDR")
	if addr == "" {
		addr = ":8313"
	}
	dataDir := os.Getenv("PREDICTION_STORE_DATA_DIR")

	store, err := predictionstore.Open(dataDir)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer store.Close()

	mux := http.NewServeMux()
	predictionstore.RegisterHandlers(mux, store)

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Printf("prediction-store listening on %s (data=%s)", addr, store.DataDir())
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	log.Println("shutting down…")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}
