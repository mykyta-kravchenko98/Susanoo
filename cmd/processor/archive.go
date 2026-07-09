package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/mykyta-kravchenko98/Susanoo/internal/letters"
	"github.com/mykyta-kravchenko98/Susanoo/internal/messages"
	"github.com/mykyta-kravchenko98/Susanoo/internal/telegram"
)

func (a *App) getOwnedLetter(ctx context.Context, chatID int64, letterID string) (*letters.Letter, error) {
	letter, err := a.letters.Get(ctx, letterID)
	if err != nil {
		if errors.Is(err, letters.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("get letter %s: %w", letterID, err)
	}

	if letter.ChatID != chatID {
		a.logger.WarnContext(ctx, "chat_id mismatch on letter callback",
			slog.String("letter_id", letterID),
			slog.Int64("requesting_chat_id", chatID),
			slog.Int64("letter_chat_id", letter.ChatID))
		return nil, nil
	}

	if letter.Status == letters.StatusPendingDeletion {
		return nil, nil
	}

	return letter, nil
}

// handleViewLetter responds to a letter button pressed from /archive's list
// (see callbackViewLetterPrefix in commands.go). It shows the letter's
// metadata plus "Request PDF"/"Delete" buttons.
func (a *App) handleViewLetter(ctx context.Context, chatID int64, letterID string) error {
	letter, err := a.getOwnedLetter(ctx, chatID, letterID)
	if err != nil {
		return err
	}
	if letter == nil {
		return a.telegramClient.SendMessage(ctx, chatID, messages.LetterNotFound)
	}

	text := messages.LetterDetail(letter.ReceivedDate, letter.Organization, letter.DocType,
		letter.SummaryRU, letter.Deadline, letter.ActionRequiredRU)

	return a.telegramClient.SendMessage(ctx, chatID, text,
		telegram.InlineButton{Text: messages.ButtonRequestPDF, CallbackData: callbackRequestPDFPrefix + letter.LetterID},
		telegram.InlineButton{Text: messages.ButtonDeleteLetter, CallbackData: callbackDeleteLetterPrefix + letter.LetterID},
	)
}

func (a *App) handleRequestPDF(ctx context.Context, chatID int64, letterID string) error {
	letter, err := a.getOwnedLetter(ctx, chatID, letterID)
	if err != nil {
		return err
	}
	if letter == nil {
		return a.telegramClient.SendMessage(ctx, chatID, messages.LetterNotFound)
	}

	data, err := a.docs.GetObject(ctx, letter.S3Key)
	if err != nil {
		if sendErr := a.telegramClient.SendMessage(ctx, chatID, messages.PDFRequestFailed); sendErr != nil {
			a.logger.WarnContext(ctx, "failed to send pdf-request-failed notice", slog.String("error", sendErr.Error()))
		}
		return fmt.Errorf("get pdf object %s for letter %s: %w", letter.S3Key, letterID, err)
	}

	if err := a.telegramClient.SendDocument(ctx, chatID, pdfFilename(letter.Filename), data); err != nil {
		if sendErr := a.telegramClient.SendMessage(ctx, chatID, messages.PDFRequestFailed); sendErr != nil {
			a.logger.WarnContext(ctx, "failed to send pdf-request-failed notice", slog.String("error", sendErr.Error()))
		}
		return fmt.Errorf("send pdf document for letter %s: %w", letterID, err)
	}

	return nil
}

// pdfFilename ensures the document sent to Telegram has a sensible .pdf
// filename even if the stored Filename is empty.
func pdfFilename(filename string) string {
	if filename == "" {
		filename = "letter"
	}
	if !strings.HasSuffix(strings.ToLower(filename), ".pdf") {
		filename += ".pdf"
	}
	return filename
}

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
