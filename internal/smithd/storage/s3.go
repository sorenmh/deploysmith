package storage

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
)

// S3Storage handles S3 operations for version storage
type S3Storage struct {
	bucket string
	region string
	client *s3.S3
}

// NewS3Storage creates a new S3 storage client
func NewS3Storage(bucket, region, endpoint string) (*S3Storage, error) {
	config := &aws.Config{
		Region: aws.String(region),
	}

	// If custom endpoint is provided (for MinIO, etc.), configure it
	if endpoint != "" {
		config.Endpoint = aws.String(endpoint)
		config.S3ForcePathStyle = aws.Bool(true) // Required for MinIO
	}

	sess, err := session.NewSession(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create AWS session: %w", err)
	}

	return &S3Storage{
		bucket: bucket,
		region: region,
		client: s3.New(sess),
	}, nil
}

// GeneratePresignedURL generates a pre-signed URL for uploading files
func (s *S3Storage) GeneratePresignedURL(appName, versionID, filename string) (string, error) {
	key := fmt.Sprintf("drafts/%s/%s/%s", appName, versionID, filename)

	req, _ := s.client.PutObjectRequest(&s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})

	// URL expires in 5 minutes
	url, err := req.Presign(5 * time.Minute)
	if err != nil {
		return "", fmt.Errorf("failed to generate presigned URL: %w", err)
	}

	return url, nil
}

// ListFiles lists all files for a version
func (s *S3Storage) ListFiles(appName, versionID string, published bool) ([]string, error) {
	prefix := fmt.Sprintf("drafts/%s/%s/", appName, versionID)
	if published {
		prefix = fmt.Sprintf("published/%s/%s/", appName, versionID)
	}

	result, err := s.client.ListObjectsV2(&s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(prefix),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list files: %w", err)
	}

	files := []string{}
	for _, obj := range result.Contents {
		// Extract just the filename from the full key
		parts := strings.Split(*obj.Key, "/")
		if len(parts) > 0 {
			filename := parts[len(parts)-1]
			if filename != "" {
				files = append(files, filename)
			}
		}
	}

	return files, nil
}

// MoveVersion moves a version from drafts to published
func (s *S3Storage) MoveVersion(appName, versionID string) error {
	// List all files in the draft
	files, err := s.ListFiles(appName, versionID, false)
	if err != nil {
		return fmt.Errorf("failed to list draft files: %w", err)
	}

	if len(files) == 0 {
		return fmt.Errorf("no files found in draft")
	}

	// Copy each file to published location
	for _, file := range files {
		srcKey := fmt.Sprintf("drafts/%s/%s/%s", appName, versionID, file)
		dstKey := fmt.Sprintf("published/%s/%s/%s", appName, versionID, file)

		// Copy file
		_, err := s.client.CopyObject(&s3.CopyObjectInput{
			Bucket:     aws.String(s.bucket),
			CopySource: aws.String(fmt.Sprintf("%s/%s", s.bucket, srcKey)),
			Key:        aws.String(dstKey),
		})
		if err != nil {
			return fmt.Errorf("failed to copy %s: %w", file, err)
		}

		// Delete original
		_, err = s.client.DeleteObject(&s3.DeleteObjectInput{
			Bucket: aws.String(s.bucket),
			Key:    aws.String(srcKey),
		})
		if err != nil {
			return fmt.Errorf("failed to delete draft %s: %w", file, err)
		}
	}

	return nil
}

// GetFile retrieves a file from S3
func (s *S3Storage) GetFile(appName, versionID, filename string, published bool) (io.ReadCloser, error) {
	key := fmt.Sprintf("drafts/%s/%s/%s", appName, versionID, filename)
	if published {
		key = fmt.Sprintf("published/%s/%s/%s", appName, versionID, filename)
	}

	result, err := s.client.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get file: %w", err)
	}

	return result.Body, nil
}

// GetAllFiles retrieves all files for a version
func (s *S3Storage) GetAllFiles(appName, versionID string, published bool) (map[string][]byte, error) {
	files, err := s.ListFiles(appName, versionID, published)
	if err != nil {
		return nil, err
	}

	result := make(map[string][]byte)
	for _, file := range files {
		reader, err := s.GetFile(appName, versionID, file, published)
		if err != nil {
			return nil, err
		}
		defer reader.Close()

		data, err := io.ReadAll(reader)
		if err != nil {
			return nil, fmt.Errorf("failed to read file %s: %w", file, err)
		}

		result[file] = data
	}

	return result, nil
}
