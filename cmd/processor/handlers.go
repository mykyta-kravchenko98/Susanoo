package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/aws/aws-lambda-go/events"

	"github.com/mykyta-kravchenko98/Susanoo/internal/messages"
	"github.com/mykyta-kravchenko98/Susanoo/internal/telegram"
)

const (
	callbackAddMore     = "add_more"
	callbackDone        = "done"
	callbackRestart     = "restart"
	callbackConfirmSave = "confirm_save"
	callbackRequestFix  = "request_fix"

	// callbackRequestPDFPrefix and callbackDeleteLetterPrefix are followed
	// directly by a letter_id (e.g. "pdf:3f9c..."), mirroring
	// callbackViewLetterPrefix in commands.go. callbackRequestPDFPrefix is
	// handled by handleRequestPDF (archive.go); callbackDeleteLetterPrefix's
	// handler is still pending (a later PR) - until then it falls through to
	// the "unknown callback data" default below.
	callbackRequestPDFPrefix   = "pdf:"
	callbackDeleteLetterPrefix = "delete:"
)

func (a *App) Handle(ctx context.Context, sqsEvent events.SQSEvent) error {
	for _, record := range sqsEvent.Records {
		if err := a.handleRecord(ctx, record); err != nil {
			a.logger.ErrorContext(ctx, "failed to process record",
				slog.String("error", err.Error()),
				slog.String("message_id", record.MessageId),
			)
			return err
		}
	}
	return nil
}

func (a *App) handleRecord(ctx context.Context, record events.SQSMessage) error {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal([]byte(record.Body), &probe); err != nil {
		a.logger.ErrorContext(ctx, "invalid JSON in message body, skipping", slog.String("error", err.Error()))
		return nil
	}

	if _, isProcessedImages := probe["processed_keys"]; isProcessedImages {
		return a.handleProcessedImages(ctx, record.Body)
	}

	if _, isTelegramUpdate := probe["update_id"]; isTelegramUpdate {
		var update telegram.Update
		if err := json.Unmarshal([]byte(record.Body), &update); err != nil {
			a.logger.ErrorContext(ctx, "invalid telegram update JSON, skipping", slog.String("error", err.Error()))
			return nil
		}
		return a.handleTelegramUpdate(ctx, &update)
	}

	a.logger.WarnContext(ctx, "unrecognized message shape, skipping")
	return nil
}

func (a *App) handleTelegramUpdate(ctx context.Context, update *telegram.Update) error {
	switch {
	case update.CallbackQuery != nil:
		return a.handleCallback(ctx, update.CallbackQuery)
	case update.Message != nil && strings.HasPrefix(strings.TrimSpace(update.Message.Text), "/"):
		return a.dispatchCommand(ctx, update.Message)
	case update.Message != nil && len(update.Message.Photo) > 0:
		return a.handlePhoto(ctx, update.Message)
	case update.Message != nil:
		return a.telegramClient.SendMessage(ctx, update.Message.Chat.ID, messages.NoPhotoPrompt)
	default:
		a.logger.WarnContext(ctx, "update has neither message nor callback_query, ignoring")
		return nil
	}
}

func (a *App) handleCallback(ctx context.Context, cb *telegram.CallbackQuery) error {
	if err := a.telegramClient.AnswerCallbackQuery(ctx, cb.ID); err != nil {
		a.logger.WarnContext(ctx, "failed to answer callback query", slog.String("error", err.Error()))
	}

	chatID := cb.Message.Chat.ID

	switch {
	case cb.Data == callbackAddMore:
		return a.handleAddMore(ctx, chatID)

	case cb.Data == callbackRestart:
		return a.handleRestart(ctx, chatID)

	case cb.Data == callbackDone:
		return a.handleDone(ctx, chatID)

	case cb.Data == callbackConfirmSave:
		return a.handleConfirmSave(ctx, chatID)

	case cb.Data == callbackRequestFix:
		return a.handleRequestFix(ctx, chatID)

	case strings.HasPrefix(cb.Data, callbackViewLetterPrefix):
		letterID := strings.TrimPrefix(cb.Data, callbackViewLetterPrefix)
		return a.handleViewLetter(ctx, chatID, letterID)

	case strings.HasPrefix(cb.Data, callbackRequestPDFPrefix):
		letterID := strings.TrimPrefix(cb.Data, callbackRequestPDFPrefix)
		return a.handleRequestPDF(ctx, chatID, letterID)

	case strings.HasPrefix(cb.Data, callbackArchiveOrgPrefix):
		orgSlug := strings.TrimPrefix(cb.Data, callbackArchiveOrgPrefix)
		return a.handleArchiveOrg(ctx, chatID, orgSlug)

	case strings.HasPrefix(cb.Data, callbackArchiveYearPrefix):
		return a.handleArchiveYearCallback(ctx, chatID, strings.TrimPrefix(cb.Data, callbackArchiveYearPrefix))

	case strings.HasPrefix(cb.Data, callbackDeleteLetterPrefix):
		letterID := strings.TrimPrefix(cb.Data, callbackDeleteLetterPrefix)
		return a.handleDeleteLetter(ctx, chatID, letterID)

	default:
		a.logger.WarnContext(ctx, "unknown callback data", slog.String("data", cb.Data))
		return nil
	}
}
