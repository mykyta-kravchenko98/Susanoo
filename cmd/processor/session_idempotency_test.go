//go:build integration

package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/mykyta-kravchenko98/Susanoo/internal/session"
)

// TestMarkAwaitingProcessing_ConcurrentDoneTaps verifies that if the user
// double-taps "Done" (or Telegram redelivers the callback update, which it
// does under at-least-once delivery), only one of the concurrent
// MarkAwaitingProcessing calls wins the atomic transition. This is the guard
// that stops the same batch of photos from being sent to the
// images-to-process queue twice.
func TestMarkAwaitingProcessing_ConcurrentDoneTaps(t *testing.T) {
	ctx := context.Background()
	chatID := nextChatID()
	store := session.NewStore(testEnv.ddb, testSessionsTable)

	for i := 0; i < 3; i++ {
		if _, err := store.AppendRawKey(ctx, chatID, fmt.Sprintf("raw/%d/page-%d.jpg", chatID, i)); err != nil {
			t.Fatalf("seed raw key %d: %v", i, err)
		}
	}

	const attempts = 20
	var wg sync.WaitGroup
	results := make([]error, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := store.MarkAwaitingProcessing(ctx, chatID)
			results[idx] = err
		}(i)
	}
	wg.Wait()

	successes, alreadyProcessing, unexpected := 0, 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, session.ErrAlreadyProcessing):
			alreadyProcessing++
		default:
			unexpected++
			t.Errorf("unexpected error: %v", err)
		}
	}

	if successes != 1 {
		t.Errorf("expected exactly 1 successful transition out of %d concurrent attempts, got %d", attempts, successes)
	}
	if alreadyProcessing != attempts-1 {
		t.Errorf("expected %d ErrAlreadyProcessing results, got %d", attempts-1, alreadyProcessing)
	}
	if unexpected != 0 {
		t.Errorf("got %d unexpected (non-ErrAlreadyProcessing) errors", unexpected)
	}

	final, err := store.Get(ctx, chatID)
	if err != nil {
		t.Fatalf("get final session state: %v", err)
	}
	if final == nil || final.Status != session.StatusAwaitingProcessing {
		t.Fatalf("expected final status=awaiting_processing, got %+v", final)
	}
	if len(final.RawKeys) != 3 {
		t.Errorf("expected the 3 seeded raw keys to survive the race untouched, got %d", len(final.RawKeys))
	}
}

// TestMarkSaving_ConcurrentConfirmTaps mirrors the above for the "Save"
// confirmation step: only one concurrent Save tap should be allowed to move
// pending_confirmation -> saving, which matters because handleConfirmSave
// moves the PDF in S3 immediately after — doing that twice concurrently could
// otherwise corrupt or duplicate the move.
func TestMarkSaving_ConcurrentConfirmTaps(t *testing.T) {
	ctx := context.Background()
	chatID := nextChatID()
	store := session.NewStore(testEnv.ddb, testSessionsTable)

	if err := store.SetPendingConfirmation(ctx, chatID, "Unsorted/dummy.pdf", `{"organization":"test"}`); err != nil {
		t.Fatalf("seed pending confirmation: %v", err)
	}

	const attempts = 20
	var wg sync.WaitGroup
	results := make([]error, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := store.MarkSaving(ctx, chatID)
			results[idx] = err
		}(i)
	}
	wg.Wait()

	successes, notPending, unexpected := 0, 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, session.ErrNotPending):
			notPending++
		default:
			unexpected++
			t.Errorf("unexpected error: %v", err)
		}
	}

	if successes != 1 {
		t.Errorf("expected exactly 1 successful transition out of %d concurrent attempts, got %d", attempts, successes)
	}
	if notPending != attempts-1 {
		t.Errorf("expected %d ErrNotPending results, got %d", attempts-1, notPending)
	}
	if unexpected != 0 {
		t.Errorf("got %d unexpected (non-ErrNotPending) errors", unexpected)
	}

	final, err := store.Get(ctx, chatID)
	if err != nil {
		t.Fatalf("get final session state: %v", err)
	}
	if final == nil || final.Status != session.StatusSaving {
		t.Fatalf("expected final status=saving, got %+v", final)
	}
}
