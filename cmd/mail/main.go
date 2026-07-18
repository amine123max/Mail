package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/amine123max/Mail/internal/auth"
	"github.com/amine123max/Mail/internal/config"
	"github.com/amine123max/Mail/internal/httpapi"
	"github.com/amine123max/Mail/internal/mailservice"
	"github.com/amine123max/Mail/internal/secure"
	"github.com/amine123max/Mail/internal/store"
	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load()
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	box, err := secure.New(cfg.DataDir, cfg.EncryptionKey, cfg.Production)
	if err != nil {
		log.Fatal(err)
	}
	storage, err := store.Open(cfg.DataDir, box)
	if err != nil {
		log.Fatal(err)
	}
	defer storage.Close()
	if err := storage.CleanupExpired(context.Background()); err != nil {
		log.Printf("清理过期会话失败：%v", err)
	}
	go cleanupLoop(storage)

	authentication := auth.New(cfg, storage)
	mail := mailservice.New(cfg, storage)
	api := httpapi.New(cfg, storage, authentication, mail)
	server := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Handler:           api.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		log.Printf("Mail Go API 已启动：http://%s", server.Addr)
		log.Printf("SQLite 存储已就绪：%s", storage.Path)
		if setup, _ := storage.IsSetupRequired(context.Background()); setup {
			log.Print("等待首次部署管理员初始化")
		}
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("Mail Go API 启动失败：%v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("Mail Go API 关闭失败：%v", err)
	}
}

func cleanupLoop(storage *store.Store) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		if err := storage.CleanupExpired(context.Background()); err != nil {
			log.Printf("清理过期会话失败：%v", err)
		}
	}
}
