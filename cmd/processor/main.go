package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"

	"github.com/mykyta-kravchenko98/Susanoo/internal/messages"
	"github.com/mykyta-kravchenko98/Susanoo/internal/session"
	"github.com/mykyta-kravchenko98/Susanoo/internal/telegram"
)

const (
	callbackAddMore = "add_more"
	callbackDone    = "done"
)

var (
	logger        *slog.Logger
	sessionStore  *session.Store
	tgClient      *telegram.Client
)

func init() {
	logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))

	ctx := context.Background()

	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		panic(fmt.Sprintf("failed to load AWS config: %v", err))
	}

	sessionsTable := mustEnv("SESSIONS_TABLE")
	sessionStore = session.NewStore(dynamodb.NewFromConfig(cfg), sessionsTable)

	tgToken, err := fetchSecret(ctx, secretsmanager.NewFromConfig(cfg), mustEnv("TELEGRAM_TOKEN_SECRET"))
	if err != nil {
		panic(fmt.Sprintf("failed to fetch telegram token: %v", err))
	}
	tgClient = telegram.NewClient(tgToken)
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		panic(fmt.Sprintf("%s env var is not set", key))
	}
	return v
}

func fetchSecret(ctx context.Context, client *secretsmanager.Client, secretName string) (string, error) {
	out, err := client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: &secretName,
	})
	if err != nil {
		return "", fmt.Errorf("get secret %s: %w", secretName, err)
	}
	if out.SecretString == nil {
		return "", fmt.Errorf("secret %s has no string value", secretName)
	}
	return *out.SecretString, nil
}

func handler(ctx context.Context, sqsEvent events.SQSEvent) error {
	for _, record := range sqsEvent.Records {
		if err := handleRecord(ctx, record); err != nil {
			logger.ErrorContext(ctx, "failed to process record",
				slog.String("error", err.Error()),
				slog.String("message_id", record.MessageId),
			)
			return err
		}
	}
	return nil
}

func handleRecord(ctx context.Context, record events.SQSMessage) error {
	var update telegram.Update
	if err := json.Unmarshal([]byte(record.Body), &update); err != nil {
		logger.ErrorContext(ctx, "invalid update JSON, skipping", slog.String("error", err.Error()))
		return nil
	}

	switch {
	case update.CallbackQuery != nil:
		return handleCallback(ctx, update.CallbackQuery)
	case update.Message != nil && len(update.Message.Photo) > 0:
		return handlePhoto(ctx, update.Message)
	case update.Message != nil:
		return tgClient.SendMessage(ctx, update.Message.Chat.ID, messages.NoPhotoPrompt)
	default:
		logger.WarnContext(ctx, "update has neither message nor callback_query, ignoring")
		return nil
	}
}

func handlePhoto(ctx context.Context, msg *telegram.Message) error {
	photo, ok := msg.LargestPhoto()
	if !ok {
		return fmt.Errorf("message has Photo slice but LargestPhoto found none")
	}

	sess, err := sessionStore.AppendPhoto(ctx, msg.Chat.ID, photo.FileID)
	if err != nil {
		return fmt.Errorf("append photo to session: %w", err)
	}

	text := messages.PhotoAdded(len(sess.PhotoIDs))
	return tgClient.SendMessage(ctx, msg.Chat.ID, text,
		telegram.InlineButton{Text: messages.ButtonAddPage, CallbackData: callbackAddMore},
		telegram.InlineButton{Text: messages.ButtonDone, CallbackData: callbackDone},
	)
}

func handleCallback(ctx context.Context, cb *telegram.CallbackQuery) error {
	if err := tgClient.AnswerCallbackQuery(ctx, cb.ID); err != nil {
		logger.WarnContext(ctx, "failed to answer callback query", slog.String("error", err.Error()))
	}

	chatID := cb.Message.Chat.ID

	switch cb.Data {
	case callbackAddMore:
		return tgClient.SendMessage(ctx, chatID, messages.AddMorePrompt)

	case callbackDone:
		sess, err := sessionStore.Get(ctx, chatID)
		if err != nil {
			return fmt.Errorf("get session on done: %w", err)
		}
		if sess == nil || len(sess.PhotoIDs) == 0 {
			return tgClient.SendMessage(ctx, chatID, messages.NoPhotosYet)
		}

		if err := tgClient.SendMessage(ctx, chatID, messages.ProcessingStarted); err != nil {
			logger.WarnContext(ctx, "failed to send processing notice", slog.String("error", err.Error()))
		}

		// TODO: the next step is processDocument(ctx, chatID, sess.PhotoIDs):
		//   1. download photo by file_id via tgClient.GetFilePath + DownloadFile
		//   2. downsampling + PDF assembly
		//   3. Claude Haiku Vision API call (field extraction + translation)
		//   4. preview with "OK/Fix" buttons
		//   5. Upon confirmation: save to S3 + DynamoDB (letters) + EventBridge for the deadline.
		logger.InfoContext(ctx, "TODO: processDocument not yet implemented",
			slog.Int64("chat_id", chatID),
			slog.Int("photo_count", len(sess.PhotoIDs)),
		)

		return sessionStore.Clear(ctx, chatID)

	default:
		logger.WarnContext(ctx, "unknown callback data", slog.String("data", cb.Data))
		return nil
	}
}

func main() {
	lambda.Start(handler)
}