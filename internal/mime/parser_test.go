package mime

import (
	"strings"
	"testing"
)

func TestParsePlainText(t *testing.T) {
	raw := []byte("From: test@example.com\r\n" +
		"To: user@example.com\r\n" +
		"Subject: Hello\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n" +
		"\r\n" +
		"This is a test.")
	parts, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if parts.From != "test@example.com" {
		t.Errorf("From = %q, want %q", parts.From, "test@example.com")
	}
	if len(parts.To) != 1 || parts.To[0] != "user@example.com" {
		t.Errorf("To = %v, want [user@example.com]", parts.To)
	}
	if parts.Subject != "Hello" {
		t.Errorf("Subject = %q, want %q", parts.Subject, "Hello")
	}
	if parts.BodyText != "This is a test." {
		t.Errorf("BodyText = %q, want %q", parts.BodyText, "This is a test.")
	}
}

func TestParseMultipartAlternative(t *testing.T) {
	raw := []byte("From: test@example.com\r\n" +
		"To: user@example.com\r\n" +
		"Subject: Multi\r\n" +
		"Content-Type: multipart/alternative; boundary=xyz\r\n" +
		"\r\n" +
		"--xyz\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n" +
		"\r\n" +
		"Plain text\r\n" +
		"--xyz\r\n" +
		"Content-Type: text/html; charset=UTF-8\r\n" +
		"\r\n" +
		"<p>HTML text</p>\r\n" +
		"--xyz--\r\n")
	parts, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if parts.BodyText != "Plain text" {
		t.Errorf("BodyText = %q, want %q", parts.BodyText, "Plain text")
	}
	if parts.BodyHTML != "<p>HTML text</p>" {
		t.Errorf("BodyHTML = %q, want %q", parts.BodyHTML, "<p>HTML text</p>")
	}
}

func TestBase64Decode(t *testing.T) {
	raw := []byte("From: test@example.com\r\n" +
		"Subject: Base64\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		"\r\n" +
		"SGVsbG8gV29ybGQ=")
	parts, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if parts.BodyText != "Hello World" {
		t.Errorf("BodyText = %q, want %q", parts.BodyText, "Hello World")
	}
}

func TestRFC2047Decode(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"=?UTF-8?B?SGVsbG8=?=", "Hello"},
		{"=?UTF-8?Q?Hello?=", "Hello"},
		{"=?UTF-8?Q?H=C3=A9llo?=", "Héllo"},
		{"=?UTF-8?B?w6lsw6k=?=", "élé"},
	}
	for _, tt := range tests {
		got := decodeRFC2047(tt.input)
		if got != tt.expected {
			t.Errorf("decodeRFC2047(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestGenerateMessageID(t *testing.T) {
	id := GenerateMessageID("example.com")
	if id == "" {
		t.Fatal("GenerateMessageID returned empty")
	}
	if !strings.HasSuffix(id, "@example.com>") {
		t.Errorf("GenerateMessageID doesn't end with @example.com: %s", id)
	}
	if id[0] != '<' {
		t.Errorf("GenerateMessageID should start with <: %s", id)
	}
}

func TestBuildRaw(t *testing.T) {
	raw, err := BuildRaw("sender@example.com", "Test Subject",
		[]string{"recip@example.com"}, nil, "Hello World", "")
	if err != nil {
		t.Fatalf("BuildRaw failed: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("BuildRaw returned empty")
	}

	// Parse it back
	parts, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse back failed: %v", err)
	}
	if parts.Subject != "Test Subject" {
		t.Errorf("Subject = %q, want %q", parts.Subject, "Test Subject")
	}
	// QP encoding adds trailing CRLF
	if parts.BodyText != "Hello World\r\n" {
		t.Errorf("BodyText = %q, want %q", parts.BodyText, "Hello World\r\n")
	}
}

func TestBuildRawWithHTML(t *testing.T) {
	raw, err := BuildRaw("sender@example.com", "HTML Test",
		[]string{"recip@example.com"}, nil, "Text body", "<p>HTML body</p>")
	if err != nil {
		t.Fatalf("BuildRaw failed: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("BuildRaw returned empty")
	}

	// Parse it back
	parts, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse back failed: %v", err)
	}
	if parts.BodyText != "Text body" {
		t.Errorf("BodyText = %q, want %q", parts.BodyText, "Text body")
	}
	if parts.BodyHTML != "<p>HTML body</p>" {
		t.Errorf("BodyHTML = %q, want %q", parts.BodyHTML, "<p>HTML body</p>")
	}
}

func TestParseWithAttachments(t *testing.T) {
	raw := []byte("From: test@example.com\r\n" +
		"To: user@example.com\r\n" +
		"Subject: With Attach\r\n" +
		"Content-Type: multipart/mixed; boundary=xyz\r\n" +
		"\r\n" +
		"--xyz\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n" +
		"\r\n" +
		"Body text\r\n" +
		"--xyz\r\n" +
		"Content-Type: application/pdf\r\n" +
		"Content-Disposition: attachment; filename=test.pdf\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		"\r\n" +
		"AAAA\r\n" +
		"--xyz--\r\n")
	parts, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if parts.BodyText != "Body text" {
		t.Errorf("BodyText = %q, want %q", parts.BodyText, "Body text")
	}
	if len(parts.Attachments) != 1 {
		t.Fatalf("Expected 1 attachment, got %d", len(parts.Attachments))
	}
	if parts.Attachments[0].Filename != "test.pdf" {
		t.Errorf("Filename = %q, want %q", parts.Attachments[0].Filename, "test.pdf")
	}
	if parts.Attachments[0].MimeType != "application/pdf" {
		t.Errorf("MimeType = %q, want %q", parts.Attachments[0].MimeType, "application/pdf")
	}
}
