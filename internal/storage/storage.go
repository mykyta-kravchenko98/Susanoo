package storage

import (
	"bytes"
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type DocumentStore struct {
	client *s3.Client
	bucket string
}

func NewDocumentStore(client *s3.Client, bucket string) *DocumentStore {
	return &DocumentStore{client: client, bucket: bucket}
}

func (s *DocumentStore) PutRaw(ctx context.Context, key string, data []byte) error {
	return s.put(ctx, key, data, "image/jpeg")
}

func (s *DocumentStore) PutPDF(ctx context.Context, key string, data []byte) error {
	return s.put(ctx, key, data, "application/pdf")
}

func (s *DocumentStore) put(ctx context.Context, key string, data []byte, contentType string) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(data),
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return fmt.Errorf("put object %s: %w", key, err)
	}
	return nil
}