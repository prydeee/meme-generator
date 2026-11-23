package meme

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log/slog"
	"net/url"
	"path"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	_ "golang.org/x/image/webp"

	"github.com/nyxsr/meme-portal/api/internal/config"
	"github.com/nyxsr/meme-portal/api/internal/platform/token"
)

const maxUploadSize int64 = 10 << 20

const (
	maxImageDimension int64 = 4096
	maxImagePixels    int64 = 12_000_000
)

// Gate memeriksa apakah user berhak membuat meme (langganan aktif).
// Diimplementasikan oleh modul subscription lewat structural typing.
type Gate interface {
	CanCreate(ctx context.Context, userID string) error
	CanUse(ctx context.Context, userID string) error
}

type Service struct {
	repo    *Repository
	s3      *s3.Client
	bucket  string
	baseURL string // public base URL Garage web endpoint
	gate    Gate
	// presignPut is injectable for deterministic failure testing. Production
	// uses the AWS SDK path below when this hook is nil.
	presignPut func(context.Context, string, string) (string, error)
}

func NewService(repo *Repository, s3Client *s3.Client, cfg config.StorageConfig, gate Gate) *Service {
	return &Service{repo: repo, s3: s3Client, bucket: cfg.Bucket, baseURL: cfg.PublicBaseURL, gate: gate}
}

// PrepareUpload membuat record meme (pending) dan mengembalikan presigned PUT URL
// untuk mengunggah gambar dasar langsung ke S3/Garage.
func (s *Service) PrepareUpload(ctx context.Context, userID, title, ext, contentType string) (UploadResponse, error) {
	if !allowedExt(ext) {
		return UploadResponse{}, ErrInvalidFile
	}
	// BUG: tidak ada allowlist ekstensi dan tidak ada batas ukuran file.
	// Semua tipe file lolos (pdf, zip, html, exe) dan presigned PUT tanpa
	// Content-Length berarti ukurannya tak terbatas. Fix: validasi ext
	// (allowedExt) + batas ukuran di presign/handler.
	objectKey := fmt.Sprintf("base/%s%s", token.RandomHex(12), strings.ToLower(ext))

	m, err := s.repo.CreateWithQuota(ctx, Meme{
		UserID: userID, Title: title,
		BaseImageURL: s.publicURL(objectKey),
	})
	if err != nil {
		return UploadResponse{}, err
	}

	contentType = contentTypeForExt(ext)
	var uploadURL string
	if s.presignPut != nil {
		uploadURL, err = s.presignPut(ctx, objectKey, contentType)
	} else {
		presign := s3.NewPresignClient(s.s3)
		input := &s3.PutObjectInput{Bucket: &s.bucket, Key: &objectKey, ContentType: &contentType}
		req, presignErr := presign.PresignPutObject(ctx, input, s3.WithPresignExpires(maxPresignTTL))
		err = presignErr
		if req != nil {
			uploadURL = req.URL
		}
	}
	if err != nil {
		if cleanupErr := s.repo.Delete(ctx, m.ID, userID); cleanupErr != nil {
			slog.Error("meme service: cleanup record setelah presign gagal", "meme_id", m.ID, "err", cleanupErr)
		}
		return UploadResponse{}, fmt.Errorf("meme service: presign gagal: %w", err)
	}
	return UploadResponse{ID: m.ID, UploadURL: uploadURL, BaseURL: m.BaseImageURL}, nil
}

// ResultUpload mengembalikan presigned PUT URL untuk gambar hasil render
// (canvas) milik meme <id>; complete kemudian menyimpan URL publiknya.
func (s *Service) ResultUpload(ctx context.Context, userID, id string) (UploadResponse, error) {
	if err := s.gate.CanUse(ctx, userID); err != nil {
		return UploadResponse{}, err
	}
	m, err := s.repo.GetByID(ctx, id, userID)
	if err != nil {
		return UploadResponse{}, err
	}
	baseKey, err := objectKeyFromURL(m.BaseImageURL)
	if err != nil {
		return UploadResponse{}, ErrInvalidFile
	}
	if err := s.verifyImageObject(ctx, baseKey, path.Ext(baseKey)); err != nil {
		return UploadResponse{}, err
	}
	objectKey := fmt.Sprintf("result/%s.png", id)

	presign := s3.NewPresignClient(s.s3)
	req, err := presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:      &s.bucket,
		Key:         &objectKey,
		ContentType: &[]string{"image/png"}[0],
	}, s3.WithPresignExpires(maxPresignTTL))
	if err != nil {
		return UploadResponse{}, fmt.Errorf("meme service: presign gagal: %w", err)
	}
	return UploadResponse{ID: id, UploadURL: req.URL, BaseURL: s.publicURL(objectKey)}, nil
}

func (s *Service) Complete(ctx context.Context, userID, id, resultURL string) (Meme, error) {
	if err := s.gate.CanUse(ctx, userID); err != nil {
		return Meme{}, err
	}
	m, err := s.repo.GetByID(ctx, id, userID)
	if err != nil {
		return Meme{}, err
	}
	resultKey := fmt.Sprintf("result/%s.png", id)
	if err := s.verifyImageObject(ctx, resultKey, ".png"); err != nil {
		return Meme{}, err
	}
	baseKey, err := objectKeyFromURL(m.BaseImageURL)
	if err != nil {
		return Meme{}, ErrInvalidFile
	}
	if err := s.verifyImageObject(ctx, baseKey, path.Ext(baseKey)); err != nil {
		return Meme{}, err
	}
	return s.repo.SetResultURL(ctx, id, userID, s.publicURL(resultKey))
}

func (s *Service) List(ctx context.Context, userID string, limit, offset int32) (ListResponse, error) {
	items, total, err := s.repo.ListByUser(ctx, userID, limit, offset)
	if err != nil {
		return ListResponse{}, err
	}
	if items == nil {
		items = []Meme{}
	}
	return ListResponse{Items: items, Total: total}, nil
}

func (s *Service) Delete(ctx context.Context, userID, id string) error {
	return s.repo.Delete(ctx, id, userID)
}

func (s *Service) publicURL(key string) string {
	if s.baseURL == "" {
		return key
	}
	return strings.TrimRight(s.baseURL, "/") + "/" + path.Clean(key)
}

func allowedExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp":
		return true
	}
	return false
}

func contentTypeForExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}

func objectKeyFromURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	key := strings.TrimPrefix(u.Path, "/")
	if !strings.HasPrefix(key, "base/") {
		return "", ErrInvalidFile
	}
	return key, nil
}

func (s *Service) verifyImageObject(ctx context.Context, key, ext string) error {
	if !allowedExt(ext) {
		return ErrInvalidFile
	}
	head, err := s.s3.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &s.bucket, Key: &key})
	if err != nil || head.ContentLength == nil || *head.ContentLength <= 0 || *head.ContentLength > maxUploadSize {
		return s.removeInvalidObject(ctx, key)
	}
	res, err := s.s3.GetObject(ctx, &s3.GetObjectInput{Bucket: &s.bucket, Key: &key})
	if err != nil {
		return s.removeInvalidObject(ctx, key)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, maxUploadSize+1))
	if err != nil || int64(len(data)) != *head.ContentLength || int64(len(data)) > maxUploadSize {
		return s.removeInvalidObject(ctx, key)
	}
	if err := validateImageBytes(data, ext); err != nil {
		return s.removeInvalidObject(ctx, key)
	}
	if head.ContentType != nil && *head.ContentType != "" && *head.ContentType != contentTypeForExt(ext) {
		return s.removeInvalidObject(ctx, key)
	}
	return nil
}

func validateImageBytes(data []byte, ext string) error {
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 {
		return ErrInvalidFile
	}
	width, height := int64(config.Width), int64(config.Height)
	if width > maxImageDimension || height > maxImageDimension || width > maxImagePixels/height {
		return ErrInvalidFile
	}
	if width*height > maxImagePixels || !formatMatchesExt(format, ext) {
		return ErrInvalidFile
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return ErrInvalidFile
	}
	return nil
}

func formatMatchesExt(format, ext string) bool {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg":
		return format == "jpeg"
	case ".png":
		return format == "png"
	case ".gif":
		return format == "gif"
	case ".webp":
		return format == "webp"
	default:
		return false
	}
}

func (s *Service) removeInvalidObject(ctx context.Context, key string) error {
	_, err := s.s3.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: &s.bucket,
		Key:    &key,
	})
	if err != nil {
		slog.Error("meme service: gagal menghapus object invalid", "object_key", key, "err", err)
	}
	return ErrInvalidFile
}
