package model

import (
	"encoding/base64"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
// {"type":"video_url","video_url":{"url":...}} for video input.
type VideoURL struct {
	URL string `json:"url"`
}

// MaxDataURIBytes caps how large an attachment may be inlined as a base64
// data URI in the request body. Larger files are referenced by path in the
// text part instead; the model can still read them through the workspace
// tools (read_file, shell).
const MaxDataURIBytes = 64 << 20

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
	// Verify image signatures so renamed files keep working; when the
	// signature disagrees, fall back to the detected content type.
	if att.Kind == "image" {
		if f, err := os.Open(abs); err == nil {
			head := make([]byte, 512)
			n, _ := f.Read(head)
			f.Close()
			if ct := http.DetectContentType(head[:n]); strings.HasPrefix(ct, "image/") {
				att.MIME = ct
			} else {
				att.Kind = "file"
				att.MIME = ct
			}
		}
	}
	return att, nil
}

// DataURI returns the file contents as a base64 data URI usable in an
// image_url / video_url content part. It returns an error when the file
// exceeds MaxDataURIBytes or cannot be read.
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
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

// ContentParts renders the attachment as one content part for a multimodal
// message. Images and videos become data-URI parts when they fit the inline
// limit; anything else (or anything too large) becomes a path reference so
// the model can still reach the file through the workspace tools.
func (a Attachment) ContentParts() []ContentPart {
	switch a.Kind {
	case "image", "video":
		if uri, err := a.DataURI(); err == nil {
			if a.Kind == "video" {
				return []ContentPart{{Type: "video_url", VideoURL: &VideoURL{URL: uri}}}
			}
			return []ContentPart{{Type: "image_url", ImageURL: &ImageURL{URL: uri}}}
		}
	}
	return []ContentPart{{Type: "text", Text: fmt.Sprintf("[attached file %s at %s; kind=%s size=%d bytes]", a.Name, a.Path, a.Kind, a.Size)}}
}

// BuildUserMessage builds the first user message of a turn. With no
// attachments it stays a plain string so existing servers see the same
// request shape as before; with attachments it becomes the standard
// OpenAI-compatible content array (text part first, then one part per
// attachment).
func BuildUserMessage(text string, attachments []Attachment) Message {
	if len(attachments) == 0 {
		return Message{Role: "user", Content: text}
	}
	parts := make([]ContentPart, 0, 1+len(attachments))
	parts = append(parts, ContentPart{Type: "text", Text: text})
	for _, a := range attachments {
		parts = append(parts, a.ContentParts()...)
	}
	return Message{Role: "user", ContentParts: parts}
}
