package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

var (
	sqsClient *sqs.Client
	queueURL  string
	logger    *slog.Logger
)

func init() {
	logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))

	queueURL = os.Getenv("SQS_QUEUE_URL")
	if queueURL == "" {
		panic("SQS_QUEUE_URL env var is not set")
	}

	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		panic(fmt.Sprintf("failed to load AWS config: %v", err))
	}
	sqsClient = sqs.NewFromConfig(cfg)
}

type minimalUpdateCheck struct {
	UpdateID int64 `json:"update_id"`
}

func handler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if req.Body == "" {
		logger.WarnContext(ctx, "empty request body")
		return respond(400, "empty body"), nil
	}

	var check minimalUpdateCheck
	if err := json.Unmarshal([]byte(req.Body), &check); err != nil {
		logger.ErrorContext(ctx, "invalid JSON in webhook body", slog.String("error", err.Error()))
		return respond(200, "ignored"), nil
	}

	if _, err := sqsClient.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    aws.String(queueURL),
		MessageBody: aws.String(req.Body),
	}); err != nil {
		logger.ErrorContext(ctx, "failed to send message to SQS",
			slog.String("error", err.Error()),
			slog.Int64("update_id", check.UpdateID),
		)
		return respond(500, "internal error"), errors.New("sqs send failed")
	}

	logger.InfoContext(ctx, "update queued", slog.Int64("update_id", check.UpdateID))
	return respond(200, "ok"), nil
}

func respond(statusCode int, body string) events.APIGatewayV2HTTPResponse {
	return events.APIGatewayV2HTTPResponse{
		StatusCode: statusCode,
		Body:       body,
		Headers:    map[string]string{"Content-Type": "text/plain"},
	}
}

func main() {
	lambda.Start(handler)
}