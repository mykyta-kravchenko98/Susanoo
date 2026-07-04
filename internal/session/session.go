package session

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

const ttlDuration = 30 * time.Minute
const pendingTTLDuration = 30 * time.Minute

const (
	StatusAwaitingProcessing  = "awaiting_processing"
	StatusPendingConfirmation = "pending_confirmation"
	StatusSaving              = "saving"
)

var ErrAlreadyProcessing = errors.New("session is already past the collecting-photos stage")
var ErrNotPending = errors.New("session is not in pending_confirmation state")

type Session struct {
	ChatID         int64    `dynamodbav:"chat_id"`
	RawKeys        []string `dynamodbav:"raw_keys"`
	ExpiresAt      int64    `dynamodbav:"expires_at"`
	Status         string   `dynamodbav:"status,omitempty"`
	PDFKey         string   `dynamodbav:"pdf_key,omitempty"`
	Classification string   `dynamodbav:"classification,omitempty"` // JSON-encoded llm.ExtractedFields
}

type Store struct {
	client    *dynamodb.Client
	tableName string
}

func NewStore(client *dynamodb.Client, tableName string) *Store {
	return &Store{client: client, tableName: tableName}
}

func (s *Store) Get(ctx context.Context, chatID int64) (*Session, error) {
	key, err := attributevalue.MarshalMap(map[string]int64{"chat_id": chatID})
	if err != nil {
		return nil, fmt.Errorf("marshal key: %w", err)
	}

	out, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.tableName),
		Key:       key,
	})
	if err != nil {
		return nil, fmt.Errorf("get session item: %w", err)
	}
	if out.Item == nil {
		return nil, nil
	}

	var sess Session
	if err := attributevalue.UnmarshalMap(out.Item, &sess); err != nil {
		return nil, fmt.Errorf("unmarshal session: %w", err)
	}

	if sess.ExpiresAt < time.Now().Unix() {
		return nil, nil
	}
	return &sess, nil
}

func (s *Store) AppendRawKey(ctx context.Context, chatID int64, rawKey string) (*Session, error) {
	expiresAt := time.Now().Add(ttlDuration).Unix()

	key, err := attributevalue.MarshalMap(map[string]int64{"chat_id": chatID})
	if err != nil {
		return nil, fmt.Errorf("marshal key: %w", err)
	}

	out, err := s.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:        aws.String(s.tableName),
		Key:              key,
		UpdateExpression: aws.String("SET raw_keys = list_append(if_not_exists(raw_keys, :empty_list), :new_key), expires_at = :expires_at"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":new_key":    &types.AttributeValueMemberL{Value: []types.AttributeValue{&types.AttributeValueMemberS{Value: rawKey}}},
			":empty_list": &types.AttributeValueMemberL{Value: []types.AttributeValue{}},
			":expires_at": &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", expiresAt)},
		},
		ReturnValues: types.ReturnValueAllNew,
	})
	if err != nil {
		return nil, fmt.Errorf("append raw key to session: %w", err)
	}

	var sess Session
	if err := attributevalue.UnmarshalMap(out.Attributes, &sess); err != nil {
		return nil, fmt.Errorf("unmarshal updated session: %w", err)
	}
	return &sess, nil
}

func (s *Store) MarkAwaitingProcessing(ctx context.Context, chatID int64) (*Session, error) {
	key, err := attributevalue.MarshalMap(map[string]int64{"chat_id": chatID})
	if err != nil {
		return nil, fmt.Errorf("marshal key: %w", err)
	}

	out, err := s.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:           aws.String(s.tableName),
		Key:                 key,
		UpdateExpression:    aws.String("SET #status = :awaiting"),
		ConditionExpression: aws.String("attribute_not_exists(#status)"),
		ExpressionAttributeNames: map[string]string{
			"#status": "status",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":awaiting": &types.AttributeValueMemberS{Value: StatusAwaitingProcessing},
		},
		ReturnValues: types.ReturnValueAllNew,
	})
	if err != nil {
		var condErr *types.ConditionalCheckFailedException
		if errors.As(err, &condErr) {
			return nil, ErrAlreadyProcessing
		}
		return nil, fmt.Errorf("mark session awaiting processing: %w", err)
	}

	var sess Session
	if err := attributevalue.UnmarshalMap(out.Attributes, &sess); err != nil {
		return nil, fmt.Errorf("unmarshal session: %w", err)
	}
	return &sess, nil
}

func (s *Store) MarkSaving(ctx context.Context, chatID int64) (*Session, error) {
	key, err := attributevalue.MarshalMap(map[string]int64{"chat_id": chatID})
	if err != nil {
		return nil, fmt.Errorf("marshal key: %w", err)
	}

	out, err := s.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:           aws.String(s.tableName),
		Key:                 key,
		UpdateExpression:    aws.String("SET #status = :saving"),
		ConditionExpression: aws.String("#status = :pending"),
		ExpressionAttributeNames: map[string]string{
			"#status": "status",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":saving":  &types.AttributeValueMemberS{Value: StatusSaving},
			":pending": &types.AttributeValueMemberS{Value: StatusPendingConfirmation},
		},
		ReturnValues: types.ReturnValueAllNew,
	})
	if err != nil {
		var condErr *types.ConditionalCheckFailedException
		if errors.As(err, &condErr) {
			return nil, ErrNotPending
		}
		return nil, fmt.Errorf("mark session saving: %w", err)
	}

	var sess Session
	if err := attributevalue.UnmarshalMap(out.Attributes, &sess); err != nil {
		return nil, fmt.Errorf("unmarshal session: %w", err)
	}
	return &sess, nil
}

func (s *Store) SetPendingConfirmation(ctx context.Context, chatID int64, pdfKey, classificationJSON string) error {
	expiresAt := time.Now().Add(pendingTTLDuration).Unix()

	key, err := attributevalue.MarshalMap(map[string]int64{"chat_id": chatID})
	if err != nil {
		return fmt.Errorf("marshal key: %w", err)
	}

	_, err = s.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:        aws.String(s.tableName),
		Key:              key,
		UpdateExpression: aws.String("SET #status = :status, pdf_key = :pdf_key, classification = :classification, expires_at = :expires_at"),
		ExpressionAttributeNames: map[string]string{
			"#status": "status",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":status":         &types.AttributeValueMemberS{Value: StatusPendingConfirmation},
			":pdf_key":        &types.AttributeValueMemberS{Value: pdfKey},
			":classification": &types.AttributeValueMemberS{Value: classificationJSON},
			":expires_at":     &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", expiresAt)},
		},
	})
	if err != nil {
		return fmt.Errorf("set pending confirmation: %w", err)
	}
	return nil
}

func (s *Store) Clear(ctx context.Context, chatID int64) error {
	key, err := attributevalue.MarshalMap(map[string]int64{"chat_id": chatID})
	if err != nil {
		return fmt.Errorf("marshal key: %w", err)
	}

	_, err = s.client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(s.tableName),
		Key:       key,
	})
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}
