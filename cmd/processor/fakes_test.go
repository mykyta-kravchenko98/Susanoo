//go:build integration

package main

import (
	"context"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/scheduler"
	"github.com/aws/aws-sdk-go-v2/service/scheduler/types"

	"github.com/mykyta-kravchenko98/Susanoo/internal/llm"
	"github.com/mykyta-kravchenko98/Susanoo/internal/telegram"
)

type sentMessage struct {
	chatID  int64
	text    string
	buttons []telegram.InlineButton
	rows    [][]telegram.InlineButton
}

type sentDocument struct {
	chatID   int64
	filename string
	data     []byte
}

// fakeTelegramClient satisfies the TelegramClient interface without making
// any network calls. It records every SendMessage call so tests can assert on
// what the user would have seen, and returns canned data for
// GetFilePath/DownloadFile.
type fakeTelegramClient struct {
	mu        sync.Mutex
	sent      []sentMessage
	documents []sentDocument
	filePath  string
	fileData  []byte
}

func (f *fakeTelegramClient) SendMessage(_ context.Context, chatID int64, text string, buttons ...telegram.InlineButton) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentMessage{chatID: chatID, text: text, buttons: buttons})
	return nil
}

func (f *fakeTelegramClient) SendMessageWithRows(_ context.Context, chatID int64, text string, rows [][]telegram.InlineButton) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentMessage{chatID: chatID, text: text, rows: rows})
	return nil
}

func (f *fakeTelegramClient) SendDocument(_ context.Context, chatID int64, filename string, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.documents = append(f.documents, sentDocument{chatID: chatID, filename: filename, data: data})
	return nil
}

func (f *fakeTelegramClient) GetFilePath(_ context.Context, _ string) (string, error) {
	return f.filePath, nil
}

func (f *fakeTelegramClient) DownloadFile(_ context.Context, _ string) ([]byte, error) {
	return f.fileData, nil
}

func (f *fakeTelegramClient) AnswerCallbackQuery(_ context.Context, _ string) error {
	return nil
}

func (f *fakeTelegramClient) SetMyCommands(_ context.Context, _ []telegram.BotCommand) error {
	return nil
}

func (f *fakeTelegramClient) messages() []sentMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sentMessage, len(f.sent))
	copy(out, f.sent)
	return out
}

func (f *fakeTelegramClient) sentDocuments() []sentDocument {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sentDocument, len(f.documents))
	copy(out, f.documents)
	return out
}

// fakeLLMClassifier satisfies the LLMClassifier interface, returning a fixed,
// configurable result instead of calling Anthropic. It counts calls so
// redelivery tests can assert on how many times classification actually ran.
type fakeLLMClassifier struct {
	mu     sync.Mutex
	calls  int
	fields llm.ExtractedFields
	err    error
}

func newFakeLLMClassifier() *fakeLLMClassifier {
	return &fakeLLMClassifier{
		fields: llm.ExtractedFields{
			Organization: "finanzamt-berlin",
			DocType:      "Steuerbescheid",
			Filename:     "steuerbescheid-2026",
			Summary:      "Tax assessment notice",
			SummaryRU:    "Уведомление о начислении налога",
			Urgency:      "medium",
		},
	}
}

func (f *fakeLLMClassifier) ClassifyLetter(_ context.Context, _ [][]byte, _ string) (*llm.ExtractedFields, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	fieldsCopy := f.fields
	return &fieldsCopy, nil
}

func (f *fakeLLMClassifier) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// fakeSchedulerAPI satisfies reminders.SchedulerAPI without calling real
// EventBridge Scheduler (LocalStack's community image doesn't reliably cover
// the scheduler service, and this only needs to prove handleConfirmSave calls
// ScheduleAll with the right shape, not that AWS itself accepts the request).
type fakeSchedulerAPI struct {
	mu      sync.Mutex
	calls   []*scheduler.CreateScheduleInput
	deleted map[string]bool
	err     error
}

func (f *fakeSchedulerAPI) CreateSchedule(_ context.Context, params *scheduler.CreateScheduleInput, _ ...func(*scheduler.Options)) (*scheduler.CreateScheduleOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, params)
	if f.err != nil {
		return nil, f.err
	}
	return &scheduler.CreateScheduleOutput{}, nil
}

func (f *fakeSchedulerAPI) createScheduleCalls() []*scheduler.CreateScheduleInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*scheduler.CreateScheduleInput, len(f.calls))
	copy(out, f.calls)
	return out
}

func (f *fakeSchedulerAPI) ListSchedules(_ context.Context, params *scheduler.ListSchedulesInput, _ ...func(*scheduler.Options)) (*scheduler.ListSchedulesOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	prefix := aws.ToString(params.NamePrefix)

	var summaries []types.ScheduleSummary
	seen := make(map[string]bool)
	for _, c := range f.calls {
		name := aws.ToString(c.Name)
		if name == "" || seen[name] || f.deleted[name] {
			continue
		}
		if prefix != "" && !strings.HasPrefix(name, prefix) {
			continue
		}
		seen[name] = true
		summaries = append(summaries, types.ScheduleSummary{Name: aws.String(name)})
	}
	return &scheduler.ListSchedulesOutput{Schedules: summaries}, nil
}

func (f *fakeSchedulerAPI) DeleteSchedule(_ context.Context, params *scheduler.DeleteScheduleInput, _ ...func(*scheduler.Options)) (*scheduler.DeleteScheduleOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleted == nil {
		f.deleted = make(map[string]bool)
	}
	f.deleted[aws.ToString(params.Name)] = true
	return &scheduler.DeleteScheduleOutput{}, nil
}
