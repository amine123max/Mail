package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/amine123max/Mail/server/internal/auth"
	"github.com/amine123max/Mail/server/internal/config"
	"github.com/amine123max/Mail/server/internal/secure"
	"github.com/amine123max/Mail/server/internal/store"
	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load()
	var input struct {
		Username string `json:"username"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		fail("管理员配置输入格式无效")
	}
	cfg, err := config.Load()
	if err != nil {
		fail(err.Error())
	}
	box, err := secure.New(cfg.DataDir, cfg.EncryptionKey, cfg.Production)
	if err != nil {
		fail(err.Error())
	}
	storage, err := store.Open(cfg.DataDir, box)
	if err != nil {
		fail(err.Error())
	}
	defer storage.Close()
	user, err := auth.New(cfg, storage).BootstrapAdministrator(context.Background(), input.Username, input.Email, input.Password)
	if err != nil {
		fail(err.Error())
	}
	fmt.Printf("管理员 %s 已创建\n", user.Username)
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
