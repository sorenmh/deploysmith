package storage

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
)

func newTestStorage(t *testing.T) *S3Storage {
	t.Helper()
	server := httptest.NewServer(gofakes3.New(s3mem.New()).Server())
	t.Cleanup(server.Close)

	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_CONFIG_FILE", "/dev/null")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "/dev/null")

	s, err := NewS3Storage("versions", "eu-central-1", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.client.CreateBucket(context.Background(), &s3.CreateBucketInput{Bucket: aws.String("versions")}); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestUploadAndPublishVersion(t *testing.T) {
	s := newTestStorage(t)

	uploadURL, err := s.GeneratePresignedURL("hello", "v1", "manifests.tar.gz")
	if err != nil {
		t.Fatal(err)
	}

	// The uploader sends a plain PUT, so the URL must not demand checksums
	parsed, err := url.Parse(uploadURL)
	if err != nil {
		t.Fatal(err)
	}
	for param := range parsed.Query() {
		if strings.Contains(strings.ToLower(param), "checksum") {
			t.Errorf("presigned URL requires a checksum: %s", uploadURL)
		}
	}

	req, err := http.NewRequest(http.MethodPut, uploadURL, bytes.NewReader([]byte("tarball")))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload returned %d", resp.StatusCode)
	}

	files, err := s.ListFiles("hello", "v1", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != "manifests.tar.gz" {
		t.Fatalf("draft files = %v", files)
	}

	if err := s.MoveVersion("hello", "v1"); err != nil {
		t.Fatal(err)
	}
	if files, _ := s.ListFiles("hello", "v1", false); len(files) != 0 {
		t.Errorf("draft not removed after publish: %v", files)
	}

	published, err := s.GetAllFiles("hello", "v1", true)
	if err != nil {
		t.Fatal(err)
	}
	if string(published["manifests.tar.gz"]) != "tarball" {
		t.Errorf("published files = %v", published)
	}
}

func TestCopySource(t *testing.T) {
	got := copySource("versions", "drafts/hello/v1/my file+1.yaml")
	want := "versions/drafts/hello/v1/my%20file+1.yaml"
	if got != want {
		t.Errorf("copySource = %q, want %q", got, want)
	}
}
