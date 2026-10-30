package storage

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3Storage handles S3 operations for version storage
type S3Storage struct {
	bucket  string
	region  string
	client  *s3.Client
	presign *s3.PresignClient
}

// NewS3Storage creates a new S3 storage client
func NewS3Storage(bucket, region, endpoint string) (*S3Storage, error) {
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(region),
		// Checksums only where S3 requires them, as with SDK v1: some
		// S3-compatible stores reject the checksum headers v2 adds by default
		awsconfig.WithRequestChecksumCalculation(aws.RequestChecksumCalculationWhenRequired),
		awsconfig.WithResponseChecksumValidation(aws.ResponseChecksumValidationWhenRequired),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		// If custom endpoint is provided (for MinIO, etc.), configure it
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
			o.UsePathStyle = true // Required for MinIO
		}
	})

	return &S3Storage{
		bucket:  bucket,
		region:  region,
		client:  client,
		presign: s3.NewPresignClient(client),
	}, nil
}

// GeneratePresignedURL generates a pre-signed URL for uploading files
func (s *S3Storage) GeneratePresignedURL(appName, versionID, filename string) (string, error) {
	key := fmt.Sprintf("drafts/%s/%s/%s", appName, versionID, filename)

	// URL expires in 5 minutes
	req, err := s.presign.PresignPutObject(context.Background(), &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(5*time.Minute))
	if err != nil {
		return "", fmt.Errorf("failed to generate presigned URL: %w", err)
	}

	return req.URL, nil
}

// ListFiles lists all files for a version
func (s *S3Storage) ListFiles(appName, versionID string, published bool) ([]string, error) {
	prefix := fmt.Sprintf("drafts/%s/%s/", appName, versionID)
	if published {
		prefix = fmt.Sprintf("published/%s/%s/", appName, versionID)
	}

	files := []string{}
	pages := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(prefix),
	})
	for pages.HasMorePages() {
		page, err := pages.NextPage(context.Background())
		if err != nil {
			return nil, fmt.Errorf("failed to list files: %w", err)
		}

		for _, obj := range page.Contents {
			// Extract just the filename from the full key
			parts := strings.Split(aws.ToString(obj.Key), "/")
			if len(parts) > 0 {
				filename := parts[len(parts)-1]
				if filename != "" {
					files = append(files, filename)
				}
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
		_, err := s.client.CopyObject(context.Background(), &s3.CopyObjectInput{
			Bucket:     aws.String(s.bucket),
			CopySource: aws.String(copySource(s.bucket, srcKey)),
			Key:        aws.String(dstKey),
		})
		if err != nil {
			return fmt.Errorf("failed to copy %s: %w", file, err)
		}

		// Delete original
		_, err = s.client.DeleteObject(context.Background(), &s3.DeleteObjectInput{
			Bucket: aws.String(s.bucket),
			Key:    aws.String(srcKey),
		})
		if err != nil {
			return fmt.Errorf("failed to delete draft %s: %w", file, err)
		}
	}

	return nil
}

// DeleteVersion deletes all files of a version, from the drafts or the
// published location. A version without files is not an error.
func (s *S3Storage) DeleteVersion(appName, versionID string, published bool) error {
	files, err := s.ListFiles(appName, versionID, published)
	if err != nil {
		return fmt.Errorf("failed to list files: %w", err)
	}

	prefix := "drafts"
	if published {
		prefix = "published"
	}
	for _, file := range files {
		_, err := s.client.DeleteObject(context.Background(), &s3.DeleteObjectInput{
			Bucket: aws.String(s.bucket),
			Key:    aws.String(fmt.Sprintf("%s/%s/%s/%s", prefix, appName, versionID, file)),
		})
		if err != nil {
			return fmt.Errorf("failed to delete %s: %w", file, err)
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

	result, err := s.client.GetObject(context.Background(), &s3.GetObjectInput{
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

// copySource returns the URL-encoded "bucket/key" CopyObject expects
func copySource(bucket, key string) string {
	segments := strings.Split(bucket+"/"+key, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}
