package letters

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

type Letter struct {
	LetterID         string `dynamodbav:"letter_id"`
	Organization     string `dynamodbav:"organization"`
	ReceivedDate     string `dynamodbav:"received_date"` // ISO 8601
	DocType          string `dynamodbav:"doc_type"`
	Filename         string `dynamodbav:"filename"`
	Summary          string `dynamodbav:"summary"`
	SummaryRU        string `dynamodbav:"summary_ru"`
	Deadline         string `dynamodbav:"deadline,omitempty"`
	ActionRequired   string `dynamodbav:"action_required,omitempty"`
	ActionRequiredRU string `dynamodbav:"action_required_ru,omitempty"`
	Urgency          string `dynamodbav:"urgency"`
	S3Key            string `dynamodbav:"s3_key"`
	CreatedAt        string `dynamodbav:"created_at"` // ISO 8601 timestamp
}

type Store struct {
	client    *dynamodb.Client
	tableName string
}

func NewStore(client *dynamodb.Client, tableName string) *Store {
	return &Store{client: client, tableName: tableName}
}

func (s *Store) Put(ctx context.Context, letter Letter) error {
	item, err := attributevalue.MarshalMap(letter)
	if err != nil {
		return fmt.Errorf("marshal letter: %w", err)
	}

	_, err = s.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: &s.tableName,
		Item:      item,
	})
	if err != nil {
		return fmt.Errorf("put letter item: %w", err)
	}
	return nil
}

// NewLetterID generates a random letter identifier. It is not an RFC4122 UUID (to avoid
// adding an extra dependency for just one function)—simply 16 random bytes in hex,
// which is more than sufficient to ensure uniqueness for a volume of "dozens of letters per month."
func NewLetterID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate letter id: %w", err)
	}
	return hex.EncodeToString(b), nil
}