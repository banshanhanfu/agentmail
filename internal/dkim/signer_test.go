package dkim

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
)

func TestGenerateKeyPair(t *testing.T) {
	pub, priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}
	if len(pub) == 0 {
		t.Error("public key is empty")
	}
	if len(priv) == 0 {
		t.Error("private key is empty")
	}

	// Verify the private key can be parsed
	block, _ := pem.Decode(priv)
	if block == nil {
		t.Fatal("no PEM block in private key")
	}
	if block.Type != "PRIVATE KEY" {
		t.Errorf("private key type = %q, want PRIVATE KEY", block.Type)
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("failed to parse private key: %v", err)
	}
	if _, ok := key.(*rsa.PrivateKey); !ok {
		t.Fatalf("key is not RSA, got %T", key)
	}

	// Verify public key
	block, _ = pem.Decode(pub)
	if block == nil {
		t.Fatal("no PEM block in public key")
	}
	if block.Type != "PUBLIC KEY" {
		t.Errorf("public key type = %q, want PUBLIC KEY", block.Type)
	}
}

func TestSign(t *testing.T) {
	_, priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	email := []byte("From: sender@example.com\r\n" +
		"To: recipient@example.com\r\n" +
		"Subject: Test DKIM\r\n" +
		"Date: Mon, 1 Jan 2024 00:00:00 +0000\r\n" +
		"Message-ID: <test@example.com>\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n" +
		"\r\n" +
		"Hello World")

	signed, err := Sign(priv, "example.com", "default", email)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	if len(signed) <= len(email) {
		t.Error("signed email should be longer than original")
	}

	// Check DKIM-Signature header is present
	if !bytes.HasPrefix(signed, []byte("DKIM-Signature: ")) {
		t.Errorf("signed email should start with DKIM-Signature header, got:\n%s", signed[:100])
	}

	// Check the header contains expected fields
	signedStr := string(signed)
	if !strings.Contains(signedStr, "v=1") {
		t.Error("DKIM-Signature missing v=1")
	}
	if !strings.Contains(signedStr, "a=rsa-sha256") {
		t.Error("DKIM-Signature missing a=rsa-sha256")
	}
	if !strings.Contains(signedStr, "d=example.com") {
		t.Error("DKIM-Signature missing d=example.com")
	}
	if !strings.Contains(signedStr, "s=default") {
		t.Error("DKIM-Signature missing s=default")
	}
	if !strings.Contains(signedStr, "bh=") {
		t.Error("DKIM-Signature missing bh=")
	}
	if !strings.Contains(signedStr, "b=") {
		t.Error("DKIM-Signature missing b=")
	}

	// Verify the original body is intact
	if !bytes.Contains(signed, []byte("Hello World")) {
		t.Error("signed email missing original body")
	}
}

func TestSignWithCustomSelector(t *testing.T) {
	_, priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	email := []byte("From: test@example.com\r\n" +
		"To: user@example.com\r\n" +
		"Subject: Custom Selector\r\n" +
		"Date: Mon, 1 Jan 2024 00:00:00 +0000\r\n" +
		"Message-ID: <test@example.com>\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n" +
		"\r\n" +
		"Body")

	signed, err := Sign(priv, "example.com", "dkim2024", email)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	if !strings.Contains(string(signed), "s=dkim2024") {
		t.Error("DKIM-Signature missing s=dkim2024")
	}
}

func TestSignEmptyEmail(t *testing.T) {
	_, priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	_, err = Sign(priv, "example.com", "default", []byte{})
	if err == nil {
		t.Error("expected error for empty email")
	}
}

func TestSignBadKey(t *testing.T) {
	badKey := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: []byte("not-a-real-key"),
	})

	email := []byte("From: test@example.com\r\n\r\nBody")
	_, err := Sign(badKey, "example.com", "default", email)
	if err == nil {
		t.Error("expected error for bad private key")
	}
}

func TestRelaxHeader(t *testing.T) {
	tests := []struct {
		name, value, expected string
	}{
		{"From", "  sender@example.com  ", "from:sender@example.com"},
		{"Subject", "Hello   World", "subject:Hello World"},
		{"To", "  a@b.com,  c@d.com  ", "to:a@b.com, c@d.com"},
	}
	for _, tt := range tests {
		got := relaxHeader(tt.name, tt.value)
		if got != tt.expected {
			t.Errorf("relaxHeader(%q, %q) = %q, want %q", tt.name, tt.value, got, tt.expected)
		}
	}
}

func TestCompressWSP(t *testing.T) {
	tests := []struct {
		input, expected string
	}{
		{"Hello   World", "Hello World"},
		{"  leading", " leading"},
		{"trailing  ", "trailing "},
		{"nochange", "nochange"},
	}
	for _, tt := range tests {
		got := compressWSP(tt.input)
		if got != tt.expected {
			t.Errorf("compressWSP(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestCanonicalizeBodySimple(t *testing.T) {
	tests := []struct {
		input    []byte
		expected []byte
	}{
		{[]byte("Hello World"), []byte("Hello World\r\n")},
		{[]byte(""), []byte("\r\n")},
		{[]byte("Hello\r\nWorld\r\n"), []byte("Hello\r\nWorld\r\n")},
		{[]byte("Line1\r\n\r\n"), []byte("Line1\r\n")},
	}
	for _, tt := range tests {
		got := canonicalizeBodySimple(tt.input)
		if !bytes.Equal(got, tt.expected) {
			t.Errorf("canonicalizeBodySimple(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestPKCS1KeySupport(t *testing.T) {
	// Generate an RSA key and encode as PKCS1
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("Generate RSA key failed: %v", err)
	}

	privPKCS1 := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})

	email := []byte("From: test@example.com\r\n" +
		"To: user@example.com\r\n" +
		"Subject: PKCS1 Test\r\n" +
		"Date: Mon, 1 Jan 2024 00:00:00 +0000\r\n" +
		"Message-ID: <test@example.com>\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n" +
		"\r\n" +
		"Testing PKCS1 key support")

	signed, err := Sign(privPKCS1, "example.com", "pkcs1test", email)
	if err != nil {
		t.Fatalf("Sign with PKCS1 key failed: %v", err)
	}

	if !bytes.HasPrefix(signed, []byte("DKIM-Signature: ")) {
		t.Error("signed email should start with DKIM-Signature")
	}
}
