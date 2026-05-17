package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/liuqiangssss/texas-holdem/server/internal/store"
	"github.com/liuqiangssss/texas-holdem/server/internal/table"
	"github.com/liuqiangssss/texas-holdem/server/internal/ws"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	hist := buildHandHistoryStore(ctx)
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		if err := hist.Close(closeCtx); err != nil {
			log.Printf("hand history store close: %v", err)
		}
	}()

	mgr := table.NewManager(ctx, hist)

	mux := http.NewServeMux()
	mux.Handle("/ws", &ws.Handler{Manager: mgr})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	srv := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		log.Println("shutdown signal received")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Printf("listening on %s", *addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

// buildHandHistoryStore returns a MongoStore when MONGO_URI is set and dial
// succeeds, otherwise a NoopStore. We never crash on connect failure: the
// product can run and persist results elsewhere later if Mongo is down.
func buildHandHistoryStore(ctx context.Context) store.HandHistoryStore {
	uri := os.Getenv("MONGO_URI")
	db := os.Getenv("MONGO_DB")
	if db == "" {
		db = "texas"
	}
	if uri == "" {
		log.Println("hand history: MONGO_URI not set, using noop store")
		return store.NewNoopStore()
	}
	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ms, err := store.NewMongoStore(dialCtx, uri, db)
	if err != nil {
		log.Printf("hand history: mongo dial failed (%v); falling back to noop", err)
		return store.NewNoopStore()
	}
	log.Printf("hand history: mongo connected (db=%s)", db)
	return ms
}
