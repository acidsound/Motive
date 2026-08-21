package tui

import (
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	llm "github.com/acidsound/Motive/internal/model"
)

func TestBuildUserMessageParts(t *testing.T) {
	msg := llm.BuildUserMessage("describe", nil)
	if msg.Content != "describe" || len(msg.ContentParts) != 0 {
		t.Fatalf("plain message = %+v", msg)
	}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	var plain map[string]any
	if err := json.Unmarshal(data, &plain); err != nil {
		t.Fatal(err)
	}
	if plain["content"] != "describe" {
		t.Fatalf("plain content = %v", plain["content"])
	}

	// With a real image file on disk the attachment becomes an image_url part
	// (data URI); a missing file would fall back to a path-reference text part.
	dir := t.TempDir()
	img := filepath.Join(dir, "x.png")
	if err := writePNG(img, 4, 4); err != nil {
		t.Fatal(err)
	}
	att, err := llm.DetectAttachment(img)
	if err != nil {
		t.Fatal(err)
	}
	msg = llm.BuildUserMessage("see this", []llm.Attachment{att})
	if len(msg.ContentParts) != 2 {
		t.Fatalf("parts = %d, want 2", len(msg.ContentParts))
	}
	if msg.ContentParts[0].Type != "text" || msg.ContentParts[0].Text != "see this" {
		t.Errorf("text part = %+v", msg.ContentParts[0])
	}
	if msg.ContentParts[1].Type != "image_url" {
		t.Errorf("image part = %+v", msg.ContentParts[1])
	}
	data, err = json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Content []map[string]any `json:"content"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Content) != 2 || got.Content[0]["type"] != "text" || got.Content[1]["type"] != "image_url" {
		t.Fatalf("serialized content = %v", got.Content)
	}
}

func TestDetectAttachmentKinds(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "shot.png")
	if err := writePNG(img, 4, 4); err != nil {
		t.Fatal(err)
	}
	txt := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(txt, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	att, err := llm.DetectAttachment(img)
	if err != nil {
		t.Fatal(err)
	}
	if att.Kind != "image" || att.MIME != "image/png" || att.Name != "shot.png" {
		t.Errorf("image attach = %+v", att)
	}
	uri, err := att.DataURI()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(uri, "data:image/png;base64,") {
		t.Errorf("data uri = %.40s", uri)
	}

	att, err = llm.DetectAttachment(txt)
	if err != nil {
		t.Fatal(err)
	}
	if att.Kind != "file" {
		t.Errorf("text attach kind = %q", att.Kind)
	}
	if len(att.ContentParts()) != 1 || att.ContentParts()[0].Type != "text" {
		t.Errorf("file parts = %+v", att.ContentParts())
	}

	if _, err := llm.DetectAttachment(filepath.Join(dir, "missing.png")); err == nil {
		t.Error("missing file should error")
	}
	if _, err := llm.DetectAttachment(dir); err == nil {
		t.Error("directory should error")
	}
}

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

func TestKittyImageChunking(t *testing.T) {
	// A payload larger than two kittyChunkSize blocks must be split into
	// continuation chunks (m=1) with a final m=0 chunk.
	enc := strings.Repeat("A", kittyChunkSize*2+100)
	out := kittyImage(enc)
	if !strings.HasPrefix(out, "\x1b_Ga=T,f=100,m=1;") {
		t.Errorf("first chunk header wrong: %.30q", out)
	}
	if !strings.Contains(out, "\x1b_Gm=1;") {
		t.Error("missing continuation chunk")
	}
	if !strings.HasSuffix(out, "\x1b_Gm=0;"+enc[len(enc)-100:]+"\x1b\\") {
		t.Error("final chunk wrong")
	}
}

func TestDirPickerResolveTypedPath(t *testing.T) {
	base := t.TempDir()
	sub := filepath.Join(base, "sub")
	os.Mkdir(sub, 0o755)
	file := filepath.Join(base, "a.txt")
	os.WriteFile(file, []byte("x"), 0o644)

	p := newDirPicker(base, 80, 24)

	// Absolute path to a file.
	if path, isDir, ok := p.resolveTypedPath(file); !ok || isDir || path != file {
		t.Errorf("absolute file = %q %v %v", path, isDir, ok)
	}
	// Relative path resolved against the picker directory.
	if path, isDir, ok := p.resolveTypedPath("sub"); !ok || !isDir || path != sub {
		t.Errorf("relative dir = %q %v %v", path, isDir, ok)
	}
	if path, _, ok := p.resolveTypedPath("a.txt"); !ok || path != file {
		t.Errorf("relative file = %q %v", path, ok)
	}
	// Missing paths do not resolve.
	if _, _, ok := p.resolveTypedPath("nope.txt"); ok {
		t.Error("missing path resolved")
	}
}

func TestAttachOverlayCommitsFile(t *testing.T) {
	base := t.TempDir()
	img := filepath.Join(base, "pic.png")
	if err := writePNG(img, 8, 8); err != nil {
		t.Fatal(err)
	}
	m := newTestModel()
	m.width = 80
	m.height = 24
	// Remove the ws-root line; newTestModel already has rt.WS == nil.
	m2, _ := m.handleKey(teaKey("ctrl+a"))
	m = *m2.(*model)
	if m.overlay != overlayAttach {
		t.Fatalf("overlay = %v, want attach", m.overlay)
	}
	// Attach by typed absolute path.
	m2, _ = m.handleKey(teaKey("enter"))
	m = *m2.(*model)
	if m.overlay != overlayAttach {
		t.Fatalf("empty enter should keep overlay open")
	}
	for _, r := range img {
		m2, _ = m.handleKey(teaKey(string(r)))
		m = *m2.(*model)
	}
	m2, _ = m.handleKey(teaKey("enter"))
	m = *m2.(*model)
	if m.overlay != overlayNone {
		t.Fatalf("overlay = %v, want closed after attach", m.overlay)
	}
	if len(m.attachments) != 1 {
		t.Fatalf("attachments = %d, want 1", len(m.attachments))
	}
	if m.attachments[0].Kind != "image" || m.attachments[0].Name != "pic.png" {
		t.Errorf("attachment = %+v", m.attachments[0])
	}
	// The pre-rendered thumbnail is only present on inline-image terminals;
	// in tests it must at least not panic and stay consistent.
	m.View()
}

// teaKey builds a tea.KeyPressMsg whose String() equals s, matching how
// handleKey dispatches: printable text via Text, special keys via Code, and
// ctrl+<char> via Code+ModCtrl.
func teaKey(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEsc}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	}
	if strings.HasPrefix(s, "ctrl+") && len(s) == 6 {
		return tea.KeyPressMsg{Code: rune(s[5]), Mod: tea.ModCtrl}
	}
	return tea.KeyPressMsg{Text: s}
}
