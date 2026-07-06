//go:build integration

package main

import (
	"context"
	"fmt"
	"testing"

	"github.com/mykyta-kravchenko98/Susanoo/internal/session"
	"github.com/mykyta-kravchenko98/Susanoo/internal/telegram"
)

// TestConfirmSave_MoveFailure_StartOverRecovers exercises the partial-failure
// scenario from the project notes: the session transitions
// pending_confirmation -> saving, and then the S3 Move (CopyObject from
// Unsorted/ to the final key, then DeleteObject) fails. Here the failure is
// induced the simplest reliable way against a real S3 backend — the source
// PDF key was never actually written, so CopyObject 404s — rather than by
// mocking the S3 client, so the test is exercising the real AWS error path.
//
// Once that happens the session is stuck in status=saving with no code path
// that clears it automatically. This test asserts that "Start Over"
// (callbackRestart) unconditionally clears the session regardless of its
// status — Clear() has no ConditionExpression — and that a brand new session
// can be started immediately afterward with no leftover state.
func TestConfirmSave_MoveFailure_StartOverRecovers(t *testing.T) {
	ctx := context.Background()
	chatID := nextChatID()

	tg := &fakeTelegramClient{}
	llmClient := newFakeLLMClassifier()
	app := newTestApp(tg, llmClient)

	missingPDFKey := fmt.Sprintf("Unsorted/%d/does-not-exist.pdf", chatID)
	classification := `{"organization":"finanzamt-berlin","doc_type":"Steuerbescheid","filename":"steuerbescheid",` +
		`"summary":"x","summary_ru":"x","urgency":"medium"}`
	if err := app.sessions.SetPendingConfirmation(ctx, chatID, missingPDFKey, classification); err != nil {
		t.Fatalf("seed pending confirmation: %v", err)
	}

	if err := app.handleConfirmSave(ctx, chatID); err == nil {
		t.Fatalf("expected handleConfirmSave to fail because the source PDF was never written to S3")
	}

	stuck, err := app.sessions.Get(ctx, chatID)
	if err != nil {
		t.Fatalf("get session after failed save: %v", err)
	}
	if stuck == nil || stuck.Status != session.StatusSaving {
		t.Fatalf("expected session stuck in status=saving after the Move failure, got %+v", stuck)
	}

	// User taps "Start Over".
	if err := app.handleCallback(ctx, &telegram.CallbackQuery{
		ID:   "cb-1",
		Data: callbackRestart,
		Message: telegram.Message{
			Chat: telegram.Chat{ID: chatID},
		},
	}); err != nil {
		t.Fatalf("handle restart callback: %v", err)
	}

	cleared, err := app.sessions.Get(ctx, chatID)
	if err != nil {
		t.Fatalf("get session after restart: %v", err)
	}
	if cleared != nil {
		t.Fatalf("expected session to be gone after Start Over, got %+v", cleared)
	}

	// A fresh session must be usable right away — nothing about the old
	// "saving" status should leak into the new one, since Clear() does a
	// plain DeleteItem rather than a status transition.
	fresh, err := app.sessions.AppendRawKey(ctx, chatID, fmt.Sprintf("raw/%d/new-page-0.jpg", chatID))
	if err != nil {
		t.Fatalf("append raw key to fresh session: %v", err)
	}
	if fresh.Status != "" {
		t.Errorf("expected fresh session to have no status, got %q", fresh.Status)
	}
	if len(fresh.RawKeys) != 1 {
		t.Errorf("expected fresh session to have exactly 1 raw key, got %d", len(fresh.RawKeys))
	}

	// And the ordinary Done flow must work again from here — regression guard
	// against any future change that ties MarkAwaitingProcessing's guard to
	// something other than "does status already exist".
	if _, err := app.sessions.MarkAwaitingProcessing(ctx, chatID); err != nil {
		t.Errorf("expected fresh session to accept MarkAwaitingProcessing, got: %v", err)
	}
}
