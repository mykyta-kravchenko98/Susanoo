//go:build integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"

	"github.com/mykyta-kravchenko98/Susanoo/internal/session"
)

// TestHandleProcessedImages_Redelivery documents what actually happens today
// if SQS redelivers the same processed-images message — e.g. the Lambda
// finished the work but timed out or errored before the message was deleted
// from the queue, which SQS's at-least-once delivery guarantee makes
// possible. The pipeline does NOT dedupe on message ID: each delivery
// re-assembles the PDF and re-calls the LLM from scratch. This test pins that
// behavior down as a known, understood tradeoff rather than an assumption —
// and confirms redelivery degrades safely (no crash, no corrupted session,
// user always sees one consistent preview) rather than silently misbehaving.
//
// If you add message-ID based deduping later, this test should start
// failing at the callCount()==2 assertion — flip it to 1 at that point.
func TestHandleProcessedImages_Redelivery(t *testing.T) {
	ctx := context.Background()
	chatID := nextChatID()

	processedKey := fmt.Sprintf("processed/%d/page-0.jpg", chatID)
	if err := testEnv.putTestJPEG(ctx, processedKey); err != nil {
		t.Fatalf("seed processed image: %v", err)
	}

	tg := &fakeTelegramClient{}
	llmClient := newFakeLLMClassifier()
	app := newTestApp(tg, llmClient)

	body, err := json.Marshal(processedImagesMessage{ChatID: chatID, ProcessedKeys: []string{processedKey}})
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}
	record := events.SQSMessage{Body: string(body)}

	if err := app.handleRecord(ctx, record); err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	firstSession, err := app.sessions.Get(ctx, chatID)
	if err != nil {
		t.Fatalf("get session after first delivery: %v", err)
	}
	if firstSession == nil || firstSession.Status != session.StatusPendingConfirmation {
		t.Fatalf("expected pending_confirmation after first delivery, got %+v", firstSession)
	}
	firstPDFKey := firstSession.PDFKey

	// pdfKey embeds a second-resolution timestamp (handlers.go:
	// receivedAt.Format("20060102T150405")). Real SQS redelivery only happens
	// after the processed-images queue's 90s visibility timeout elapses (see
	// infra/queue.tf), so two real deliveries are always seconds apart. A
	// back-to-back call in a test would collide on that same timestamp and
	// silently overwrite the first PDF at an identical key — which is a
	// second-resolution artifact of this test, not a redelivery race in
	// production. Sleeping past the second boundary keeps the two deliveries
	// realistic instead of accidentally exercising that unrelated collision.
	time.Sleep(1100 * time.Millisecond)

	// Simulate SQS redelivering the exact same message body (at-least-once
	// delivery semantics — the record is byte-identical to the first one).
	if err := app.handleRecord(ctx, record); err != nil {
		t.Fatalf("second (redelivered) delivery: %v", err)
	}
	secondSession, err := app.sessions.Get(ctx, chatID)
	if err != nil {
		t.Fatalf("get session after second delivery: %v", err)
	}
	if secondSession == nil || secondSession.Status != session.StatusPendingConfirmation {
		t.Fatalf("expected pending_confirmation after redelivery, got %+v", secondSession)
	}

	if got := llmClient.callCount(); got != 2 {
		t.Errorf("expected 2 LLM calls (no dedupe implemented today), got %d", got)
	}

	if secondSession.PDFKey == firstPDFKey {
		t.Errorf("expected redelivery to produce a distinct PDF key (each delivery independently builds "+
			"and stores its own PDF under Unsorted/); got the same key %q both times, which would mean the "+
			"second build silently clobbered the first instead of leaving two separate objects", firstPDFKey)
	}

	// The session always reflects the *latest* delivery's classification, so
	// the user is shown one consistent Save/Fix prompt instead of racing
	// between two. The orphaned first PDF is left under Unsorted/, which has
	// a 7-day S3 lifecycle rule (see infra/storage.tf), so it isn't a
	// permanent storage leak — but it is wasted LLM spend until dedupe exists.
	msgs := tg.messages()
	if len(msgs) != 2 {
		t.Errorf("expected 2 classification-preview messages sent to the user (one per delivery), got %d", len(msgs))
	}
}
