package cos

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/IBM/ibm-cos-sdk-go/aws"
	"github.com/IBM/ibm-cos-sdk-go/aws/awserr"
	"github.com/IBM/ibm-cos-sdk-go/aws/credentials"
	"github.com/IBM/ibm-cos-sdk-go/aws/credentials/ibmiam"
	"github.com/IBM/ibm-cos-sdk-go/aws/session"
	"github.com/IBM/ibm-cos-sdk-go/service/s3"
	"github.com/oborges/cos-nfs-gateway/internal/config"
	"github.com/oborges/cos-nfs-gateway/internal/logging"
	"github.com/oborges/cos-nfs-gateway/pkg/types"
	"go.uber.org/zap"
)

// Client wraps the IBM Cloud COS SDK client
type Client struct {
	s3Client *s3.S3
	bucket   string
	config   *config.COSConfig
}

// NewClient creates a new COS client
func NewClient(cfg *config.COSConfig) (*Client, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	// Create AWS config
	awsConfig := &aws.Config{
		Endpoint: aws.String(cfg.Endpoint),
		Region:   aws.String(cfg.Region),
	}

	// Set timeout
	timeout, err := cfg.GetTimeout()
	if err != nil {
		return nil, fmt.Errorf("invalid timeout: %w", err)
	}
	awsConfig.HTTPClient = &http.Client{
		Timeout: timeout,
	}

	// Configure authentication
	var creds *credentials.Credentials
	switch cfg.AuthType {
	case "iam":
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("api_key is required for IAM authentication")
		}
		creds = ibmiam.NewStaticCredentials(
			aws.NewConfig(),
			"https://iam.cloud.ibm.com/identity/token",
			cfg.APIKey,
			cfg.ServiceID,
		)
	case "hmac":
		if cfg.AccessKey == "" || cfg.SecretKey == "" {
			return nil, fmt.Errorf("access_key and secret_key are required for HMAC authentication")
		}
		creds = credentials.NewStaticCredentials(
			cfg.AccessKey,
			cfg.SecretKey,
			"",
		)
	default:
		return nil, fmt.Errorf("invalid auth_type: %s", cfg.AuthType)
	}

	awsConfig.Credentials = creds

	// Create session
	sess, err := session.NewSession(awsConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	// Create S3 client
	s3Client := s3.New(sess)

	client := &Client{
		s3Client: s3Client,
		bucket:   cfg.Bucket,
		config:   cfg,
	}

	// Verify connectivity
	if err := client.ping(); err != nil {
		return nil, fmt.Errorf("failed to connect to COS: %w", err)
	}

	logging.Info("COS client initialized",
		zap.String("endpoint", cfg.Endpoint),
		zap.String("bucket", cfg.Bucket),
		zap.String("region", cfg.Region),
	)

	return client, nil
}

// ping verifies connectivity to COS
func (c *Client) ping() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := c.s3Client.HeadBucketWithContext(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(c.bucket),
	})
	if err != nil {
		return fmt.Errorf("bucket not accessible: %w", err)
	}

	return nil
}

// GetObject retrieves an object from COS
func (c *Client) GetObject(ctx context.Context, key string) ([]byte, error) {
	log := logging.WithOperation("GetObject").With(zap.String("key", key))
	log.Debug("getting object")

	input := &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	}

	result, err := c.s3Client.GetObjectWithContext(ctx, input)
	if err != nil {
		log.Error("failed to get object", zap.Error(err))
		return nil, fmt.Errorf("failed to get object: %w", err)
	}
	defer result.Body.Close()

	data, err := io.ReadAll(result.Body)
	if err != nil {
		log.Error("failed to read object body", zap.Error(err))
		return nil, fmt.Errorf("failed to read object body: %w", err)
	}

	log.Debug("object retrieved", zap.Int("size", len(data)))
	return data, nil
}

// GetObjectRange retrieves a range of bytes from an object
func (c *Client) GetObjectRange(ctx context.Context, key string, offset, length int64) ([]byte, error) {
	log := logging.WithOperation("GetObjectRange").With(
		zap.String("key", key),
		zap.Int64("offset", offset),
		zap.Int64("length", length),
	)
	log.Debug("getting object range")

	rangeHeader := fmt.Sprintf("bytes=%d-%d", offset, offset+length-1)

	input := &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
		Range:  aws.String(rangeHeader),
	}

	result, err := c.s3Client.GetObjectWithContext(ctx, input)
	if err != nil {
		log.Error("failed to get object range", zap.Error(err))
		return nil, fmt.Errorf("failed to get object range: %w", err)
	}
	defer result.Body.Close()

	data, err := io.ReadAll(result.Body)
	if err != nil {
		log.Error("failed to read object body", zap.Error(err))
		return nil, fmt.Errorf("failed to read object body: %w", err)
	}

	log.Debug("object range retrieved", zap.Int("size", len(data)))
	return data, nil
}

// PutObject uploads an object to COS
func (c *Client) PutObject(ctx context.Context, key string, data []byte, metadata map[string]string) error {
	log := logging.WithOperation("PutObject").With(
		zap.String("key", key),
		zap.Int("size", len(data)),
	)
	log.Debug("putting object")

	// Convert metadata to AWS format
	awsMetadata := make(map[string]*string)
	for k, v := range metadata {
		awsMetadata[k] = aws.String(v)
	}

	input := &s3.PutObjectInput{
		Bucket:   aws.String(c.bucket),
		Key:      aws.String(key),
		Body:     bytes.NewReader(data),
		Metadata: awsMetadata,
	}

	_, err := c.s3Client.PutObjectWithContext(ctx, input)
	if err != nil {
		log.Error("failed to put object", zap.Error(err))
		return fmt.Errorf("failed to put object: %w", err)
	}

	log.Debug("object uploaded")
	return nil
}

// DeleteObject deletes an object from COS
func (c *Client) DeleteObject(ctx context.Context, key string) error {
	log := logging.WithOperation("DeleteObject").With(zap.String("key", key))
	log.Debug("deleting object")

	input := &s3.DeleteObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	}

	_, err := c.s3Client.DeleteObjectWithContext(ctx, input)
	if err != nil {
		log.Error("failed to delete object", zap.Error(err))
		return fmt.Errorf("failed to delete object: %w", err)
	}

	log.Debug("object deleted")
	return nil
}

// HeadObject retrieves object metadata
func (c *Client) HeadObject(ctx context.Context, key string) (*types.ObjectMetadata, error) {
	log := logging.WithOperation("HeadObject").With(zap.String("key", key))
	log.Debug("getting object metadata")

	input := &s3.HeadObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	}

	result, err := c.s3Client.HeadObjectWithContext(ctx, input)
	if err != nil {
		log.Debug("object not found or error", zap.Error(err))
		return nil, fmt.Errorf("failed to get object metadata: %w", err)
	}

	// Convert metadata
	metadata := make(map[string]string)
	for k, v := range result.Metadata {
		if v != nil {
			metadata[k] = *v
		}
	}

	objectMeta := &types.ObjectMetadata{
		Key:          key,
		Size:         aws.Int64Value(result.ContentLength),
		LastModified: aws.TimeValue(result.LastModified),
		ETag:         aws.StringValue(result.ETag),
		ContentType:  aws.StringValue(result.ContentType),
		Metadata:     metadata,
	}

	log.Debug("object metadata retrieved", zap.Int64("size", objectMeta.Size))
	return objectMeta, nil
}

// ListObjects lists objects with a given prefix
func (c *Client) ListObjects(ctx context.Context, prefix string, maxKeys int) ([]*types.ObjectMetadata, error) {
	log := logging.WithOperation("ListObjects").With(
		zap.String("prefix", prefix),
		zap.Int("maxKeys", maxKeys),
	)
	log.Debug("listing objects")

	input := &s3.ListObjectsV2Input{
		Bucket:  aws.String(c.bucket),
		Prefix:  aws.String(prefix),
		MaxKeys: aws.Int64(int64(maxKeys)),
	}

	result, err := c.s3Client.ListObjectsV2WithContext(ctx, input)
	if err != nil {
		log.Error("failed to list objects", zap.Error(err))
		return nil, fmt.Errorf("failed to list objects: %w", err)
	}

	objects := make([]*types.ObjectMetadata, 0, len(result.Contents))
	for _, obj := range result.Contents {
		objects = append(objects, &types.ObjectMetadata{
			Key:          aws.StringValue(obj.Key),
			Size:         aws.Int64Value(obj.Size),
			LastModified: aws.TimeValue(obj.LastModified),
			ETag:         aws.StringValue(obj.ETag),
		})
	}

	log.Debug("objects listed", zap.Int("count", len(objects)))
	return objects, nil
}

// CopyObject copies an object within COS
func (c *Client) CopyObject(ctx context.Context, sourceKey, destKey string) error {
	log := logging.WithOperation("CopyObject").With(
		zap.String("sourceKey", sourceKey),
		zap.String("destKey", destKey),
	)
	log.Debug("copying object")

	copySource := fmt.Sprintf("%s/%s", c.bucket, sourceKey)

	input := &s3.CopyObjectInput{
		Bucket:     aws.String(c.bucket),
		CopySource: aws.String(copySource),
		Key:        aws.String(destKey),
	}

	_, err := c.s3Client.CopyObjectWithContext(ctx, input)
	if err != nil {
		log.Error("failed to copy object", zap.Error(err))
		return fmt.Errorf("failed to copy object: %w", err)
	}

	log.Debug("object copied")
	return nil
}

// ObjectExists checks if an object exists
func (c *Client) ObjectExists(ctx context.Context, key string) (bool, error) {
	_, err := c.HeadObject(ctx, key)
	if err != nil {
		// Check if it's a "not found" error
		if isNotFoundError(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// isNotFoundError checks if an error is a "not found" error
func isNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	// Check for AWS error codes
	if aerr, ok := err.(awserr.Error); ok {
		code := aerr.Code()
		return code == "NotFound" || code == "NoSuchKey" || code == s3.ErrCodeNoSuchKey || strings.Contains(code, "404")
	}
	return false
}

// Close closes the client (cleanup if needed)
func (c *Client) Close() error {
	logging.Info("COS client closed")
	return nil
}

// Made with Bob
