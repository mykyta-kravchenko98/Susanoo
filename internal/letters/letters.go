package letters

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// Status distinguishes normal letters from ones the user has deleted via the
// bot but that are still within their 30-day soft-delete grace period (see
// the PendingDeletion/ S3 prefix + expires_at TTL below, and MarkPendingDeletion).
type Status string

const (
	StatusActive          Status = "active"
	StatusPendingDeletion Status = "pending_deletion"
)

// ErrNotFound is returned by Get when no letter exists with the given ID.
var ErrNotFound = errors.New("letter not found")

// chatIDIndex is the GSI used by Query to list a chat's letters newest-first
// (see infra/storage.tf: chat_id-received_date-index).
const chatIDIndex = "chat_id-received_date-index"

// orgYearIndex is the GSI used by the organization -> year -> letter
// drill-down (see infra/storage.tf: chat_id-org_year-index).
const orgYearIndex = "chat_id-org_year-index"

var unsafeKeyChars = regexp.MustCompile(`[^a-z0-9\-_]+`)

func SanitizeForKey(s string) string {
	lower := strings.ToLower(strings.TrimSpace(s))
	safe := unsafeKeyChars.ReplaceAllString(lower, "-")
	safe = strings.Trim(safe, "-")
	if safe == "" {
		return "unsorted"
	}
	return safe
}

// orgYear builds the synthetic sort key used by chat_id-org_year-index:
// "{organization-slug}#{year}". year is parsed out of receivedDate (ISO
// 8601, "2006-01-02"); if that fails to parse, orgYear returns "" so the
// letter simply won't appear in the drill-down GSI rather than being
// indexed under a bogus year - the rest of Put/Get/Query still work
// normally either way.
func orgYear(organization, receivedDate string) string {
	parsed, err := time.Parse("2006-01-02", receivedDate)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%s#%d", SanitizeForKey(organization), parsed.Year())
}

type Letter struct {
	LetterID         string `dynamodbav:"letter_id"`
	ChatID           int64  `dynamodbav:"chat_id"`
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
	CreatedAt        string `dynamodbav:"created_at"` // ISO 8601

	Status    Status `dynamodbav:"status,omitempty"`
	ExpiresAt int64  `dynamodbav:"expires_at,omitempty"`
	OrgYear   string `dynamodbav:"org_year,omitempty"`
}
type Store struct {
	client    *dynamodb.Client
	tableName string
}

func NewStore(client *dynamodb.Client, tableName string) *Store {
	return &Store{client: client, tableName: tableName}
}

func (s *Store) Put(ctx context.Context, letter Letter) error {
	if letter.Status == "" {
		letter.Status = StatusActive
	}
	letter.OrgYear = orgYear(letter.Organization, letter.ReceivedDate)

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

// Get fetches a single letter by ID, returning ErrNotFound if it doesn't
// exist (or has already been reclaimed by TTL).
func (s *Store) Get(ctx context.Context, letterID string) (*Letter, error) {
	out, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: &s.tableName,
		Key: map[string]types.AttributeValue{
			"letter_id": &types.AttributeValueMemberS{Value: letterID},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("get letter %s: %w", letterID, err)
	}
	if out.Item == nil {
		return nil, ErrNotFound
	}

	var letter Letter
	if err := attributevalue.UnmarshalMap(out.Item, &letter); err != nil {
		return nil, fmt.Errorf("unmarshal letter %s: %w", letterID, err)
	}
	return &letter, nil
}

func (s *Store) Query(ctx context.Context, chatID int64, limit int32) ([]Letter, error) {
	out, err := s.client.Query(ctx, &dynamodb.QueryInput{
		TableName:              &s.tableName,
		IndexName:              aws.String(chatIDIndex),
		KeyConditionExpression: aws.String("chat_id = :chat_id"),
		FilterExpression:       aws.String("attribute_not_exists(#status) OR #status <> :pending_deletion"),
		ExpressionAttributeNames: map[string]string{
			"#status": "status",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":chat_id":          &types.AttributeValueMemberN{Value: strconv.FormatInt(chatID, 10)},
			":pending_deletion": &types.AttributeValueMemberS{Value: string(StatusPendingDeletion)},
		},
		ScanIndexForward: aws.Bool(false), // newest received_date first
		Limit:            aws.Int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("query letters for chat %d: %w", chatID, err)
	}

	result := make([]Letter, 0, len(out.Items))
	for _, item := range out.Items {
		var letter Letter
		if err := attributevalue.UnmarshalMap(item, &letter); err != nil {
			return nil, fmt.Errorf("unmarshal letter item: %w", err)
		}
		result = append(result, letter)
	}
	return result, nil
}

func (s *Store) MarkPendingDeletion(ctx context.Context, letterID, newS3Key string, expiresAt time.Time) error {
	_, err := s.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: &s.tableName,
		Key: map[string]types.AttributeValue{
			"letter_id": &types.AttributeValueMemberS{Value: letterID},
		},
		UpdateExpression: aws.String("SET #status = :status, expires_at = :expires_at, s3_key = :s3_key"),
		ExpressionAttributeNames: map[string]string{
			"#status": "status",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":status":     &types.AttributeValueMemberS{Value: string(StatusPendingDeletion)},
			":expires_at": &types.AttributeValueMemberN{Value: strconv.FormatInt(expiresAt.Unix(), 10)},
			":s3_key":     &types.AttributeValueMemberS{Value: newS3Key},
		},
	})
	if err != nil {
		return fmt.Errorf("mark letter %s pending deletion: %w", letterID, err)
	}
	return nil
}

type OrganizationSummary struct {
	Slug  string
	Name  string
	Count int
}

func (s *Store) QueryOrganizations(ctx context.Context, chatID int64) ([]OrganizationSummary, error) {
	items, err := s.queryOrgYear(ctx, chatID, "")
	if err != nil {
		return nil, err
	}

	order := make([]string, 0)
	bySlug := make(map[string]*OrganizationSummary)
	for _, letter := range items {
		slug := SanitizeForKey(letter.Organization)
		summary, ok := bySlug[slug]
		if !ok {
			summary = &OrganizationSummary{Slug: slug, Name: letter.Organization}
			bySlug[slug] = summary
			order = append(order, slug)
		}
		summary.Count++
	}

	result := make([]OrganizationSummary, 0, len(order))
	for _, slug := range order {
		result = append(result, *bySlug[slug])
	}
	return result, nil
}

func (s *Store) QueryYears(ctx context.Context, chatID int64, orgSlug string) ([]int, error) {
	items, err := s.queryOrgYear(ctx, chatID, orgSlug+"#")
	if err != nil {
		return nil, err
	}

	seen := make(map[int]bool)
	var years []int
	for _, letter := range items {
		year, ok := yearFromOrgYear(letter.OrgYear)
		if !ok {
			continue
		}
		if !seen[year] {
			seen[year] = true
			years = append(years, year)
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(years)))
	return years, nil
}

func (s *Store) QueryByOrgYear(ctx context.Context, chatID int64, orgSlug string, year int) ([]Letter, error) {
	return s.queryOrgYear(ctx, chatID, fmt.Sprintf("%s#%d", orgSlug, year))
}

func (s *Store) queryOrgYear(ctx context.Context, chatID int64, prefix string) ([]Letter, error) {
	keyCondition := "chat_id = :chat_id"
	exprValues := map[string]types.AttributeValue{
		":chat_id":          &types.AttributeValueMemberN{Value: strconv.FormatInt(chatID, 10)},
		":pending_deletion": &types.AttributeValueMemberS{Value: string(StatusPendingDeletion)},
	}
	if prefix != "" {
		keyCondition += " AND begins_with(org_year, :prefix)"
		exprValues[":prefix"] = &types.AttributeValueMemberS{Value: prefix}
	}

	out, err := s.client.Query(ctx, &dynamodb.QueryInput{
		TableName:              &s.tableName,
		IndexName:              aws.String(orgYearIndex),
		KeyConditionExpression: aws.String(keyCondition),
		FilterExpression:       aws.String("attribute_not_exists(#status) OR #status <> :pending_deletion"),
		ExpressionAttributeNames: map[string]string{
			"#status": "status",
		},
		ExpressionAttributeValues: exprValues,
	})
	if err != nil {
		return nil, fmt.Errorf("query letters by org_year for chat %d (prefix %q): %w", chatID, prefix, err)
	}

	result := make([]Letter, 0, len(out.Items))
	for _, item := range out.Items {
		var letter Letter
		if err := attributevalue.UnmarshalMap(item, &letter); err != nil {
			return nil, fmt.Errorf("unmarshal letter item: %w", err)
		}
		result = append(result, letter)
	}
	return result, nil
}

// yearFromOrgYear extracts the year suffix from a "{slug}#{year}" org_year
// value. Returns false if v doesn't look like that shape at all (shouldn't
// happen for anything Put wrote, but Query results are still just strings
// from DynamoDB's point of view).
func yearFromOrgYear(v string) (int, bool) {
	idx := strings.LastIndexByte(v, '#')
	if idx < 0 {
		return 0, false
	}
	year, err := strconv.Atoi(v[idx+1:])
	if err != nil {
		return 0, false
	}
	return year, true
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
