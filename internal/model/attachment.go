package model

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	// Register decoders so image.Decode handles the common web formats.
	_ "image/gif"

	_ "golang.org/x/image/bmp"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// Attachment is one user-supplied file attached to a turn. Kind is one of
// "image", "video", or "file". Path is always absolute; Name is the base name
// used for display. The path stays valid for the whole session so a resumed
// session can re-attach the file.
type Attachment struct {
	Name string `json:"name"`
	Path string `json:"path"`
	MIME string `json:"mime,omitempty"`
	Kind string `json:"kind,omitempty"` // image | video | file
	Size int64  `json:"size,omitempty"`
}

// ContentPart is one element of a multimodal user message in the
// OpenAI-compatible content array format.
type ContentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
	VideoURL *VideoURL `json:"video_url,omitempty"`
}

type ImageURL struct {
	URL string `json:"url"`
}

// VideoURL mirrors the image_url shape; Qwen-style servers accept
// {"type":"video_url","video_url":{"url":...}} for video input. Motive does
// not emit video_url parts: no common OpenAI-compatible backend accepts a
// base64 data URI there, so videos are attached as path references instead
// (see Attachment.ContentParts).
type VideoURL struct {
	URL string `json:"url"`
}

// MaxDataURIBytes caps how large an attachment may be inlined as a base64
// data URI in the request body. Larger files are referenced by path in the
// text part instead; the model can still read them through the workspace
// tools (read_file, shell).
const MaxDataURIBytes = 20 << 20

// MaxImageDim is the longest edge (pixels) an image may have after client-side
// downscaling before it is inlined. Vision backends tokenize images roughly
// proportionally to their pixel count, so a full-resolution screenshot can
// cost tens of thousands of prompt tokens or be rejected outright; downscaling
// first keeps the request small and inference fast.
const MaxImageDim = 1280

// jpegQuality is the re-encode quality for downscaled photographs.
const jpegQuality = 85

// sniffableImageMIMEs are the image types http.DetectContentType recognizes.
// A file with one of these extensions whose content sniffs as something else
// is misnamed or not really an image and is treated as a plain file. Formats
// DetectContentType cannot recognize (svg, tiff, avif) are not listed here, so
// their extension-derived image type is preserved.
var sniffableImageMIMEs = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
	"image/bmp":  true,
}

var imageExts = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".bmp":  "image/bmp",
	".tif":  "image/tiff",
	".tiff": "image/tiff",
	".svg":  "image/svg+xml",
	".avif": "image/avif",
}

var videoExts = map[string]string{
	".mp4":  "video/mp4",
	".mov":  "video/quicktime",
	".webm": "video/webm",
	".mkv":  "video/x-matroska",
	".avi":  "video/x-msvideo",
	".m4v":  "video/mp4",
	".mpeg": "video/mpeg",
	".mpg":  "video/mpeg",
}

// DetectAttachment classifies a file on disk. The kind is derived from the
// extension and, for images, verified against the file signature so a
// misnamed file is still attached as an image when it actually is one.
func DetectAttachment(path string) (Attachment, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Attachment{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return Attachment{}, err
	}
	if info.IsDir() {
		return Attachment{}, fmt.Errorf("%s is a directory, not a file", path)
	}
	att := Attachment{
		Name: filepath.Base(abs),
		Path: abs,
		Size: info.Size(),
	}
	ext := strings.ToLower(filepath.Ext(abs))
	if m, ok := imageExts[ext]; ok {
		att.Kind = "image"
		att.MIME = m
	} else if m, ok := videoExts[ext]; ok {
		att.Kind = "video"
		att.MIME = m
	} else {
		att.Kind = "file"
		if m := mime.TypeByExtension(ext); m != "" {
			att.MIME = m
		}
	}
	// Verify image signatures so renamed files keep working: a sniffed image
	// type wins over the extension. Formats that http.DetectContentType cannot
	// recognize (svg, tiff, avif) keep their extension-derived image type;
	// only a sniffable-image extension whose content is not an image at all is
	// downgraded to a plain file.
	if att.Kind == "image" {
		if f, err := os.Open(abs); err == nil {
			head := make([]byte, 512)
			n, _ := f.Read(head)
			f.Close()
			if ct := http.DetectContentType(head[:n]); strings.HasPrefix(ct, "image/") {
				att.MIME = ct
			} else if sniffableImageMIMEs[att.MIME] {
				att.Kind = "file"
				att.MIME = ct
			}
		}
	}
	return att, nil
}

// DataURI returns the file contents as a base64 data URI usable in an
// image_url content part. Images are downscaled to at most MaxImageDim on the
// longest edge and re-encoded before inlining so the request body stays small;
// formats Go cannot decode (svg, tiff, avif) are inlined raw. It returns an
// error when the file exceeds MaxDataURIBytes or cannot be read.
func (a Attachment) DataURI() (string, error) {
	if a.Size > MaxDataURIBytes {
		return "", fmt.Errorf("%s is %d bytes, exceeding the %d byte inline limit", a.Name, a.Size, MaxDataURIBytes)
	}
	data, err := os.ReadFile(a.Path)
	if err != nil {
		return "", err
	}
	mimeType := a.MIME
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	if a.Kind == "image" {
		if enc, encMIME := downscaleImage(data, mimeType); enc != nil {
			data = enc
			mimeType = encMIME
		}
	}
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

// downscaleImage shrinks a decodable image so its longest edge is at most
// MaxImageDim, re-encoding it for the request body. It returns nil when the
// image already fits (the original bytes stay inlined) or when the data is not
// a decodable image, leaving the raw bytes to the backend.
func downscaleImage(data []byte, mimeType string) ([]byte, string) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, ""
	}
	b := img.Bounds()
	if b.Dx() <= MaxImageDim && b.Dy() <= MaxImageDim {
		return nil, ""
	}
	scale := float64(MaxImageDim) / float64(max(b.Dx(), b.Dy()))
	nw, nh := int(float64(b.Dx())*scale), int(float64(b.Dy())*scale)
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
	var buf bytes.Buffer
	if strings.HasPrefix(mimeType, "image/jpeg") {
		if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: jpegQuality}); err != nil {
			return nil, ""
		}
		return buf.Bytes(), "image/jpeg"
	}
	// Lossless/palette formats (png, gif, webp, bmp, tiff, …) re-encode as PNG
	// so alpha and text stay crisp.
	if err := png.Encode(&buf, dst); err != nil {
		return nil, ""
	}
	return buf.Bytes(), "image/png"
}

// formatBytes renders a byte count for the attachment manifest.
func formatBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// ContentParts renders the attachment as content parts for a multimodal
// message. Images become data-URI image_url parts when they fit the inline
// limit (downscaled client-side when needed); videos and any other file
// become path references the model can reach through the workspace tools.
// No common OpenAI-compatible backend accepts a base64 video_url part, so a
// video path reference plus a frame-extraction hint is strictly more useful.
func (a Attachment) ContentParts() []ContentPart {
	switch a.Kind {
	case "image":
		if uri, err := a.DataURI(); err == nil {
			return []ContentPart{{Type: "image_url", ImageURL: &ImageURL{URL: uri}}}
		}
	}
	return []ContentPart{{Type: "text", Text: a.pathReference()}}
}

// pathReference renders the attachment as a text part pointing at the file.
func (a Attachment) pathReference() string {
	if a.Kind == "video" {
		return fmt.Sprintf("[attached video: %s at %s; size=%s; extract frames with the shell tool, e.g. ffmpeg -i %s -vf fps=1 frame-%%03d.png]", a.Name, a.Path, formatBytes(a.Size), a.Path)
	}
	return fmt.Sprintf("[attached %s: %s at %s; size=%s]", a.Kind, a.Name, a.Path, formatBytes(a.Size))
}

// BuildUserMessage builds the first user message of a turn. With no
// attachments it stays a plain string so existing servers see the same
// request shape as before; with attachments it becomes the standard
// OpenAI-compatible content array (text part first, then one part per
// attachment). Inlined images are preceded by a one-line annotation naming
// the file so the model can refer to it in later tool calls.
func BuildUserMessage(text string, attachments []Attachment) Message {
	if len(attachments) == 0 {
		return Message{Role: "user", Content: text}
	}
	parts := make([]ContentPart, 0, 1+2*len(attachments))
	parts = append(parts, ContentPart{Type: "text", Text: text})
	for _, a := range attachments {
		cp := a.ContentParts()
		if len(cp) == 1 && cp[0].Type == "image_url" {
			parts = append(parts, ContentPart{Type: "text", Text: fmt.Sprintf("[attached image: %s (%s)]", a.Name, formatBytes(a.Size))})
		}
		parts = append(parts, cp...)
	}
	return Message{Role: "user", ContentParts: parts}
}
