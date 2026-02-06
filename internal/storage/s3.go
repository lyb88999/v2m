package storage

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type S3Client struct {
	client         *minio.Client
	presignClient  *minio.Client
	bucket         string
	usePathStyle   bool
	publicEndpoint string
	endpointURL    string
}

func NewS3(endpoint, accessKey, secretKey, region, bucket string, usePathStyle bool, publicEndpoint string) (*S3Client, error) {
	host, secure, endpointURL, err := normalizeEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	if bucket == "" {
		return nil, errors.New("S3_BUCKET is required")
	}

	lookup := minio.BucketLookupAuto
	if usePathStyle {
		lookup = minio.BucketLookupPath
	}

	client, err := minio.New(host, &minio.Options{
		Creds:        credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure:       secure,
		Region:       region,
		BucketLookup: lookup,
	})
	if err != nil {
		return nil, err
	}

	if publicEndpoint == "" {
		publicEndpoint = endpointURL
	}

	var presignClient *minio.Client
	if strings.TrimSpace(publicEndpoint) != "" && publicEndpoint != endpointURL {
		pHost, pSecure, _, err := normalizeEndpoint(publicEndpoint)
		if err == nil {
			if c, err := minio.New(pHost, &minio.Options{
				Creds:        credentials.NewStaticV4(accessKey, secretKey, ""),
				Secure:       pSecure,
				Region:       region,
				BucketLookup: lookup,
			}); err == nil {
				presignClient = c
			}
		}
	}

	return &S3Client{
		client:         client,
		presignClient:  presignClient,
		bucket:         bucket,
		usePathStyle:   usePathStyle,
		publicEndpoint: publicEndpoint,
		endpointURL:    endpointURL,
	}, nil
}

func (s *S3Client) UploadMP3(ctx context.Context, filePath, objectKey string) (string, error) {
	_, err := s.client.FPutObject(ctx, s.bucket, objectKey, filePath, minio.PutObjectOptions{
		ContentType: "audio/mpeg",
	})
	if err != nil {
		return "", err
	}
	return objectKey, nil
}

func (s *S3Client) objectURL(objectKey string) string {
	base := strings.TrimRight(s.publicEndpoint, "/")
	if base == "" {
		base = strings.TrimRight(s.endpointURL, "/")
	}
	if base == "" {
		return objectKey
	}

	if s.usePathStyle {
		return fmt.Sprintf("%s/%s/%s", base, s.bucket, objectKey)
	}

	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return fmt.Sprintf("%s/%s/%s", base, s.bucket, objectKey)
	}
	u.Host = fmt.Sprintf("%s.%s", s.bucket, u.Host)
	u.Path = "/" + objectKey
	return u.String()
}

func (s *S3Client) PresignMP3(ctx context.Context, objectKey string, expiry time.Duration) (string, error) {
	if strings.TrimSpace(objectKey) == "" {
		return "", errors.New("object key is empty")
	}
	if expiry <= 0 {
		expiry = 15 * time.Minute
	}
	client := s.client
	if s.presignClient != nil {
		client = s.presignClient
	}
	u, err := client.PresignedGetObject(ctx, s.bucket, objectKey, expiry, nil)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

func (s *S3Client) PresignMP3Download(ctx context.Context, objectKey string, expiry time.Duration, filename string) (string, error) {
	if strings.TrimSpace(objectKey) == "" {
		return "", errors.New("object key is empty")
	}
	if strings.TrimSpace(filename) == "" {
		filename = "download.mp3"
	}
	if expiry <= 0 {
		expiry = 15 * time.Minute
	}
	params := url.Values{}
	params.Set("response-content-disposition", fmt.Sprintf("attachment; filename=%q", filename))
	params.Set("response-content-type", "audio/mpeg")

	client := s.client
	if s.presignClient != nil {
		client = s.presignClient
	}
	u, err := client.PresignedGetObject(ctx, s.bucket, objectKey, expiry, params)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

func (s *S3Client) DeleteObject(ctx context.Context, objectKey string) error {
	if strings.TrimSpace(objectKey) == "" {
		return errors.New("object key is empty")
	}
	return s.client.RemoveObject(ctx, s.bucket, objectKey, minio.RemoveObjectOptions{})
}

func normalizeEndpoint(raw string) (host string, secure bool, endpointURL string, err error) {
	if raw == "" {
		return "", false, "", errors.New("S3_ENDPOINT is required")
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", false, "", err
		}
		if u.Host == "" {
			return "", false, "", errors.New("invalid S3_ENDPOINT")
		}
		return u.Host, u.Scheme == "https", u.Scheme + "://" + u.Host, nil
	}
	return raw, false, "http://" + raw, nil
}
