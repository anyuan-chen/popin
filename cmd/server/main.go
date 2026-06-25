package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/popin/popin/auth"
	"github.com/popin/popin/config"
	"github.com/popin/popin/db"
	"github.com/popin/popin/server"
)

func main() {
	log.Println("Starting Popin video call server...")

	cfg := config.Load()

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	if err := db.Migrate(database); err != nil {
		log.Fatalf("Failed to migrate database: %v", err)
	}
	log.Println("Database migrated")

	authSvc := auth.NewService(database, cfg.SessionDuration)

	srv := server.New(cfg, authSvc)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("Received shutdown signal")
		cancel()
	}()

	if err := srv.Start(ctx); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
