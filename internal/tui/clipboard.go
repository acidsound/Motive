package tui

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"

	tea "charm.land/bubbletea/v2"

	llm "github.com/acidsound/Motive/internal/model"
)

// clipImageMsg is the result of an async clipboard image read.
type clipImageMsg struct {
	data []byte
	mime string
	err  error
}

// pasteImageCmd reads the clipboard image off the UI goroutine so a slow
// osascript/wl-paste call does not freeze the input.
func pasteImageCmd() tea.Cmd {
	return func() tea.Msg {
		data, mime, err := clipboardImage()
		return clipImageMsg{data: data, mime: mime, err: err}
	}
}

// handleClipImage attaches the clipboard image (when present) or shows the
// failure as a transient notice above the input box.
func (m *model) handleClipImage(msg clipImageMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.notice = msg.err.Error()
		return m, nil
	}
	path, err := saveClipboardImage(msg.data, msg.mime)
	if err != nil {
		m.notice = "clipboard image: " + err.Error()
		return m, nil
	}
	att, err := llm.DetectAttachment(path)
	if err != nil {
		m.notice = "clipboard image: " + err.Error()
		return m, nil
	}
	m.attachments = append(m.attachments, attachItem{Attachment: att, thumb: thumbnail(att.Path)})
	m.notice = "📋 " + att.Name + " attached from clipboard"
	return m, nil
}

// clipboardImage reads an image from the system clipboard using platform-
// specific tools. On macOS it tries osascript with PNG, GIF, then JPEG class
// codes. On Linux it tries wl-paste (Wayland) then xclip (X11). On Windows it
// returns an error. The returned bytes are the raw image data; the caller
// should detect the actual MIME type from the bytes.
func clipboardImage() (data []byte, mime string, err error) {
	switch runtime.GOOS {
	case "darwin":
		return clipboardImageDarwin()
	case "linux":
		return clipboardImageLinux()
	default:
		return nil, "", fmt.Errorf("clipboard image not supported on %s", runtime.GOOS)
	}
}

func clipboardImageDarwin() ([]byte, string, error) {
	// Try PNG, then GIF, then JPEG (the most common screenshot format).
	for _, pair := range [][2]string{
		{"PNGf", "image/png"},
		{"GIFf", "image/gif"},
		{"JPEG", "image/jpeg"},
	} {
		cmd := exec.Command("osascript", "-e", "get the clipboard as «class "+pair[0]+"»")
		out, err := cmd.Output()
		if err == nil && len(out) > 4 {
			// osascript often appends a trailing newline; strip it.
			// The data is raw binary for these types.
			return bytesTrimTrailingNewline(out), pair[1], nil
		}
	}
	return nil, "", fmt.Errorf("clipboard does not contain an image")
}

func clipboardImageLinux() ([]byte, string, error) {
	// Try wl-paste (Wayland) first, then xclip (X11).
	if _, err := exec.LookPath("wl-paste"); err == nil {
		cmd := exec.Command("wl-paste", "-t", "image/png")
		out, err := cmd.Output()
		if err == nil && len(out) > 4 {
			return out, "image/png", nil
		}
	}
	if _, err := exec.LookPath("xclip"); err == nil {
		cmd := exec.Command("xclip", "-selection", "clipboard", "-t", "image/png", "-o")
		out, err := cmd.Output()
		if err == nil && len(out) > 4 {
			return out, "image/png", nil
		}
	}
	return nil, "", fmt.Errorf("clipboard image not found (try wl-paste or xclip)")
}

func bytesTrimTrailingNewline(b []byte) []byte {
	if len(b) > 0 && b[len(b)-1] == '\n' {
		return b[:len(b)-1]
	}
	return b
}

// saveClipboardImage writes image bytes to a temp file and returns the path.
// The file is not cleaned up automatically; the caller should treat it as a
// session attachment whose lifetime matches the TUI session.
func saveClipboardImage(data []byte, mime string) (string, error) {
	ext := ".bin"
	switch mime {
	case "image/png":
		ext = ".png"
	case "image/gif":
		ext = ".gif"
	case "image/jpeg":
		ext = ".jpg"
	}
	f, err := os.CreateTemp("", "motive-clip-*"+ext)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}