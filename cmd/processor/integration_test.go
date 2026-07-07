//go:build integration

// Integration tests for the processor Lambda, run against a real LocalStack
// container via testcontainers-go instead of hand-rolled AWS mocks. Only
// TelegramClient and LLMClassifier are faked (see fakes_test.go) — DynamoDB,
// S3 and SQS are the real AWS SDK v2 clients pointed at LocalStack, so the
// tests exercise the actual ConditionExpression / CopyObject+DeleteObject /
// SQS semantics the production code depends on.
//
// Run with:
//
//	go test -tags=integration ./cmd/processor/... -v
//
// Requires Docker. Pinned to localstack/localstack:4.4.0, the last LocalStack
// image that starts without a LOCALSTACK_AUTH_TOKEN (LocalStack moved to
// authenticated single-image releases starting with the 2026.03.0 release on
// 2026-03-23). Bump this once the project has a LocalStack auth token wired
// into CI.
package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"log/slog"
	"net"
	"os"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/localstack"

	"github.com/mykyta-kravchenko98/Susanoo/internal/letters"
	"github.com/mykyta-kravchenko98/Susanoo/internal/reminders"
	"github.com/mykyta-kravchenko98/Susanoo/internal/session"
	"github.com/mykyta-kravchenko98/Susanoo/internal/storage"
)

const (
	localstackImage   = "localstack/localstack:4.4.0"
	testSessionsTable = "test-susanoo-sessions"
	testLettersTable  = "test-susanoo-letters"
	testBucket        = "test-susanoo-documents"
	testRegion        = "eu-central-1"
)

type testEnvironment struct {
	ddb                *dynamodb.Client
	s3                 *s3.Client
	sqs                *sqs.Client
	imagesToProcessURL string
}

var testEnv testEnvironment

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

// runTests is a separate function (rather than inlining into TestMain) so that
// deferred container cleanup actually runs — os.Exit inside TestMain would
// otherwise skip it.
func runTests(m *testing.M) int {
	ctx := context.Background()

	container, err := localstack.Run(ctx, localstackImage)
	defer func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			fmt.Fprintf(os.Stderr, "failed to terminate localstack container: %v\n", err)
		}
	}()
	if err != nil {
		fmt.Fprintf(os.Stderr, "start localstack: %v\n", err)
		return 1
	}

	endpoint, err := localstackEndpoint(ctx, container)
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve localstack endpoint: %v\n", err)
		return 1
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(testRegion),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load aws config: %v\n", err)
		return 1
	}

	testEnv.ddb = dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})
	testEnv.s3 = s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})
	testEnv.sqs = sqs.NewFromConfig(cfg, func(o *sqs.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})

	if err := setupInfra(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "setup infra: %v\n", err)
		return 1
	}

	return m.Run()
}

// localstackEndpoint resolves the host:port the test process (running on the
// Docker host, not inside the LocalStack network) should use to reach the
// container's edge port (4566), following the pattern from the localstack
// module docs (https://golang.testcontainers.org/modules/localstack/).
func localstackEndpoint(ctx context.Context, c *localstack.LocalStackContainer) (string, error) {
	mappedPort, err := c.MappedPort(ctx, "4566/tcp")
	if err != nil {
		return "", fmt.Errorf("get mapped port: %w", err)
	}

	provider, err := testcontainers.NewDockerProvider()
	if err != nil {
		return "", fmt.Errorf("create docker provider: %w", err)
	}
	defer func() { _ = provider.Close() }()

	host, err := provider.DaemonHost(ctx)
	if err != nil {
		return "", fmt.Errorf("get daemon host: %w", err)
	}

	return "http://" + net.JoinHostPort(host, mappedPort.Port()), nil
}

func setupInfra(ctx context.Context) error {
	_, err := testEnv.ddb.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: aws.String(testSessionsTable),
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("chat_id"), AttributeType: ddbtypes.ScalarAttributeTypeN},
		},
		KeySchema: []ddbtypes.KeySchemaElement{
			{AttributeName: aws.String("chat_id"), KeyType: ddbtypes.KeyTypeHash},
		},
		BillingMode: ddbtypes.BillingModePayPerRequest,
	})
	if err != nil {
		return fmt.Errorf("create sessions table: %w", err)
	}

	_, err = testEnv.ddb.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: aws.String(testLettersTable),
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("letter_id"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		KeySchema: []ddbtypes.KeySchemaElement{
			{AttributeName: aws.String("letter_id"), KeyType: ddbtypes.KeyTypeHash},
		},
		BillingMode: ddbtypes.BillingModePayPerRequest,
	})
	if err != nil {
		return fmt.Errorf("create letters table: %w", err)
	}

	// S3 requires an explicit LocationConstraint for every region except
	// us-east-1 — omitting it is what produced the
	// IllegalLocationConstraintException against eu-central-1.
	if _, err := testEnv.s3.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(testBucket),
		CreateBucketConfiguration: &s3types.CreateBucketConfiguration{
			LocationConstraint: s3types.BucketLocationConstraint(testRegion),
		},
	}); err != nil {
		return fmt.Errorf("create bucket: %w", err)
	}

	out, err := testEnv.sqs.CreateQueue(ctx, &sqs.CreateQueueInput{QueueName: aws.String("test-images-to-process")})
	if err != nil {
		return fmt.Errorf("create images-to-process queue: %w", err)
	}
	testEnv.imagesToProcessURL = *out.QueueUrl

	return nil
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestApp builds an App wired to the shared LocalStack-backed stores, with
// caller-supplied fake Telegram/LLM clients. All tests share the same
// DynamoDB tables / S3 bucket / SQS queue (recreating them per test would be
// slow), so tests must use nextChatID() to keep their data from colliding.
//
// reminderScheduler is backed by a throwaway fakeSchedulerAPI rather than
// real EventBridge Scheduler — no current test needs to assert on scheduled
// reminders, this just keeps App.reminderScheduler non-nil so any test whose
// classification happens to include a future deadline doesn't nil-panic.
func newTestApp(tg *fakeTelegramClient, llmClient *fakeLLMClassifier) *App {
	logger := discardLogger()
	return &App{
		sessions:           session.NewStore(testEnv.ddb, testSessionsTable),
		docs:               storage.NewDocumentStore(testEnv.s3, testBucket, logger),
		letters:            letters.NewStore(testEnv.ddb, testLettersTable),
		llmClient:          llmClient,
		telegramClient:     tg,
		sqsClient:          testEnv.sqs,
		imagesToProcessURL: testEnv.imagesToProcessURL,
		reminderScheduler:  reminders.NewScheduler(&fakeSchedulerAPI{}, "test-reminders", "arn:aws:lambda:eu-central-1:000000000000:function:test-reminder-sender", "arn:aws:iam::000000000000:role/test-scheduler"),
		logger:             logger,
	}
}

var chatIDCounter int64 = 1_000_000

// nextChatID hands out a fresh chat ID for each test so tests running against
// the shared LocalStack tables never see each other's sessions.
func nextChatID() int64 {
	return atomic.AddInt64(&chatIDCounter, 1)
}

// putTestJPEG writes a tiny but structurally valid JPEG to the given S3 key,
// so pdfbuilder.BuildFromJPEGs (which shells out to pdfcpu's image import) has
// something real to decode.
func (e *testEnvironment) putTestJPEG(ctx context.Context, key string) error {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		return fmt.Errorf("encode test jpeg: %w", err)
	}

	_, err := e.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(testBucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(buf.Bytes()),
		ContentType: aws.String("image/jpeg"),
	})
	if err != nil {
		return fmt.Errorf("put test jpeg %s: %w", key, err)
	}
	return nil
}
