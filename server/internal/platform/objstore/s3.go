package objstore

import (
	"context"
	"fmt"
	"mime"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Config descreve o endpoint. Endpoint é o que o servidor alcança; PublicEndpoint (opcional) é o que o cliente
// alcança e vai nas URLs pré-assinadas (a assinatura cobre o host, então ele precisa ser o certo na hora de assinar).
type S3Config struct {
	Endpoint       string // "host:port" ou "http(s)://host:port"
	PublicEndpoint string
	Bucket         string
	AccessKey      string
	SecretKey      string
	Region         string
}

// S3 usa minio-go/v7: um módulo só, sem o grafo de dezenas de módulos do aws-sdk-go-v2, e a API de
// presign é direta. Funciona com MinIO e com S3.
type S3 struct {
	cli     *minio.Client // operações do servidor (Stat/Delete/bucket)
	presign *minio.Client // assina para o endpoint público; com Region fixa, não faz chamada de rede
	bucket  string
}

func newClient(endpoint string, c S3Config) (*minio.Client, error) {
	secure := false
	host := endpoint
	if strings.Contains(endpoint, "://") {
		u, err := url.Parse(endpoint)
		if err != nil || u.Host == "" {
			return nil, fmt.Errorf("objstore: invalid endpoint %q", endpoint)
		}
		secure, host = u.Scheme == "https", u.Host
	}
	return minio.New(host, &minio.Options{
		Creds:  credentials.NewStaticV4(c.AccessKey, c.SecretKey, ""),
		Secure: secure,
		Region: c.Region,
	})
}

// NewS3 conecta e cria o bucket se ele não existir.
func NewS3(ctx context.Context, c S3Config) (*S3, error) {
	if c.Region == "" {
		c.Region = "us-east-1"
	}
	cli, err := newClient(c.Endpoint, c)
	if err != nil {
		return nil, err
	}
	presign := cli
	if c.PublicEndpoint != "" && c.PublicEndpoint != c.Endpoint {
		if presign, err = newClient(c.PublicEndpoint, c); err != nil {
			return nil, err
		}
	}
	s := &S3{cli: cli, presign: presign, bucket: c.Bucket}
	if err := s.ensureBucket(ctx, c.Region); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *S3) ensureBucket(ctx context.Context, region string) error {
	ok, err := s.cli.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("objstore: check bucket: %w", err)
	}
	if ok {
		return nil
	}
	if err := s.cli.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{Region: region}); err != nil {
		// outra réplica pode ter criado entre a checagem e a criação
		if minio.ToErrorResponse(err).Code == "BucketAlreadyOwnedByYou" {
			return nil
		}
		return fmt.Errorf("objstore: make bucket: %w", err)
	}
	return nil
}

func (s *S3) PresignPut(ctx context.Context, key string, ttl time.Duration) (string, error) {
	u, err := s.presign.PresignedPutObject(ctx, s.bucket, key, ttl)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

func (s *S3) PresignGet(ctx context.Context, key string, opt GetOptions, ttl time.Duration) (string, error) {
	q := url.Values{}
	if opt.ContentType != "" {
		q.Set("response-content-type", opt.ContentType)
	}
	if opt.Filename != "" {
		kind := "attachment"
		if opt.Inline {
			kind = "inline"
		}
		q.Set("response-content-disposition", mime.FormatMediaType(kind, map[string]string{"filename": opt.Filename}))
	}
	u, err := s.presign.PresignedGetObject(ctx, s.bucket, key, ttl, q)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

func (s *S3) Stat(ctx context.Context, key string) (Info, error) {
	st, err := s.cli.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return Info{}, ErrNotFound
		}
		return Info{}, err
	}
	return Info{Size: st.Size, ContentType: st.ContentType}, nil
}

func (s *S3) Delete(ctx context.Context, key string) error {
	return s.cli.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
}

// RemoveBucket apaga o bucket e o que houver nele. Só para testes.
func (s *S3) RemoveBucket(ctx context.Context) error {
	for o := range s.cli.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Recursive: true}) {
		if o.Err == nil {
			_ = s.cli.RemoveObject(ctx, s.bucket, o.Key, minio.RemoveObjectOptions{})
		}
	}
	return s.cli.RemoveBucket(ctx, s.bucket)
}
