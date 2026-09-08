package main

import (
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/vay1314/CtYun-Keeper/internal/security"
	"github.com/vay1314/CtYun-Keeper/internal/service"
	"github.com/vay1314/CtYun-Keeper/internal/storage"
	webapp "github.com/vay1314/CtYun-Keeper/internal/web"
)

var version = "dev"

func env(k, d string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return d
}
func main() {
	dataDir := env("CTYUN_DATA_DIR", "./data")
	if e := os.MkdirAll(dataDir, 0750); e != nil {
		log.Fatal(e)
	}
	store, e := storage.Open(filepath.Join(dataDir, "ctyun-keeper.db"))
	if e != nil {
		log.Fatal(e)
	}
	defer store.Close()
	credentialKey, e := security.LoadOrCreateKey(filepath.Join(dataDir, ".credential_key"), 32)
	if e != nil {
		log.Fatal(e)
	}
	sessionKey, e := security.LoadOrCreateKey(filepath.Join(dataDir, ".web_session_key"), 32)
	if e != nil {
		log.Fatal(e)
	}
	manager := service.New(store, credentialKey, dataDir, env("OCR_ENDPOINT", "https://orc.1999111.xyz/ocr"))
	manager.Start()
	defer manager.Close()
	staticDir := env("CTYUN_STATIC_DIR", "./app/web/static")
	handler := webapp.New(store, manager, sessionKey, credentialKey, version, dataDir, staticDir, strings.EqualFold(env("WEB_SECURE_COOKIE", "false"), "true")).Handler()
	server := &http.Server{Addr: ":" + env("APP_PORT", "9845"), Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-stop; manager.Close(); _ = server.Close() }()
	log.Printf("CtYunKeeper v%s listening on %s", version, server.Addr)
	if e = server.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		log.Fatal(e)
	}
}
