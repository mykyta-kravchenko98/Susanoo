package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"
 
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
 
	"github.com/mykyta-kravchenko98/Susanoo/internal/imaging"
	"github.com/mykyta-kravchenko98/Susanoo/internal/letters"
	"github.com/mykyta-kravchenko98/Susanoo/internal/llm"
	"github.com/mykyta-kravchenko98/Susanoo/internal/messages"
	"github.com/mykyta-kravchenko98/Susanoo/internal/pdfbuilder"
	"github.com/mykyta-kravchenko98/Susanoo/internal/session"
	"github.com/mykyta-kravchenko98/Susanoo/internal/storage"
	"github.com/mykyta-kravchenko98/Susanoo/internal/telegram"
)

const (
	callbackAddMore    = "add_more"
	callbackDone       = "done"
	callbackRestart    = "restart"
	callbackConfirmSave = "confirm_save"
	callbackRequestFix  = "request_fix"
)

var (
	logger        *slog.Logger
	sessionStore  *session.Store
	docStore      *storage.DocumentStore
	lettersStore  *letters.Store
	llmClient     *llm.Client
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

	documentsBucket := mustEnv("DOCUMENTS_BUCKET")
	docStore = storage.NewDocumentStore(s3.NewFromConfig(cfg), documentsBucket)

	lettersTable := mustEnv("LETTERS_TABLE")
	lettersStore = letters.NewStore(dynamodb.NewFromConfig(cfg), lettersTable)

	smClient := secretsmanager.NewFromConfig(cfg)

	tgToken, err := fetchSecret(ctx, smClient, mustEnv("TELEGRAM_TOKEN_SECRET"))
	if err != nil {
		panic(fmt.Sprintf("failed to fetch telegram token: %v", err))
	}
	tgClient = telegram.NewClient(tgToken)
 
	anthropicKey, err := fetchSecret(ctx, smClient, mustEnv("ANTHROPIC_KEY_SECRET"))
	if err != nil {
		panic(fmt.Sprintf("failed to fetch anthropic api key: %v", err))
	}
	llmClient = llm.NewClient(anthropicKey)
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
		telegram.InlineButton{Text: messages.ButtonStartOver, CallbackData: callbackRestart},
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

	case callbackRestart:
		if err := sessionStore.Clear(ctx, chatID); err != nil {
			return fmt.Errorf("clear session on restart: %w", err)
		}
		return tgClient.SendMessage(ctx, chatID, messages.SessionCleared)

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

		if err := processDocument(ctx, chatID, sess.PhotoIDs); err != nil {
			logger.ErrorContext(ctx, "failed to process document",
				slog.String("error", err.Error()),
				slog.Int64("chat_id", chatID),
			)
			if sendErr := tgClient.SendMessage(ctx, chatID, messages.ProcessingFailed); sendErr != nil {
				logger.WarnContext(ctx, "failed to send failure notice", slog.String("error", sendErr.Error()))
			}
			// Do not clear the session upon payment - the user might type "Done" again
			// without needing to photograph all the pages again.
			return fmt.Errorf("process document: %w", err)
		}

		return nil

	case callbackConfirmSave:
		return handleConfirmSave(ctx, chatID)
 
	case callbackRequestFix:
		if err := sessionStore.Clear(ctx, chatID); err != nil {
			return fmt.Errorf("clear session on fix: %w", err)
		}
		return tgClient.SendMessage(ctx, chatID, messages.RequestFixPrompt)
 
	default:
		logger.WarnContext(ctx, "unknown callback data", slog.String("data", cb.Data))
		return nil
	}
}

func processDocument(ctx context.Context, chatID int64, photoIDs []string) error {
	receivedAt := time.Now().UTC()
 
	downsampled := make([][]byte, 0, len(photoIDs))
 
	for i, fileID := range photoIDs {
		filePath, err := tgClient.GetFilePath(ctx, fileID)
		if err != nil {
			return fmt.Errorf("get file path for photo %d: %w", i, err)
		}
 
		raw, err := tgClient.DownloadFile(ctx, filePath)
		if err != nil {
			return fmt.Errorf("download photo %d: %w", i, err)
		}
 
		rawKey := fmt.Sprintf("raw/%d/%s_%d.jpg", chatID, receivedAt.Format("20060102T150405"), i)
		if err := docStore.PutRaw(ctx, rawKey, raw); err != nil {
			logger.WarnContext(ctx, "failed to store raw photo, continuing anyway",
				slog.String("error", err.Error()), slog.Int("photo_index", i))
		}
 
		small, err := imaging.Downsample(raw)
		if err != nil {
			return fmt.Errorf("downsample photo %d: %w", i, err)
		}
		downsampled = append(downsampled, small)
	}
 
	pdfBytes, err := pdfbuilder.BuildFromJPEGs(downsampled)
	if err != nil {
		return fmt.Errorf("build pdf: %w", err)
	}
 
	pdfKey := fmt.Sprintf("Unsorted/%d/%s.pdf", receivedAt.Year(), receivedAt.Format("20060102T150405"))
	if err := docStore.PutPDF(ctx, pdfKey, pdfBytes); err != nil {
		return fmt.Errorf("store pdf: %w", err)
	}
 
	fields, err := llmClient.ClassifyLetter(ctx, downsampled, receivedAt.Format("2006-01-02"))
	if err != nil {
		return fmt.Errorf("classify letter (pdf already saved at %s): %w", pdfKey, err)
	}

	classificationJSON, err := json.Marshal(fields)
	if err != nil {
		return fmt.Errorf("marshal classification: %w", err)
	}

	if err := sessionStore.SetPendingConfirmation(ctx, chatID, pdfKey, string(classificationJSON)); err != nil {
		return fmt.Errorf("set pending confirmation: %w", err)
	}

	logger.InfoContext(ctx, "document classified, awaiting confirmation",
		slog.Int64("chat_id", chatID),
		slog.Int("page_count", len(downsampled)),
		slog.String("pdf_key", pdfKey),
		slog.String("organization", fields.Organization),
	)

	return tgClient.SendMessage(ctx, chatID, messages.ClassificationPreview(
		fields.Organization, fields.DocType, fields.SummaryRU, fields.ActionRequiredRU, fields.Deadline, fields.Urgency,
	),
		telegram.InlineButton{Text: messages.ButtonSave, CallbackData: callbackConfirmSave},
		telegram.InlineButton{Text: messages.ButtonFix, CallbackData: callbackRequestFix},
	)
}

func handleConfirmSave(ctx context.Context, chatID int64) error {
	sess, err := sessionStore.MarkSaving(ctx, chatID)
	if err != nil {
		if errors.Is(err, session.ErrNotPending) {
			return tgClient.SendMessage(ctx, chatID, messages.NothingToConfirm)
		}
		return fmt.Errorf("mark session saving: %w", err)
	}
 
	if err := tgClient.SendMessage(ctx, chatID, messages.SavingStarted); err != nil {
		logger.WarnContext(ctx, "failed to send saving notice", slog.String("error", err.Error()))
	}
 
	var fields llm.ExtractedFields
	if err := json.Unmarshal([]byte(sess.Classification), &fields); err != nil {
		return fmt.Errorf("unmarshal stored classification: %w", err)
	}
 
	now := time.Now().UTC()
	safeOrg := sanitizeForKey(fields.Organization)
	safeFilename := sanitizeForKey(fields.Filename)
	finalKey := fmt.Sprintf("%d/%s/%d/%02d/%s.pdf", chatID, safeOrg, now.Year(), now.Month(), safeFilename)
 
	if err := docStore.Move(ctx, sess.PDFKey, finalKey); err != nil {
		if sendErr := tgClient.SendMessage(ctx, chatID, messages.SaveFailed); sendErr != nil {
			logger.WarnContext(ctx, "failed to send save-failed notice", slog.String("error", sendErr.Error()))
		}
		return fmt.Errorf("move pdf to final location: %w", err)
	}
 
	letterID, err := letters.NewLetterID()
	if err != nil {
		return fmt.Errorf("generate letter id: %w", err)
	}
 
	letter := letters.Letter{
		LetterID:     letterID,
		ChatID:       chatID,
		Organization: fields.Organization,
		ReceivedDate: time.Now().UTC().Format("2006-01-02"),
		DocType:      fields.DocType,
		Filename:     fields.Filename,
		Summary:      fields.Summary,
		SummaryRU:    fields.SummaryRU,
		Urgency:      fields.Urgency,
		S3Key:        finalKey,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	}
	if fields.Deadline != nil {
		letter.Deadline = *fields.Deadline
	}
	if fields.ActionRequired != nil {
		letter.ActionRequired = *fields.ActionRequired
	}
	if fields.ActionRequiredRU != nil {
		letter.ActionRequiredRU = *fields.ActionRequiredRU
	}
 
	if err := lettersStore.Put(ctx, letter); err != nil {
		return fmt.Errorf("save letter metadata: %w", err)
	}

 
	if err := sessionStore.Clear(ctx, chatID); err != nil {
		logger.WarnContext(ctx, "failed to clear session after save", slog.String("error", err.Error()))
	}
 
	logger.InfoContext(ctx, "letter saved",
		slog.Int64("chat_id", chatID),
		slog.String("letter_id", letterID),
		slog.String("s3_key", finalKey),
	)
 
	return tgClient.SendMessage(ctx, chatID, messages.LetterSaved)
}
 
// sanitizeForKey converts a string into a format safe for an S3 key: only [a-z0-9-_] are allowed,
// with spaces and other characters replaced by "-". This ensures that CopyObject (used in Move)
// does not fail due to spaces or Unicode characters in CopySource without manual URL encoding.
var unsafeKeyChars = regexp.MustCompile(`[^a-z0-9\-_]+`)

func sanitizeForKey(s string) string {
	lower := strings.ToLower(strings.TrimSpace(s))
	safe := unsafeKeyChars.ReplaceAllString(lower, "-")
	safe = strings.Trim(safe, "-")
	if safe == "" {
		return "unsorted"
	}
	return safe
}

func main() {
	lambda.Start(handler)
}