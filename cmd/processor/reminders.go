package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/mykyta-kravchenko98/Susanoo/internal/messages"
	"github.com/mykyta-kravchenko98/Susanoo/internal/telegram"
)

func (a *App) handleViewReminder(ctx context.Context, chatID int64, name string) error {
	letterID, kind, ok := strings.Cut(name, "-")
	if !ok {
		a.logger.WarnContext(ctx, "malformed view-reminder callback data", slog.String("data", name))
		return nil
	}

	letter, err := a.getOwnedLetter(ctx, chatID, letterID)
	if err != nil {
		return err
	}
	if letter == nil {
		return a.telegramClient.SendMessage(ctx, chatID, messages.LetterNotFound)
	}

	text := messages.ReminderDetail(letter.Organization, letter.DocType, letter.Deadline, kind)

	return a.telegramClient.SendMessage(ctx, chatID, text,
		telegram.InlineButton{Text: messages.ButtonCancelReminder, CallbackData: callbackCancelReminderPrefix + name},
	)
}

func (a *App) handleCancelReminder(ctx context.Context, chatID int64, name string) error {
	letterID, _, ok := strings.Cut(name, "-")
	if !ok {
		a.logger.WarnContext(ctx, "malformed cancel-reminder callback data", slog.String("data", name))
		return nil
	}

	letter, err := a.getOwnedLetter(ctx, chatID, letterID)
	if err != nil {
		return err
	}
	if letter == nil {
		return a.telegramClient.SendMessage(ctx, chatID, messages.LetterNotFound)
	}

	if err := a.reminderScheduler.Cancel(ctx, name); err != nil {
		if sendErr := a.telegramClient.SendMessage(ctx, chatID, messages.ReminderCancelFailed); sendErr != nil {
			a.logger.WarnContext(ctx, "failed to send reminder-cancel-failed notice", slog.String("error", sendErr.Error()))
		}
		return fmt.Errorf("cancel schedule %s: %w", name, err)
	}

	return a.telegramClient.SendMessage(ctx, chatID, messages.ReminderCancelled)
}
