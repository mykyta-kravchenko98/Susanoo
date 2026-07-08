package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/scheduler"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/mykyta-kravchenko98/Susanoo/internal/helper"
	"github.com/mykyta-kravchenko98/Susanoo/internal/letters"
	"github.com/mykyta-kravchenko98/Susanoo/internal/llm"
	"github.com/mykyta-kravchenko98/Susanoo/internal/reminders"
	"github.com/mykyta-kravchenko98/Susanoo/internal/session"
	"github.com/mykyta-kravchenko98/Susanoo/internal/storage"
	"github.com/mykyta-kravchenko98/Susanoo/internal/telegram"
)

type TelegramClient interface {
	SendMessage(ctx context.Context, chatID int64, text string, buttons ...telegram.InlineButton) error
	SendMessageWithRows(ctx context.Context, chatID int64, text string, rows [][]telegram.InlineButton) error
	GetFilePath(ctx context.Context, fileID string) (string, error)
	DownloadFile(ctx context.Context, filePath string) ([]byte, error)
	AnswerCallbackQuery(ctx context.Context, callbackQueryID string) error
	SetMyCommands(ctx context.Context, commands []telegram.BotCommand) error
}

type LLMClassifier interface {
	ClassifyLetter(ctx context.Context, images [][]byte, receivedDate string) (*llm.ExtractedFields, error)
}

type SQSSender interface {
	SendMessage(ctx context.Context, params *sqs.SendMessageInput, optFns ...func(*sqs.Options)) (*sqs.SendMessageOutput, error)
}

type App struct {
	sessions           *session.Store
	docs               *storage.DocumentStore
	letters            *letters.Store
	llmClient          LLMClassifier
	telegramClient     TelegramClient
	sqsClient          SQSSender
	imagesToProcessURL string
	reminderScheduler  *reminders.Scheduler
	logger             *slog.Logger
}

func buildApp(ctx context.Context) (*App, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	sessionsTable := helper.MustEnv("SESSIONS_TABLE")
	documentsBucket := helper.MustEnv("DOCUMENTS_BUCKET")
	lettersTable := helper.MustEnv("LETTERS_TABLE")
	imagesToProcessURL := helper.MustEnv("IMAGES_TO_PROCESS_QUEUE_URL")
	reminderLambdaArn := helper.MustEnv("REMINDER_LAMBDA_ARN")
	schedulerRoleArn := helper.MustEnv("SCHEDULER_ROLE_ARN")
	scheduleGroupName := helper.MustEnv("SCHEDULE_GROUP_NAME")

	smClient := secretsmanager.NewFromConfig(cfg)

	tgToken, err := helper.FetchSecret(ctx, smClient, helper.MustEnv("TELEGRAM_TOKEN_SECRET"))
	if err != nil {
		return nil, fmt.Errorf("fetch telegram token: %w", err)
	}

	anthropicKey, err := helper.FetchSecret(ctx, smClient, helper.MustEnv("ANTHROPIC_KEY_SECRET"))
	if err != nil {
		return nil, fmt.Errorf("fetch anthropic api key: %w", err)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	app := &App{
		sessions:           session.NewStore(dynamodb.NewFromConfig(cfg), sessionsTable),
		docs:               storage.NewDocumentStore(s3.NewFromConfig(cfg), documentsBucket, logger),
		letters:            letters.NewStore(dynamodb.NewFromConfig(cfg), lettersTable),
		llmClient:          llm.NewClient(anthropicKey),
		telegramClient:     telegram.NewClient(tgToken, logger),
		sqsClient:          sqs.NewFromConfig(cfg),
		imagesToProcessURL: imagesToProcessURL,
		reminderScheduler:  reminders.NewScheduler(scheduler.NewFromConfig(cfg), scheduleGroupName, reminderLambdaArn, schedulerRoleArn),
		logger:             logger,
	}

	if err := app.telegramClient.SetMyCommands(ctx, botCommands()); err != nil {
		app.logger.WarnContext(ctx, "failed to register bot commands", slog.String("error", err.Error()))
	}

	return app, nil
}
