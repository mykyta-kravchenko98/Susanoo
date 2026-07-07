package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"

	"github.com/mykyta-kravchenko98/Susanoo/internal/helper"
	"github.com/mykyta-kravchenko98/Susanoo/internal/telegram"
)

type TelegramSender interface {
	SendMessage(ctx context.Context, chatID int64, text string, buttons ...telegram.InlineButton) error
}

type App struct {
	telegramClient TelegramSender
	logger         *slog.Logger
}

func buildApp(ctx context.Context) (*App, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	smClient := secretsmanager.NewFromConfig(cfg)
	tgToken, err := helper.FetchSecret(ctx, smClient, helper.MustEnv("TELEGRAM_TOKEN_SECRET"))
	if err != nil {
		return nil, fmt.Errorf("fetch telegram token: %w", err)
	}

	return &App{
		telegramClient: telegram.NewClient(tgToken, logger),
		logger:         logger,
	}, nil
}
