package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/mykyta-kravchenko98/Susanoo/internal/letters"
	"github.com/mykyta-kravchenko98/Susanoo/internal/llm"
	"github.com/mykyta-kravchenko98/Susanoo/internal/messages"
	"github.com/mykyta-kravchenko98/Susanoo/internal/pdfbuilder"
	"github.com/mykyta-kravchenko98/Susanoo/internal/reminders"
	"github.com/mykyta-kravchenko98/Susanoo/internal/session"
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
	// callbackViewLetterPrefix in commands.go. Wired up here on the letter
	// detail view (handleViewLetter) ahead of their own handlers landing in
	// a later PR - see the comment on callbackViewLetterPrefix for why that's
	// fine.
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

func (a *App) handlePhoto(ctx context.Context, msg *telegram.Message) error {
	photo, ok := msg.LargestPhoto()
	if !ok {
		return fmt.Errorf("message has Photo slice but LargestPhoto found none")
	}

	filePath, err := a.telegramClient.GetFilePath(ctx, photo.FileID)
	if err != nil {
		return fmt.Errorf("get file path: %w", err)
	}

	raw, err := a.telegramClient.DownloadFile(ctx, filePath)
	if err != nil {
		return fmt.Errorf("download photo: %w", err)
	}

	rawKey := fmt.Sprintf("raw/%d/%s.jpg", msg.Chat.ID, time.Now().UTC().Format("20060102T150405.000000"))
	if err := a.docs.PutRaw(ctx, rawKey, raw); err != nil {
		return fmt.Errorf("store raw photo: %w", err)
	}

	sess, err := a.sessions.AppendRawKey(ctx, msg.Chat.ID, rawKey)
	if err != nil {
		return fmt.Errorf("append raw key to session: %w", err)
	}

	text := messages.PhotoAdded(len(sess.RawKeys))
	return a.telegramClient.SendMessage(ctx, msg.Chat.ID, text,
		telegram.InlineButton{Text: messages.ButtonAddPage, CallbackData: callbackAddMore},
		telegram.InlineButton{Text: messages.ButtonDone, CallbackData: callbackDone},
		telegram.InlineButton{Text: messages.ButtonStartOver, CallbackData: callbackRestart},
	)
}

func (a *App) handleCallback(ctx context.Context, cb *telegram.CallbackQuery) error {
	if err := a.telegramClient.AnswerCallbackQuery(ctx, cb.ID); err != nil {
		a.logger.WarnContext(ctx, "failed to answer callback query", slog.String("error", err.Error()))
	}

	chatID := cb.Message.Chat.ID

	switch {
	case cb.Data == callbackAddMore:
		return a.telegramClient.SendMessage(ctx, chatID, messages.AddMorePrompt)

	case cb.Data == callbackRestart:
		if err := a.sessions.Clear(ctx, chatID); err != nil {
			return fmt.Errorf("clear session on restart: %w", err)
		}
		return a.telegramClient.SendMessage(ctx, chatID, messages.SessionCleared)

	case cb.Data == callbackDone:
		return a.handleDone(ctx, chatID)

	case cb.Data == callbackConfirmSave:
		return a.handleConfirmSave(ctx, chatID)

	case cb.Data == callbackRequestFix:
		if err := a.sessions.Clear(ctx, chatID); err != nil {
			return fmt.Errorf("clear session on fix: %w", err)
		}
		return a.telegramClient.SendMessage(ctx, chatID, messages.RequestFixPrompt)

	case strings.HasPrefix(cb.Data, callbackViewLetterPrefix):
		letterID := strings.TrimPrefix(cb.Data, callbackViewLetterPrefix)
		return a.handleViewLetter(ctx, chatID, letterID)

	case strings.HasPrefix(cb.Data, callbackArchiveOrgPrefix):
		orgSlug := strings.TrimPrefix(cb.Data, callbackArchiveOrgPrefix)
		return a.handleArchiveOrg(ctx, chatID, orgSlug)

	case strings.HasPrefix(cb.Data, callbackArchiveYearPrefix):
		return a.handleArchiveYearCallback(ctx, chatID, strings.TrimPrefix(cb.Data, callbackArchiveYearPrefix))

	default:
		a.logger.WarnContext(ctx, "unknown callback data", slog.String("data", cb.Data))
		return nil
	}
}

func (a *App) handleViewLetter(ctx context.Context, chatID int64, letterID string) error {
	letter, err := a.letters.Get(ctx, letterID)
	if err != nil {
		if errors.Is(err, letters.ErrNotFound) {
			return a.telegramClient.SendMessage(ctx, chatID, messages.LetterNotFound)
		}
		return fmt.Errorf("get letter %s: %w", letterID, err)
	}

	if letter.ChatID != chatID {
		a.logger.WarnContext(ctx, "chat_id mismatch on view letter callback",
			slog.String("letter_id", letterID),
			slog.Int64("requesting_chat_id", chatID),
			slog.Int64("letter_chat_id", letter.ChatID))
		return a.telegramClient.SendMessage(ctx, chatID, messages.LetterNotFound)
	}

	if letter.Status == letters.StatusPendingDeletion {
		return a.telegramClient.SendMessage(ctx, chatID, messages.LetterNotFound)
	}

	text := messages.LetterDetail(letter.ReceivedDate, letter.Organization, letter.DocType,
		letter.SummaryRU, letter.Deadline, letter.ActionRequiredRU)

	return a.telegramClient.SendMessage(ctx, chatID, text,
		telegram.InlineButton{Text: messages.ButtonRequestPDF, CallbackData: callbackRequestPDFPrefix + letter.LetterID},
		telegram.InlineButton{Text: messages.ButtonDeleteLetter, CallbackData: callbackDeleteLetterPrefix + letter.LetterID},
	)
}

// handleArchiveOrg is /archive's second level: it shows the years the chat
// has letters from one organization (see callbackArchiveOrgPrefix in
// commands.go). Tapping a year leads to handleArchiveYearCallback below.
func (a *App) handleArchiveOrg(ctx context.Context, chatID int64, orgSlug string) error {
	years, err := a.letters.QueryYears(ctx, chatID, orgSlug)
	if err != nil {
		return fmt.Errorf("query years for org %s: %w", orgSlug, err)
	}

	if len(years) == 0 {
		return a.telegramClient.SendMessage(ctx, chatID, messages.ArchiveEmpty)
	}

	rows := make([][]telegram.InlineButton, 0, len(years))
	for _, year := range years {
		rows = append(rows, []telegram.InlineButton{
			{
				Text:         strconv.Itoa(year),
				CallbackData: fmt.Sprintf("%s%s:%d", callbackArchiveYearPrefix, orgSlug, year),
			},
		})
	}

	return a.telegramClient.SendMessageWithRows(ctx, chatID, messages.ArchiveYearsHeader, rows)
}

// handleArchiveYearCallback parses the "{org_slug}:{year}" payload from a
// callbackArchiveYearPrefix callback and delegates to handleArchiveYear. Kept
// separate from handleArchiveYear itself so the parsing/validation (which
// can fail on malformed data) is isolated from the actual query+render logic.
func (a *App) handleArchiveYearCallback(ctx context.Context, chatID int64, rest string) error {
	orgSlug, yearStr, ok := strings.Cut(rest, ":")
	if !ok {
		a.logger.WarnContext(ctx, "malformed archive-year callback data", slog.String("data", rest))
		return nil
	}

	year, err := strconv.Atoi(yearStr)
	if err != nil {
		a.logger.WarnContext(ctx, "malformed year in archive-year callback data", slog.String("data", rest))
		return nil
	}

	return a.handleArchiveYear(ctx, chatID, orgSlug, year)
}

// handleArchiveYear is /archive's third and final level: the organization's
// letters from one specific year, each as its own button leading into the
// existing handleViewLetter flow (callbackViewLetterPrefix) - the same
// per-letter view the original flat /archive list used.
func (a *App) handleArchiveYear(ctx context.Context, chatID int64, orgSlug string, year int) error {
	list, err := a.letters.QueryByOrgYear(ctx, chatID, orgSlug, year)
	if err != nil {
		return fmt.Errorf("query letters for org %s year %d: %w", orgSlug, year, err)
	}

	if len(list) == 0 {
		return a.telegramClient.SendMessage(ctx, chatID, messages.ArchiveEmpty)
	}

	rows := make([][]telegram.InlineButton, 0, len(list))
	for _, letter := range list {
		label := messages.LetterButtonLabel(letter.ReceivedDate, letter.Organization, letter.DocType)
		rows = append(rows, []telegram.InlineButton{
			{Text: label, CallbackData: callbackViewLetterPrefix + letter.LetterID},
		})
	}

	return a.telegramClient.SendMessageWithRows(ctx, chatID, messages.ArchiveHeader, rows)
}

func (a *App) handleDone(ctx context.Context, chatID int64) error {
	sess, err := a.sessions.Get(ctx, chatID)
	if err != nil {
		return fmt.Errorf("get session on done: %w", err)
	}
	if sess == nil || len(sess.RawKeys) == 0 {
		return a.telegramClient.SendMessage(ctx, chatID, messages.NoPhotosYet)
	}

	updated, err := a.sessions.MarkAwaitingProcessing(ctx, chatID)
	if err != nil {
		if errors.Is(err, session.ErrAlreadyProcessing) {
			return nil
		}
		return fmt.Errorf("mark session awaiting processing: %w", err)
	}

	if err := a.telegramClient.SendMessage(ctx, chatID, messages.ProcessingStarted); err != nil {
		a.logger.WarnContext(ctx, "failed to send processing notice", slog.String("error", err.Error()))
	}

	batch, err := json.Marshal(imagesToProcessMessage{ChatID: chatID, RawKeys: updated.RawKeys})
	if err != nil {
		return fmt.Errorf("marshal images-to-process batch: %w", err)
	}

	if _, err := a.sqsClient.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    aws.String(a.imagesToProcessURL),
		MessageBody: aws.String(string(batch)),
	}); err != nil {
		return fmt.Errorf("send images-to-process batch: %w", err)
	}

	return nil
}

func (a *App) handleProcessedImages(ctx context.Context, body string) error {
	var msg processedImagesMessage
	if err := json.Unmarshal([]byte(body), &msg); err != nil {
		return fmt.Errorf("unmarshal processed-images message: %w", err)
	}

	images := make([][]byte, 0, len(msg.ProcessedKeys))
	for i, key := range msg.ProcessedKeys {
		data, err := a.docs.GetObject(ctx, key)
		if err != nil {
			return fmt.Errorf("get processed image %d (%s): %w", i, key, err)
		}
		images = append(images, data)
	}

	receivedAt := time.Now().UTC()

	pdfBytes, err := pdfbuilder.BuildFromJPEGs(images)
	if err != nil {
		return fmt.Errorf("build pdf: %w", err)
	}

	pdfKey := fmt.Sprintf("Unsorted/%d/%d/%s.pdf", msg.ChatID, receivedAt.Year(), receivedAt.Format("20060102T150405"))
	if err := a.docs.PutPDF(ctx, pdfKey, pdfBytes); err != nil {
		return fmt.Errorf("store pdf: %w", err)
	}

	fields, err := a.llmClient.ClassifyLetter(ctx, images, receivedAt.Format("2006-01-02"))
	if err != nil {
		return fmt.Errorf("classify letter (pdf already saved at %s): %w", pdfKey, err)
	}

	classificationJSON, err := json.Marshal(fields)
	if err != nil {
		return fmt.Errorf("marshal classification: %w", err)
	}

	if err := a.sessions.SetPendingConfirmation(ctx, msg.ChatID, pdfKey, string(classificationJSON)); err != nil {
		return fmt.Errorf("set pending confirmation: %w", err)
	}

	a.logger.InfoContext(ctx, "document classified, awaiting confirmation",
		slog.Int64("chat_id", msg.ChatID),
		slog.Int("page_count", len(images)),
		slog.String("pdf_key", pdfKey),
		slog.String("organization", fields.Organization),
	)

	isOverdue := false
	if fields.Deadline != nil {
		if parsed, err := time.Parse("2006-01-02", *fields.Deadline); err == nil {
			isOverdue = parsed.Before(time.Now().UTC().Truncate(24 * time.Hour))
		} else {
			a.logger.WarnContext(ctx, "could not parse deadline date, skipping overdue check",
				slog.String("deadline", *fields.Deadline), slog.String("error", err.Error()))
		}
	}

	return a.telegramClient.SendMessage(ctx, msg.ChatID, messages.ClassificationPreview(
		fields.Organization, fields.DocType, fields.SummaryRU, fields.ActionRequiredRU, fields.Deadline, fields.Urgency, isOverdue,
	),
		telegram.InlineButton{Text: messages.ButtonSave, CallbackData: callbackConfirmSave},
		telegram.InlineButton{Text: messages.ButtonFix, CallbackData: callbackRequestFix},
	)
}

func (a *App) handleConfirmSave(ctx context.Context, chatID int64) error {
	sess, err := a.sessions.MarkSaving(ctx, chatID)
	if err != nil {
		if errors.Is(err, session.ErrNotPending) {
			return a.telegramClient.SendMessage(ctx, chatID, messages.NothingToConfirm)
		}
		return fmt.Errorf("mark session saving: %w", err)
	}

	if err := a.telegramClient.SendMessage(ctx, chatID, messages.SavingStarted); err != nil {
		a.logger.WarnContext(ctx, "failed to send saving notice", slog.String("error", err.Error()))
	}

	var fields llm.ExtractedFields
	if err := json.Unmarshal([]byte(sess.Classification), &fields); err != nil {
		return fmt.Errorf("unmarshal stored classification: %w", err)
	}

	now := time.Now().UTC()
	safeOrg := letters.SanitizeForKey(fields.Organization)
	safeFilename := letters.SanitizeForKey(fields.Filename)
	finalKey := fmt.Sprintf("%d/%s/%d/%02d/%s.pdf", chatID, safeOrg, now.Year(), now.Month(), safeFilename)

	if err := a.docs.Move(ctx, sess.PDFKey, finalKey); err != nil {
		if sendErr := a.telegramClient.SendMessage(ctx, chatID, messages.SaveFailed); sendErr != nil {
			a.logger.WarnContext(ctx, "failed to send save-failed notice", slog.String("error", sendErr.Error()))
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

	if err := a.letters.Put(ctx, letter); err != nil {
		return fmt.Errorf("save letter metadata: %w", err)
	}

	if fields.Deadline != nil {
		if deadline, parseErr := time.Parse("2006-01-02", *fields.Deadline); parseErr == nil {
			plan := reminders.Plan(deadline, time.Now().UTC(), fields.Urgency)
			if len(plan) > 0 {
				payload := reminders.Payload{
					ChatID:           chatID,
					LetterID:         letterID,
					Organization:     fields.Organization,
					DocType:          fields.DocType,
					Deadline:         *fields.Deadline,
					ActionRequiredRU: fields.ActionRequiredRU,
				}
				if err := a.reminderScheduler.ScheduleAll(ctx, letterID, payload, plan); err != nil {
					// The letter itself is already saved successfully at this
					// point — a failed reminder schedule shouldn't roll that
					// back or fail the whole Save flow, just get logged so
					// it's visible in CloudWatch.
					a.logger.WarnContext(ctx, "failed to schedule deadline reminders",
						slog.String("letter_id", letterID), slog.String("error", err.Error()))
				}
			}
		} else {
			a.logger.WarnContext(ctx, "could not parse deadline date, skipping reminder scheduling",
				slog.String("deadline", *fields.Deadline), slog.String("error", parseErr.Error()))
		}
	}

	if err := a.sessions.Clear(ctx, chatID); err != nil {
		a.logger.WarnContext(ctx, "failed to clear session after save", slog.String("error", err.Error()))
	}

	a.logger.InfoContext(ctx, "letter saved",
		slog.Int64("chat_id", chatID),
		slog.String("letter_id", letterID),
		slog.String("s3_key", finalKey),
	)

	return a.telegramClient.SendMessage(ctx, chatID, messages.LetterSaved)
}
