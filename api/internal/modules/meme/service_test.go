package meme

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func TestQuotaAvailable(t *testing.T) {
	for _, tc := range []struct {
		name string
		used int64
		max  int32
		want bool
	}{
		{"available", 19, 20, true},
		{"exhausted", 20, 20, false},
		{"unlimited", 1000, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := quotaAvailable(tc.used, tc.max); got != tc.want {
				t.Fatalf("quotaAvailable(%d, %d) = %v, want %v", tc.used, tc.max, got, tc.want)
			}
		})
	}
}

func TestValidUploadSizeEnforcesTenMegabytes(t *testing.T) {
	if !validUploadSize(10 << 20) {
		t.Fatal("10 MiB upload should be accepted")
	}
	if validUploadSize((10 << 20) + 1) {
		t.Fatal("upload larger than 10 MiB should be rejected")
	}
}

func TestAllowedExtIsCaseInsensitive(t *testing.T) {
	for _, ext := range []string{".jpg", ".JPG", ".jpeg", ".PNG", ".gif", ".WEBP"} {
		if !allowedExt(ext) {
			t.Errorf("allowedExt(%q) = false", ext)
		}
	}
}

func TestAllowedExtRejectsUnsupportedOrEmpty(t *testing.T) {
	for _, ext := range []string{"", ".exe", ".svg", ".zip", ".bmp"} {
		if allowedExt(ext) {
			t.Errorf("allowedExt(%q) = true", ext)
		}
	}
}

func TestValidateImageBytesAcceptsRealImages(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	for _, tc := range []struct {
		name, ext string
		data      []byte
	}{
		{"png", ".png", encodePNG(t, img)},
		{"jpeg", ".jpg", encodeJPEG(t, img)},
		{"gif", ".gif", encodeGIF(t, img)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateImageBytes(tc.data, tc.ext); err != nil {
				t.Fatal(err)
			}
		})
	}
	webpData, err := base64.StdEncoding.DecodeString("UklGRrIBAABXRUJQVlA4TKUBAAAvSsAYAA8w//M///MfeJAkbXvaSG7m8Q3GfYSBJekwQztm/IcZlgwnmWImn2BK7aFmBtnVir6q//8VOkFE/xm4baTIu8c48ArEo6+B3zFKYln3pqClSCKX0begFTAXFOLXHSyF8cCNcZEG4OywuA4KVVfJCiArU7GAgJI8+lJP/OKMT/fBAjevg1cYB7YVkFuWga2lyPi5I0HFy5YTpWIHg0RZpkniRVW9odHAKOwosWuOGdxIyn2OvaCDvhg/we6TwadPBPbqBV58MsLmMJ8yZnOWk8SRz4N+QoyPL+MnamzMvcE1rHNEr91F9GKZPVUcS9w7PhhH36suB9qPeYb/oLk6cuTiJ0wOK3m5h1cKjW6EVZCYMK7dxcKCBdgP9HkKr9gkAO2P8GKZGWVdIAatQa+1IDpt6qyorVwdy01xdW8Jkfk6xjEXmVQQ+HQdFr6OKhIN34dXWq0+0qr6EJSCeeVLH9+gvGTLyqM65PQ44ihzlTXxQKjKbAvshXgir7Lil9w4L2bvMycmjQcqXaMCO6BlY28i+FOLzbfI1vEqxAhotocAAA==")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateImageBytes(webpData, ".webp"); err != nil {
		t.Fatalf("valid WebP rejected: %v", err)
	}
}

func TestValidateImageBytesRejectsInvalidAndMismatchedData(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	pngData := encodePNG(t, img)
	for _, tc := range []struct {
		name, ext string
		data      []byte
	}{
		{"empty", ".png", nil},
		{"corrupt", ".png", []byte("not an image")},
		{"mismatched format", ".jpg", pngData},
		{"non-webp with webp extension", ".webp", pngData},
		{"fake png signature", ".png", append([]byte{0x89, 'P', 'N', 'G'}, []byte("invalid")...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateImageBytes(tc.data, tc.ext); err == nil {
				t.Fatal("invalid image accepted")
			}
		})
	}
}

func TestValidateImageBytesRejectsDimensionsAndPixels(t *testing.T) {
	withinLimits := image.NewRGBA(image.Rect(0, 0, int(maxImageDimension), int(maxImagePixels/maxImageDimension)))
	if err := validateImageBytes(encodePNG(t, withinLimits), ".png"); err != nil {
		t.Fatalf("image at configured limits rejected: %v", err)
	}
	tooWide := image.NewRGBA(image.Rect(0, 0, int(maxImageDimension)+1, 1))
	if err := validateImageBytes(encodePNG(t, tooWide), ".png"); err == nil {
		t.Fatal("width above limit accepted")
	}
	tooManyPixels := image.NewRGBA(image.Rect(0, 0, 4000, 3001))
	if err := validateImageBytes(encodePNG(t, tooManyPixels), ".png"); err == nil {
		t.Fatal("pixel count above limit accepted")
	}
}

func TestFormatMatchesExt(t *testing.T) {
	for _, tc := range []struct {
		format, ext string
		want        bool
	}{
		{"jpeg", ".jpg", true}, {"jpeg", ".jpeg", true}, {"png", ".PNG", true},
		{"gif", ".gif", true}, {"webp", ".webp", true}, {"png", ".jpg", false},
	} {
		if got := formatMatchesExt(tc.format, tc.ext); got != tc.want {
			t.Errorf("formatMatchesExt(%q, %q) = %v", tc.format, tc.ext, got)
		}
	}
}

func TestVerifyImageObjectStorageFailuresCleanup(t *testing.T) {
	valid := encodePNG(t, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	for _, tc := range []struct {
		name       string
		headStatus int
		getStatus  int
		body       []byte
		deleteCode int
		wantDelete bool
	}{
		{name: "missing object", headStatus: http.StatusNotFound, getStatus: http.StatusOK, deleteCode: http.StatusNoContent, wantDelete: true},
		{name: "get object failure", headStatus: http.StatusOK, getStatus: http.StatusInternalServerError, body: valid, deleteCode: http.StatusNoContent, wantDelete: true},
		{name: "invalid metadata and bytes", headStatus: http.StatusOK, getStatus: http.StatusOK, body: []byte("not an image"), deleteCode: http.StatusNoContent, wantDelete: true},
		{name: "cleanup failure is still validation error", headStatus: http.StatusNotFound, getStatus: http.StatusOK, deleteCode: http.StatusInternalServerError, wantDelete: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deleteCalls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodHead:
					if tc.headStatus != http.StatusOK {
						w.WriteHeader(tc.headStatus)
						return
					}
					w.Header().Set("Content-Length", "2")
					w.Header().Set("Content-Type", "image/png")
					w.WriteHeader(http.StatusOK)
				case http.MethodGet:
					w.WriteHeader(tc.getStatus)
					_, _ = w.Write(tc.body)
				case http.MethodDelete:
					deleteCalls++
					w.WriteHeader(tc.deleteCode)
				default:
					w.WriteHeader(http.StatusMethodNotAllowed)
				}
			}))
			defer srv.Close()
			endpoint := srv.URL
			client := s3.NewFromConfig(aws.Config{
				Region:      "us-east-1",
				Credentials: credentials.NewStaticCredentialsProvider("access", "secret", ""),
			}, func(o *s3.Options) { o.BaseEndpoint = &endpoint; o.UsePathStyle = true })
			service := &Service{s3: client, bucket: "test-bucket"}
			if err := service.verifyImageObject(t.Context(), "base/fixture.png", ".png"); err != ErrInvalidFile {
				t.Fatalf("verifyImageObject error = %v, want ErrInvalidFile", err)
			}
			if tc.wantDelete && deleteCalls < 1 {
				t.Fatalf("DeleteObject calls = %d, want at least 1", deleteCalls)
			}
		})
	}
}

func TestVerifyImageObjectRejectsMetadataMismatch(t *testing.T) {
	valid := encodePNG(t, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	calledDelete := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodHead:
			w.Header().Set("Content-Length", "2")
			w.Header().Set("Content-Type", "image/jpeg")
		case http.MethodGet:
			_, _ = w.Write(valid)
		case http.MethodDelete:
			calledDelete = true
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()
	endpoint := srv.URL
	client := s3.NewFromConfig(aws.Config{Region: "us-east-1", Credentials: credentials.NewStaticCredentialsProvider("access", "secret", "")}, func(o *s3.Options) { o.BaseEndpoint = &endpoint; o.UsePathStyle = true })
	service := &Service{s3: client, bucket: "test-bucket"}
	if err := service.verifyImageObject(t.Context(), "base/fixture.png", ".png"); err != ErrInvalidFile {
		t.Fatalf("metadata mismatch error = %v, want ErrInvalidFile", err)
	}
	if !calledDelete {
		t.Fatal("invalid metadata did not trigger DeleteObject")
	}
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func encodeJPEG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func encodeGIF(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := gif.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
