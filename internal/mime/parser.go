// Package mime parses RFC 5322 emails and builds MIME messages.
package mime

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/textproto"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/banshanhanfu/agentmail"
)

// rfc2047Regex matches encoded words like =?UTF-8?B?...?= or =?UTF-8?Q?...?=
var rfc2047Regex = regexp.MustCompile(`=\?([^?]+)\?([BbQq])\?([^?]*)\?=`)

// Parse parses raw RFC 5322 email bytes into EmailParts.
func Parse(raw []byte) (*agentmail.EmailParts, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("mime: empty email bytes")
	}

	// Split headers and body at the first blank line
	var bodyStart int
	// Look for \r\n\r\n first (SMTP standard)
	sep := bytes.Index(raw, []byte("\r\n\r\n"))
	if sep >= 0 {
		bodyStart = sep + 4
	} else {
		// Try \n\n (unix-style)
		sep = bytes.Index(raw, []byte("\n\n"))
		if sep >= 0 {
			bodyStart = sep + 2
		} else {
			return nil, fmt.Errorf("mime: no header/body separator found")
		}
	}

	// textproto.ReadMIMEHeader needs the blank line terminator
	headerBytes := raw[:bodyStart]

	tp := textproto.NewReader(bufio.NewReader(bytes.NewReader(headerBytes)))
	hdrs, err := tp.ReadMIMEHeader()
	if err != nil {
		return nil, fmt.Errorf("mime: reading headers: %w", err)
	}

	body := raw[bodyStart:]
	parts := &agentmail.EmailParts{
		ReceivedAt: time.Now(),
	}

	// Extract simple headers
	parts.From = decodeRFC2047(hdrs.Get("From"))
	parts.Subject = decodeRFC2047(hdrs.Get("Subject"))
	parts.MessageID = strings.TrimSpace(hdrs.Get("Message-ID"))
	parts.InReplyTo = strings.TrimSpace(hdrs.Get("In-Reply-To"))

	// Parse To
	if to := hdrs.Get("To"); to != "" {
		parts.To = parseAddressList(decodeRFC2047(to))
	}

	// Parse Cc
	if cc := hdrs.Get("Cc"); cc != "" {
		parts.Cc = parseAddressList(decodeRFC2047(cc))
	}

	// Parse References
	if refs := hdrs.Get("References"); refs != "" {
		for _, r := range strings.Fields(refs) {
			r = strings.TrimSpace(r)
			if r != "" {
				parts.References = append(parts.References, r)
			}
		}
	}

	// Store raw bytes
	parts.RawBytes = raw

	// Parse content type
	contentType := hdrs.Get("Content-Type")
	if contentType == "" {
		// No content type, treat body as plain text
		parts.BodyText = decodeBody(body, hdrs.Get("Content-Transfer-Encoding"))
		return parts, nil
	}

	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		// Fall back: treat as plain text
		parts.BodyText = decodeBody(body, hdrs.Get("Content-Transfer-Encoding"))
		return parts, nil
	}

	switch {
	case strings.HasPrefix(mediaType, "multipart/alternative"):
		err = parseMultipartAlternative(body, params["boundary"], parts, hdrs)
	case strings.HasPrefix(mediaType, "multipart/mixed"):
		err = parseMultipartMixed(body, params["boundary"], parts, hdrs)
	case strings.HasPrefix(mediaType, "multipart/related"):
		err = parseMultipartRelated(body, params["boundary"], parts, hdrs)
	case strings.HasPrefix(mediaType, "text/plain"):
		parts.BodyText = decodeBody(body, hdrs.Get("Content-Transfer-Encoding"))
	case strings.HasPrefix(mediaType, "text/html"):
		parts.BodyHTML = decodeBody(body, hdrs.Get("Content-Transfer-Encoding"))
	default:
		// Treat as plain text
		parts.BodyText = decodeBody(body, hdrs.Get("Content-Transfer-Encoding"))
	}

	if err != nil {
		return parts, fmt.Errorf("mime: parsing multipart: %w", err)
	}

	return parts, nil
}

// parseMultipartAlternative handles multipart/alternative (text/plain + text/html).
func parseMultipartAlternative(body []byte, boundary string, parts *agentmail.EmailParts, _ textproto.MIMEHeader) error {
	if boundary == "" {
		return fmt.Errorf("no boundary in multipart/alternative")
	}

	mr := multipart.NewReader(bytes.NewReader(body), boundary)
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		content, err := io.ReadAll(part)
		if err != nil {
			continue
		}

		ct := part.Header.Get("Content-Type")
		encoding := part.Header.Get("Content-Transfer-Encoding")

		if strings.Contains(ct, "text/plain") {
			parts.BodyText = decodeBody(content, encoding)
		} else if strings.Contains(ct, "text/html") {
			parts.BodyHTML = decodeBody(content, encoding)
		} else if strings.HasPrefix(ct, "multipart/") {
			// Nested multipart — recurse
			_, params, _ := mime.ParseMediaType(ct)
			if strings.HasPrefix(ct, "multipart/alternative") {
				_ = parseMultipartAlternative(content, params["boundary"], parts, nil)
			} else if strings.HasPrefix(ct, "multipart/related") {
				_ = parseMultipartRelated(content, params["boundary"], parts, nil)
			} else {
				_ = parseMultipartMixed(content, params["boundary"], parts, nil)
			}
		}
	}

	return nil
}

// parseMultipartMixed handles multipart/mixed (attachments).
func parseMultipartMixed(body []byte, boundary string, parts *agentmail.EmailParts, hdrs textproto.MIMEHeader) error {
	if boundary == "" {
		return fmt.Errorf("no boundary in multipart/mixed")
	}

	mr := multipart.NewReader(bytes.NewReader(body), boundary)
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		content, err := io.ReadAll(part)
		if err != nil {
			continue
		}

		ct := part.Header.Get("Content-Type")
		encoding := part.Header.Get("Content-Transfer-Encoding")
		cd := part.Header.Get("Content-Disposition")

		if strings.HasPrefix(ct, "multipart/alternative") {
			_, params, _ := mime.ParseMediaType(ct)
			_ = parseMultipartAlternative(content, params["boundary"], parts, nil)
		} else if strings.HasPrefix(ct, "multipart/related") {
			_, params, _ := mime.ParseMediaType(ct)
			_ = parseMultipartRelated(content, params["boundary"], parts, nil)
		} else if strings.HasPrefix(ct, "multipart/mixed") {
			_, params, _ := mime.ParseMediaType(ct)
			_ = parseMultipartMixed(content, params["boundary"], parts, nil)
		} else if strings.Contains(cd, "attachment") || strings.Contains(cd, "inline") {
			att := agentmail.Attachment{}
			_, params, _ := mime.ParseMediaType(cd)
			att.Filename = params["filename"]
			if att.Filename == "" {
				_, params, _ := mime.ParseMediaType(ct)
				att.Filename = params["name"]
			}
			if att.Filename == "" {
				att.Filename = "unnamed"
			}
			att.MimeType = ct
			if idx := strings.Index(att.MimeType, ";"); idx != -1 {
				att.MimeType = strings.TrimSpace(att.MimeType[:idx])
			}
			att.Size = len(content)
			att.CID = strings.Trim(part.Header.Get("Content-ID"), "<>")

			parts.Attachments = append(parts.Attachments, att)
		} else if strings.HasPrefix(ct, "text/plain") && parts.BodyText == "" {
			parts.BodyText = decodeBody(content, encoding)
		} else if strings.HasPrefix(ct, "text/html") && parts.BodyHTML == "" {
			parts.BodyHTML = decodeBody(content, encoding)
		} else {
			// Could be an attachment without Content-Disposition
			if !strings.HasPrefix(ct, "text/") {
				att := agentmail.Attachment{}
				_, params, _ := mime.ParseMediaType(ct)
				att.Filename = params["name"]
				if att.Filename == "" {
					att.Filename = "unnamed"
				}
				att.MimeType = ct
				if idx := strings.Index(att.MimeType, ";"); idx != -1 {
					att.MimeType = strings.TrimSpace(att.MimeType[:idx])
				}
				att.Size = len(content)
				att.CID = strings.Trim(part.Header.Get("Content-ID"), "<>")
				parts.Attachments = append(parts.Attachments, att)
			} else if parts.BodyText == "" && strings.HasPrefix(ct, "text/plain") {
				parts.BodyText = decodeBody(content, encoding)
			}
		}
	}

	return nil
}

// parseMultipartRelated handles multipart/related (HTML with inline images).
func parseMultipartRelated(body []byte, boundary string, parts *agentmail.EmailParts, hdrs textproto.MIMEHeader) error {
	if boundary == "" {
		return fmt.Errorf("no boundary in multipart/related")
	}

	mr := multipart.NewReader(bytes.NewReader(body), boundary)
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		content, err := io.ReadAll(part)
		if err != nil {
			continue
		}

		ct := part.Header.Get("Content-Type")
		encoding := part.Header.Get("Content-Transfer-Encoding")
		cd := part.Header.Get("Content-Disposition")

		if strings.HasPrefix(ct, "multipart/alternative") {
			_, params, _ := mime.ParseMediaType(ct)
			_ = parseMultipartAlternative(content, params["boundary"], parts, nil)
		} else if strings.Contains(cd, "inline") || strings.Contains(ct, "image/") {
			att := agentmail.Attachment{}
			if strings.Contains(cd, "inline") {
				_, params, _ := mime.ParseMediaType(cd)
				att.Filename = params["filename"]
			}
			if att.Filename == "" {
				_, params, _ := mime.ParseMediaType(ct)
				att.Filename = params["name"]
			}
			if att.Filename == "" {
				att.Filename = "unnamed"
			}
			att.MimeType = ct
			if idx := strings.Index(att.MimeType, ";"); idx != -1 {
				att.MimeType = strings.TrimSpace(att.MimeType[:idx])
			}
			att.Size = len(content)
			att.CID = strings.Trim(part.Header.Get("Content-ID"), "<>")
			parts.Attachments = append(parts.Attachments, att)
		} else if strings.HasPrefix(ct, "text/html") {
			parts.BodyHTML = decodeBody(content, encoding)
		} else if strings.HasPrefix(ct, "text/plain") && parts.BodyText == "" {
			parts.BodyText = decodeBody(content, encoding)
		}
	}

	return nil
}

// decodeBody decodes a body using the given Content-Transfer-Encoding.
func decodeBody(data []byte, encoding string) string {
	enc := strings.ToLower(strings.TrimSpace(encoding))

	switch enc {
	case "base64":
		decoded, err := base64.StdEncoding.DecodeString(string(bytes.TrimSpace(data)))
		if err != nil {
			// Try without padding
			decoded, err = base64.RawStdEncoding.DecodeString(string(bytes.TrimSpace(data)))
			if err != nil {
				return string(data)
			}
		}
		return string(decoded)
	case "quoted-printable":
		decoded, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(data)))
		if err != nil {
			return string(data)
		}
		return string(decoded)
	case "7bit", "8bit", "binary", "":
		return string(data)
	default:
		return string(data)
	}
}

// decodeRFC2047 decodes RFC 2047 encoded words like =?UTF-8?B?...?= or =?UTF-8?Q?...?=.
// Falls back to charset detection for non-RFC2047 raw bytes.
func decodeRFC2047(s string) string {
	if s == "" {
		return ""
	}

	// Use the stdlib mime.WordDecoder for proper handling
	dec := new(mime.WordDecoder)
	decoded, err := dec.DecodeHeader(s)
	if err != nil {
		// Fall back to regex-based decoding
		decoded = rfc2047DecodeFallback(s)
	}

	// If still has non-UTF-8 sequences, try GBK/GB2312 conversion
	if !utf8.ValidString(decoded) {
		if converted := tryDecodeNonUTF8(decoded); converted != "" {
			return converted
		}
		// Last resort: replace invalid sequences
		return strings.ToValidUTF8(decoded, "�")
	}
	return decoded
}

// rfc2047DecodeFallback is a manual fallback for RFC 2047 decoding.
func rfc2047DecodeFallback(s string) string {
	return rfc2047Regex.ReplaceAllStringFunc(s, func(match string) string {
		parts := rfc2047Regex.FindStringSubmatch(match)
		if len(parts) < 4 {
			return match
		}
		charset := parts[1]
		encoding := strings.ToUpper(parts[2])
		encodedText := parts[3]

		var decoded []byte
		switch encoding {
		case "B":
			var err error
			decoded, err = base64.StdEncoding.DecodeString(encodedText)
			if err != nil {
				decoded, err = base64.RawStdEncoding.DecodeString(encodedText)
				if err != nil {
					return match
				}
			}
		case "Q":
			decoded = decodeQEncoding(encodedText)
		default:
			return match
		}

		// Try UTF-8 first, then fallback
		if strings.EqualFold(charset, "utf-8") || strings.EqualFold(charset, "utf8") {
			return string(decoded)
		}

		// For other charsets, return as-is if we can't convert
		return string(decoded)
	})
}

// decodeQEncoding decodes RFC 2047 Q-encoding (like quoted-printable).
func decodeQEncoding(s string) []byte {
	var result []byte
	for i := 0; i < len(s); {
		if s[i] == '_' {
			result = append(result, ' ')
			i++
		} else if s[i] == '=' && i+2 < len(s) {
			hex := s[i+1 : i+3]
			b, err := strconv.ParseUint(hex, 16, 8)
			if err != nil {
				result = append(result, s[i])
				i++
			} else {
				result = append(result, byte(b))
				i += 3
			}
		} else {
			result = append(result, s[i])
			i++
		}
	}
	return result
}

// parseAddressList parses a comma-separated list of email addresses.
// It handles the "Name <email>" format.
func parseAddressList(s string) []string {
	if s == "" {
		return nil
	}

	var result []string
	// Split by commas, respecting quoted strings
	current := strings.Builder{}
	inQuote := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == '"' {
			inQuote = !inQuote
			current.WriteByte(ch)
		} else if ch == ',' && !inQuote {
			addr := strings.TrimSpace(current.String())
			if addr != "" {
				result = append(result, addr)
			}
			current.Reset()
		} else {
			current.WriteByte(ch)
		}
	}
	addr := strings.TrimSpace(current.String())
	if addr != "" {
		result = append(result, addr)
	}

	return result
}

// BuildRaw builds a raw MIME email from the given parts.
func BuildRaw(from, subject string, to, cc []string, bodyText, bodyHTML string) ([]byte, error) {
	var buf bytes.Buffer

	// Build headers
	messageID := GenerateMessageID(extractDomain(from))
	buf.WriteString("From: " + from + "\r\n")
	buf.WriteString("To: " + strings.Join(to, ", ") + "\r\n")
	if len(cc) > 0 {
		buf.WriteString("Cc: " + strings.Join(cc, ", ") + "\r\n")
	}
	buf.WriteString("Subject: " + encodeSubject(subject) + "\r\n")
	buf.WriteString("Message-ID: " + messageID + "\r\n")
	buf.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	buf.WriteString("MIME-Version: 1.0\r\n")

	if bodyHTML != "" && bodyText != "" {
		// multipart/alternative with both text and HTML
		boundary := fmt.Sprintf("=_%x_%x", time.Now().UnixNano(), randUint64())
		buf.WriteString("Content-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n")
		buf.WriteString("\r\n")

		// Text part
		buf.WriteString("--" + boundary + "\r\n")
		buf.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
		buf.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
		buf.WriteString("\r\n")
		buf.Write(qpEncode(bodyText))
		buf.WriteString("\r\n")

		// HTML part
		buf.WriteString("--" + boundary + "\r\n")
		buf.WriteString("Content-Type: text/html; charset=\"UTF-8\"\r\n")
		buf.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
		buf.WriteString("\r\n")
		buf.Write(qpEncode(bodyHTML))
		buf.WriteString("\r\n")

		buf.WriteString("--" + boundary + "--\r\n")
	} else if bodyHTML != "" {
		// HTML only
		buf.WriteString("Content-Type: text/html; charset=\"UTF-8\"\r\n")
		buf.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
		buf.WriteString("\r\n")
		buf.Write(qpEncode(bodyHTML))
		buf.WriteString("\r\n")
	} else {
		// Plain text only
		buf.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
		buf.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
		buf.WriteString("\r\n")
		buf.Write(qpEncode(bodyText))
		buf.WriteString("\r\n")
	}

	return buf.Bytes(), nil
}

// encodeSubject encodes a subject line with RFC 2047 if needed.
func encodeSubject(subject string) string {
	if needsEncoding(subject) {
		return "=?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte(subject)) + "?="
	}
	return subject
}

// needsEncoding checks if a string contains non-ASCII characters.
func needsEncoding(s string) bool {
	for _, r := range s {
		if r > 127 {
			return true
		}
	}
	return false
}

// qpEncode encodes data using quoted-printable.
func qpEncode(data string) []byte {
	var buf bytes.Buffer
	w := quotedprintable.NewWriter(&buf)
	_, _ = w.Write([]byte(data))
	w.Close()
	return buf.Bytes()
}

// GenerateMessageID generates a unique Message-ID header value.
func GenerateMessageID(domain string) string {
	if domain == "" {
		domain = "localhost"
	}
	now := time.Now().UnixNano()
	var r [4]byte
	rand.Read(r[:])
	random := uint32(r[0])<<24 | uint32(r[1])<<16 | uint32(r[2])<<8 | uint32(r[3])
	return fmt.Sprintf("<%d.%x@%s>", now, random, domain)
}

// randUint64 returns a random uint64 for boundary generation.
func randUint64() uint64 {
	var b [8]byte
	rand.Read(b[:])
	return uint64(b[0])<<56 | uint64(b[1])<<48 | uint64(b[2])<<40 | uint64(b[3])<<32 |
		uint64(b[4])<<24 | uint64(b[5])<<16 | uint64(b[6])<<8 | uint64(b[7])
}

// extractDomain extracts the domain part from an email address.
func extractDomain(from string) string {
	idx := strings.LastIndex(from, "@")
	if idx == -1 {
		idx = strings.LastIndex(from, "<")
		if idx != -1 && strings.HasSuffix(from, ">") {
			inner := from[idx+1 : len(from)-1]
			at := strings.LastIndex(inner, "@")
			if at != -1 {
				return inner[at+1:]
			}
		}
		return "localhost"
	}
	// Handle "Name <email@domain>" format
	// Find the @ after the last <
	angleIdx := strings.LastIndex(from, "<")
	if angleIdx != -1 && angleIdx < idx {
		// The @ is inside angle brackets
		return from[idx+1:]
	}
	domain := from[idx+1:]
	// Remove trailing >
	if strings.HasSuffix(domain, ">") {
		domain = domain[:len(domain)-1]
	}
	return strings.TrimSpace(domain)
}
