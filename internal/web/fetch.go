package web

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"html"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// maxFetchBytes is the raw response body read cap before text normalization.
// The tool-level cap (MaxToolResultBytes, 64 KiB) bounds what the model
// actually sees; this cap bounds the download.
const maxFetchBytes = 8 << 20

var (
	blockREs = []*regexp.Regexp{
		regexp.MustCompile(`(?s)<script[^>]*>.*?</script>`),
		regexp.MustCompile(`(?s)<style[^>]*>.*?</style>`),
		regexp.MustCompile(`(?s)<noscript[^>]*>.*?</noscript>`),
		regexp.MustCompile(`(?s)<svg[^>]*>.*?</svg>`),
	}
	htmlTagRE = regexp.MustCompile(`<[^>]+>`)

	// pdfStreamRE matches stream objects, optionally with FlateDecode.
	pdfStreamRE = regexp.MustCompile(`(?s)(/FlateDecode[^\n]*)?\r?\n?[^\n]*stream\r?\n(.*?)endstream`)
	// pdfTextOpRE matches one or more parenthesised strings followed by Tj/TJ.
	pdfTextOpRE = regexp.MustCompile(`(?:\((?:[^()\\]|\\.)*\)\s*)+T[Jj]`)
	// pdfStrRE extracts the inner content of each parenthesised string.
	pdfStrRE = regexp.MustCompile(`\(((?:[^()\\]|\\.)*)\)`)
)

// Fetch retrieves a single http(s) URL and returns its textual content.
//
// Contract ("URL in, text out"):
//
//   - Accepts http and https only. Rejects file://, ftp://, etc.
//   - Normalises HTML, plain text, and PDF to text via content sniffing
//     (Content-Type header plus payload heuristics).
//   - Does not run JavaScript, crawl links, manage sessions, or return
//     binary data.
//   - Response body is capped at 8 MiB; the returned text is further capped
//     by the tool-level MaxToolResultBytes (64 KiB).
//
// This contract is format-agnostic: PDF support is simply a codec behind the
// same "URL→text" interface. Lighter/better codecs can replace the
// implementation without changing the tool contract or the model's calling
// code.
func Fetch(rawURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("web fetch: %v", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("web fetch: unsupported scheme %q (only http/https)", u.Scheme)
	}

	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return "", fmt.Errorf("web fetch: %w", err)
	}
	req.Header.Set("User-Agent", "Motive/0.1 (+https://github.com/acidsound/Motive)")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("web fetch: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchBytes))
	if err != nil {
		return "", fmt.Errorf("web fetch: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("web fetch: %s", resp.Status)
	}

	mt := mediaType(resp.Header.Get("Content-Type"))
	switch {
	case isHTML(mt, body):
		return cleanHTML(string(body)), nil
	case isPDF(mt, u.Path, body):
		text, perr := PDFText(body)
		if perr != nil {
			return "", fmt.Errorf("web fetch: %w", perr)
		}
		return text, nil
	case strings.HasPrefix(mt, "text/"):
		return clean(string(body)), nil
	case isBinary(body):
		return "", fmt.Errorf("web fetch: %q is binary; web_fetch returns text only", mt)
	default:
		return clean(string(body)), nil
	}
}

// mediaType normalises a Content-Type header value to a lower-case media type.
func mediaType(h string) string {
	if h == "" {
		return ""
	}
	mt, _, err := mime.ParseMediaType(h)
	if err != nil {
		// Fallback: strip parameters manually.
		s := strings.TrimSpace(h)
		if i := strings.IndexByte(s, ';'); i >= 0 {
			s = s[:i]
		}
		return strings.ToLower(strings.TrimSpace(s))
	}
	return strings.ToLower(mt)
}

// isHTML reports whether the response looks like HTML.
func isHTML(mt string, body []byte) bool {
	if strings.Contains(mt, "html") {
		return true
	}
	// Empty or unknown content type: sniff the body.
	trimmed := bytes.TrimLeft(body, " \t\r\n\xef\xbb\xbf") // skip BOM
	return len(trimmed) > 0 && trimmed[0] == '<'
}

// isPDF reports whether the response looks like a PDF.
func isPDF(mt, path string, body []byte) bool {
	if mt == "application/pdf" {
		return true
	}
	if strings.HasSuffix(strings.ToLower(path), ".pdf") && (mt == "" || mt == "application/octet-stream") {
		return true
	}
	return bytes.HasPrefix(body, []byte("%PDF-"))
}

// isBinary reports whether the data appears to be binary (contains a null
// byte in the first kilobyte).
func isBinary(b []byte) bool {
	n := len(b)
	if n > 1024 {
		n = 1024
	}
	return bytes.IndexByte(b[:n], 0) >= 0
}

// cleanHTML strips block-level elements (script, style, etc.), removes tags,
// and unescapes HTML entities.
func cleanHTML(s string) string {
	for _, re := range blockREs {
		s = re.ReplaceAllString(s, " ")
	}
	s = htmlTagRE.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(s), " ")
}

// ── PDF text extraction ─────────────────────────────────────────────────────
//
// This is a lightweight, dependency-free PDF text extractor. It locates stream
// objects in the raw PDF, decompresses FlateDecoded streams with the standard
// library (compress/zlib), and extracts text from content-stream operator
// sequences (Tj and TJ operators only).
//
// This is intentionally not a general-purpose PDF parser. It covers the common
// case of simple text-based PDFs without pulling in a heavy library. The
// "URL→text" contract is format-agnostic, so this codec can be replaced with a
// better one without changing the tool contract.

// PDFText extracts text from a raw PDF byte slice.
func PDFText(data []byte) (string, error) {
	var parts []string
	for _, m := range pdfStreamRE.FindAllSubmatch(data, -1) {
		payload := m[2]
		if len(m[1]) > 0 {
			if zr, err := zlib.NewReader(bytes.NewReader(payload)); err == nil {
				if b, err := io.ReadAll(io.LimitReader(zr, maxFetchBytes)); err == nil {
					payload = b
				}
				zr.Close()
			}
		}
		for _, op := range pdfTextOpRE.FindAll(payload, -1) {
			for _, sm := range pdfStrRE.FindAllSubmatch(op, -1) {
				parts = append(parts, unescapePDFString(string(sm[1])))
			}
		}
	}
	text := strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
	if text == "" {
		return "", fmt.Errorf("no extractable text in PDF")
	}
	return text, nil
}

// unescapePDFString unescapes a PDF literal string (handles \(, \), \n, \r,
// \t, \b, \f, \\, and \ddd octal).
func unescapePDFString(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++ // skip backslash
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case '(', ')', '\\':
			b.WriteByte(s[i])
		default:
			if n, ok := pdfOctal(s, i); ok {
				b.WriteByte(n)
				i += 2 // skip the two remaining octal digits
			} else {
				b.WriteByte(s[i])
			}
		}
	}
	return b.String()
}

// pdfOctal parses up to three octal digits starting at s[i]. It returns the
// byte value and true on success.
func pdfOctal(s string, i int) (byte, bool) {
	if i >= len(s) || s[i] < '0' || s[i] > '7' {
		return 0, false
	}
	v := 0
	for k := 0; k < 3 && i+k < len(s); k++ {
		c := s[i+k]
		if c < '0' || c > '7' {
			break
		}
		v = v*8 + int(c-'0')
	}
	return byte(v), true
}
