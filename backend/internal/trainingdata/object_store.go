package trainingdata

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type ObjectInfo struct {
	Key  string
	Size int64
}

type ObjectStore interface {
	HeadBucket(context.Context) error
	UploadFile(context.Context, string, string, string) (int64, error)
	Open(context.Context, string) (io.ReadCloser, error)
	Delete(context.Context, string) error
	DeletePrefix(context.Context, string) error
	List(context.Context, string) ([]ObjectInfo, error)
}

type S3ObjectStore struct {
	client *s3.Client
	bucket string
	prefix string
	sse    types.ServerSideEncryption
	kmsKey string
}

func NewS3ObjectStore(ctx context.Context, cfg config.TrainingDataObjectStoreConfig) (*S3ObjectStore, error) {
	if !cfg.IsConfigured() {
		return nil, errors.New("training data object store is not configured")
	}
	region := strings.TrimSpace(cfg.Region)
	if region == "" {
		region = "auto"
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			strings.TrimSpace(cfg.AccessKeyID), strings.TrimSpace(cfg.SecretAccessKey), "",
		)),
	)
	if err != nil {
		return nil, fmt.Errorf("load training data S3 config: %w", err)
	}
	client := s3.NewFromConfig(awsCfg, func(options *s3.Options) {
		if endpoint := strings.TrimSpace(cfg.Endpoint); endpoint != "" {
			options.BaseEndpoint = aws.String(endpoint)
		}
		options.UsePathStyle = cfg.ForcePathStyle
		options.APIOptions = append(options.APIOptions, v4.SwapComputePayloadSHA256ForUnsignedPayloadMiddleware)
		options.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
	})
	store := &S3ObjectStore{
		client: client,
		bucket: strings.TrimSpace(cfg.Bucket),
		prefix: strings.Trim(strings.TrimSpace(cfg.Prefix), "/"),
		kmsKey: strings.TrimSpace(cfg.KMSKeyID),
	}
	switch strings.TrimSpace(cfg.ServerSideEncryption) {
	case "aws:kms":
		store.sse = types.ServerSideEncryptionAwsKms
	case "AES256", "":
		store.sse = types.ServerSideEncryptionAes256
	}
	return store, nil
}

func (s *S3ObjectStore) objectKey(key string) string {
	key = strings.TrimLeft(strings.TrimSpace(key), "/")
	if s.prefix == "" {
		return key
	}
	if key == "" {
		return s.prefix + "/"
	}
	return s.prefix + "/" + key
}

func (s *S3ObjectStore) HeadBucket(ctx context.Context) error {
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(s.bucket)})
	if err != nil {
		return fmt.Errorf("head training data bucket: %w", err)
	}
	return nil
}

func (s *S3ObjectStore) UploadFile(ctx context.Context, key, filename, contentType string) (int64, error) {
	file, err := os.Open(filename)
	if err != nil {
		return 0, fmt.Errorf("open training data object %q: %w", filename, err)
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return 0, fmt.Errorf("stat training data object %q: %w", filename, err)
	}
	input := &s3.PutObjectInput{
		Bucket:               aws.String(s.bucket),
		Key:                  aws.String(s.objectKey(key)),
		Body:                 file,
		ContentLength:        aws.Int64(stat.Size()),
		ContentType:          aws.String(contentType),
		ServerSideEncryption: s.sse,
	}
	if s.sse == types.ServerSideEncryptionAwsKms {
		input.SSEKMSKeyId = aws.String(s.kmsKey)
	}
	if _, err := s.client.PutObject(ctx, input); err != nil {
		return 0, fmt.Errorf("upload training data object %q: %w", key, err)
	}
	return stat.Size(), nil
}

func (s *S3ObjectStore) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	result, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.objectKey(key)),
	})
	if err != nil {
		return nil, fmt.Errorf("open training data object %q: %w", key, err)
	}
	return result.Body, nil
}

func (s *S3ObjectStore) Delete(ctx context.Context, key string) error {
	if strings.TrimSpace(key) == "" {
		return errors.New("refusing to delete an empty training data object key")
	}
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.objectKey(key)),
	})
	if err != nil {
		return fmt.Errorf("delete training data object %q: %w", key, err)
	}
	return nil
}

func (s *S3ObjectStore) List(ctx context.Context, prefix string) ([]ObjectInfo, error) {
	fullPrefix := s.objectKey(prefix)
	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(fullPrefix),
	})
	var objects []ObjectInfo
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list training data objects %q: %w", prefix, err)
		}
		for _, object := range page.Contents {
			key := strings.TrimPrefix(aws.ToString(object.Key), s.prefix)
			key = strings.TrimLeft(key, "/")
			objects = append(objects, ObjectInfo{Key: key, Size: aws.ToInt64(object.Size)})
		}
	}
	return objects, nil
}

func (s *S3ObjectStore) DeletePrefix(ctx context.Context, prefix string) error {
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	if prefix == "" || prefix == "." {
		return errors.New("refusing to delete an empty training data object prefix")
	}
	objects, err := s.List(ctx, prefix)
	if err != nil {
		return err
	}
	for start := 0; start < len(objects); start += 1000 {
		end := start + 1000
		if end > len(objects) {
			end = len(objects)
		}
		identifiers := make([]types.ObjectIdentifier, 0, end-start)
		for _, object := range objects[start:end] {
			identifiers = append(identifiers, types.ObjectIdentifier{Key: aws.String(s.objectKey(object.Key))})
		}
		if len(identifiers) == 0 {
			continue
		}
		result, err := s.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(s.bucket),
			Delete: &types.Delete{Objects: identifiers, Quiet: aws.Bool(true)},
		})
		if err != nil {
			return fmt.Errorf("delete training data prefix %q: %w", prefix, err)
		}
		if len(result.Errors) > 0 {
			first := result.Errors[0]
			return fmt.Errorf("delete training data prefix %q object %q: %s: %s",
				prefix, aws.ToString(first.Key), aws.ToString(first.Code), aws.ToString(first.Message))
		}
	}
	return nil
}
