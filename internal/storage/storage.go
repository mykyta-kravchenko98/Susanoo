package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/mykyta-kravchenko98/Susanoo/internal/helper"
)

type DocumentStore struct {
	client *s3.Client
	bucket string
	logger *slog.Logger
}

func NewDocumentStore(client *s3.Client, bucket string, logger *slog.Logger) *DocumentStore {
	return &DocumentStore{client: client, bucket: bucket, logger: logger}
}

func (s *DocumentStore) PutRaw(ctx context.Context, key string, data []byte) error {
	return s.put(ctx, key, data, "image/jpeg")
}

func (s *DocumentStore) PutPDF(ctx context.Context, key string, data []byte) error {
	return s.put(ctx, key, data, "application/pdf")
}

func (s *DocumentStore) GetObject(ctx context.Context, key string) ([]byte, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("get object %s: %w", key, err)
	}
	defer helper.Close(s.logger, out.Body)

	data, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, fmt.Errorf("read object body %s: %w", key, err)
	}
	return data, nil
}

// Move transfers the object from fromKey to toKey (S3 lacks a native rename operation—
// it involves copy + delete). It is used to move a PDF from Unsorted/ to
// {organization}/{year}/{filename}.pdf after the user has confirmed the classification.
func (s *DocumentStore) Move(ctx context.Context, fromKey, toKey string) error {
	copySource := fmt.Sprintf("%s/%s", s.bucket, fromKey)

	_, err := s.client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(s.bucket),
		Key:        aws.String(toKey),
		CopySource: aws.String(copySource),
	})
	if err != nil {
		return fmt.Errorf("copy object from %s to %s: %w", fromKey, toKey, err)
	}

	_, err = s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(fromKey),
	})
	if err != nil {
		return fmt.Errorf("delete old object %s after copy: %w", fromKey, err)
	}
	return nil
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
