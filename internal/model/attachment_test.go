package model

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePNG(path string, w, h int) error {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 40), G: uint8(y * 40), B: 80, A: 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

// TestDetectAttachmentKeepsNonSniffableImageTypes guards the signature check
// against downgrading valid image types that http.DetectContentType cannot
// recognize (svg, tiff, avif): they must stay images and keep their
// extension-derived MIME.
func TestDetectAttachmentKeepsNonSniffableImageTypes(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		ext  string
		data []byte
		mime string
	}{
		{"logo.svg", ".svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><rect width="10" height="10"/></svg>`), "image/svg+xml"},
		{"scan.tiff", ".tiff", append([]byte("II*\x00"), make([]byte, 64)...), "image/tiff"},
		{"photo.avif", ".avif", []byte{0x00, 0x00, 0x00, 0x20, 'f', 't', 'y', 'p', 'a', 'v', 'i', 'f'}, "image/avif"},
	}
	for _, c := range cases {
		path := filepath.Join(dir, c.name)
		if err := os.WriteFile(path, c.data, 0o644); err != nil {
			t.Fatal(err)
		}
		att, err := DetectAttachment(path)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if att.Kind != "image" {
			t.Errorf("%s: kind = %q, want image", c.name, att.Kind)
		}
		if att.MIME != c.mime {
			t.Errorf("%s: MIME = %q, want %q", c.name, att.MIME, c.mime)
		}
	}
}

// TestDetectAttachmentDowngradesMisnamedImage verifies that a sniffable image
// extension whose content is not an image is treated as a plain file instead
// of being inlined as a bogus image.
func TestDetectAttachmentDowngradesMisnamedImage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.png")
	if err := os.WriteFile(path, []byte("plain text, not an image"), 0o644); err != nil {
		t.Fatal(err)
	}
	att, err := DetectAttachment(path)
	if err != nil {
		t.Fatal(err)
	}
	if att.Kind != "file" {
		t.Errorf("kind = %q, want file", att.Kind)
	}
	if att.MIME != "text/plain; charset=utf-8" {
		t.Errorf("MIME = %q, want text/plain", att.MIME)
	}
}

// TestDataURIDownscalesLargeImage verifies that an image larger than
// MaxImageDim is scaled down client-side and re-encoded before inlining, and
// that the payload decodes back to an image within the limit.
func TestDataURIDownscalesLargeImage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.png")
	if err := writePNG(path, 2000, 1200); err != nil {
		t.Fatal(err)
	}
	att, err := DetectAttachment(path)
	if err != nil {
		t.Fatal(err)
	}
	uri, err := att.DataURI()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(uri, "data:image/png;base64,") {
		t.Fatalf("uri = %.40s, want image/png data uri", uri)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(uri, "data:image/png;base64,"))
	if err != nil {
		t.Fatal(err)
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decoded payload: %v", err)
	}
	b := img.Bounds()
	if b.Dx() > MaxImageDim || b.Dy() > MaxImageDim {
		t.Errorf("downscaled dims = %dx%d, want longest edge <= %d", b.Dx(), b.Dy(), MaxImageDim)
	}
	if b.Dx() != 1280 || b.Dy() != 768 {
		t.Errorf("dims = %dx%d, want 1280x768 (aspect preserved)", b.Dx(), b.Dy())
	}
}

// TestDataURIDownscalesJPEGReencodesAsJPEG verifies photographs keep the
// JPEG container (and its smaller payload) after downscaling.
func TestDataURIDownscalesJPEGReencodesAsJPEG(t *testing.T) {
	dir := t.TempDir()
	// Build a 1600x900 JPEG from a generated PNG frame.
	tmp := filepath.Join(dir, "frame.png")
	if err := writePNG(tmp, 1600, 900); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(tmp)
	if err != nil {
		t.Fatal(err)
	}
	img, _, err := image.Decode(bytes.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "photo.jpg")
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(out, img, nil); err != nil {
		out.Close()
		t.Fatal(err)
	}
	out.Close()

	att, err := DetectAttachment(path)
	if err != nil {
		t.Fatal(err)
	}
	uri, err := att.DataURI()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(uri, "data:image/jpeg;base64,") {
		t.Fatalf("uri = %.40s, want image/jpeg data uri", uri)
	}
}

// TestDataURIKeepsSmallImageRaw verifies that images already within the
// dimension limit are inlined byte-for-byte (no lossy re-encode).
func TestDataURIKeepsSmallImageRaw(t *testing.T) {
	path := filepath.Join(t.TempDir(), "small.png")
	if err := writePNG(path, 4, 4); err != nil {
		t.Fatal(err)
	}
	att, err := DetectAttachment(path)
	if err != nil {
		t.Fatal(err)
	}
	uri, err := att.DataURI()
	if err != nil {
		t.Fatal(err)
	}
	fileBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "data:image/png;base64," + base64.StdEncoding.EncodeToString(fileBytes)
	if uri != want {
		t.Error("small image was not inlined raw")
	}
}

// TestVideoAttachmentPathReference verifies videos are never inlined as
// video_url data URIs: they become path references with a frame-extraction
// hint the model can act on.
func TestVideoAttachmentPathReference(t *testing.T) {
	att := Attachment{Name: "clip.mp4", Path: "/tmp/clip.mp4", MIME: "video/mp4", Kind: "video", Size: MaxDataURIBytes + 1}
	parts := att.ContentParts()
	if len(parts) != 1 || parts[0].Type != "text" {
		t.Fatalf("video parts = %+v, want one text part", parts)
	}
	text := parts[0].Text
	for _, want := range []string{"/tmp/clip.mp4", "ffmpeg", "video"} {
		if !strings.Contains(text, want) {
			t.Errorf("video reference missing %q: %s", want, text)
		}
	}
	if _, err := att.DataURI(); err == nil {
		t.Error("video DataURI should error on size before any inlining")
	}
}

// TestImageOverInlineLimitFallsBackToPath verifies that an image too large to
// inline becomes a path-reference text part instead of failing the turn.
func TestImageOverInlineLimitFallsBackToPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.png")
	if err := writePNG(path, 4, 4); err != nil {
		t.Fatal(err)
	}
	att, err := DetectAttachment(path)
	if err != nil {
		t.Fatal(err)
	}
	att.Size = MaxDataURIBytes + 1 // pretend the file exceeds the inline limit
	parts := att.ContentParts()
	if len(parts) != 1 || parts[0].Type != "text" {
		t.Fatalf("parts = %+v, want path-reference text part", parts)
	}
	if !strings.Contains(parts[0].Text, path) {
		t.Errorf("fallback text missing path: %s", parts[0].Text)
	}
}

// TestBuildUserMessageAnnotations verifies that an inlined image is preceded
// by an annotation naming the file, and that path-referenced attachments are
// not double-annotated.
func TestBuildUserMessageAnnotations(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "shot.png")
	if err := writePNG(img, 4, 4); err != nil {
		t.Fatal(err)
	}
	att, err := DetectAttachment(img)
	if err != nil {
		t.Fatal(err)
	}
	msg := BuildUserMessage("see this", []Attachment{att})
	if len(msg.ContentParts) != 3 {
		t.Fatalf("parts = %d, want 3 (text, annotation, image_url)", len(msg.ContentParts))
	}
	if msg.ContentParts[0].Type != "text" || msg.ContentParts[0].Text != "see this" {
		t.Errorf("text part = %+v", msg.ContentParts[0])
	}
	if msg.ContentParts[1].Type != "text" || !strings.HasPrefix(msg.ContentParts[1].Text, "[attached image: shot.png") {
		t.Errorf("annotation part = %+v", msg.ContentParts[1])
	}
	if msg.ContentParts[2].Type != "image_url" {
		t.Errorf("image part = %+v", msg.ContentParts[2])
	}

	// A path-referenced file gets exactly one part: the reference itself.
	file := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(file, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	fatt, err := DetectAttachment(file)
	if err != nil {
		t.Fatal(err)
	}
	msg = BuildUserMessage("read", []Attachment{fatt})
	if len(msg.ContentParts) != 2 {
		t.Fatalf("file parts = %d, want 2 (text, reference)", len(msg.ContentParts))
	}
	if msg.ContentParts[1].Type != "text" || !strings.Contains(msg.ContentParts[1].Text, "notes.md") {
		t.Errorf("file reference = %+v", msg.ContentParts[1])
	}
}
