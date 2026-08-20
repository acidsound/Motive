package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><script>bad()</script><h1>Hello</h1><p>World &amp; more</p></body></html>`))
	}))
	defer srv.Close()

	out, err := Fetch(srv.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !strings.Contains(out, "Hello") || !strings.Contains(out, "World & more") {
		t.Fatalf("out = %q, want title and text content", out)
	}
	if strings.Contains(out, "bad()") {
		t.Fatalf("script content leaked: %q", out)
	}
}

func TestFetchPlainText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("alpha beta\tgamma\n"))
	}))
	defer srv.Close()

	out, err := Fetch(srv.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !strings.Contains(out, "alpha beta gamma") {
		t.Fatalf("out = %q, want normalized plain text", out)
	}
}

func TestFetchSniffsHTMLWithoutContentType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<!DOCTYPE html><html><body>sniffed html</body></html>`))
	}))
	defer srv.Close()

	out, err := Fetch(srv.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !strings.Contains(out, "sniffed html") {
		t.Fatalf("out = %q, want sniffed html content", out)
	}
}

func TestFetchPDF(t *testing.T) {
	pdf := minimalPDF("Hello PDF World")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Write(pdf)
	}))
	defer srv.Close()

	out, err := Fetch(srv.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !strings.Contains(out, "Hello PDF World") {
		t.Fatalf("out = %q, want PDF text", out)
	}
}

func TestFetchRejectsNonHTTPScheme(t *testing.T) {
	if _, err := Fetch("file:///etc/passwd"); err == nil {
		t.Fatal("expected error for file:// scheme")
	}
	if _, err := Fetch("ftp://example.com/x"); err == nil {
		t.Fatal("expected error for ftp:// scheme")
	}
}

func TestFetchRejectsBinary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00})
	}))
	defer srv.Close()

	if _, err := Fetch(srv.URL); err == nil {
		t.Fatal("expected error for binary content")
	}
}

func TestPDFText(t *testing.T) {
	out, err := PDFText(minimalPDF("Line one & Line two"))
	if err != nil {
		t.Fatalf("PDFText: %v", err)
	}
	if !strings.Contains(out, "Line one") || !strings.Contains(out, "Line two") {
		t.Fatalf("out = %q, want extracted lines", out)
	}
}

func TestPDFTextEscapes(t *testing.T) {
	out, err := PDFText(minimalPDF("has \\( paren \\n backslash-n"))
	if err != nil {
		t.Fatalf("PDFText: %v", err)
	}
	if !strings.Contains(out, "has ( paren") {
		t.Fatalf("out = %q, want unescaped literal", out)
	}
}

// minimalPDF builds a tiny single-page PDF with one text-showing operator.
func minimalPDF(text string) []byte {
	content := "BT /F1 12 Tf 72 720 Td (" + text + ") Tj ET"
	return []byte("%PDF-1.4\n" +
		"1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n" +
		"2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n" +
		"3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>\nendobj\n" +
		"4 0 obj\n<< /Length " + itoa(len(content)) + " >>\nstream\n" + content + "\nendstream\nendobj\n" +
		"5 0 obj\n<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>\nendobj\n" +
		"trailer\n<< /Root 1 0 R >>\n%%EOF\n")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
