// Package dkim provides DKIM signing and key generation.
package dkim

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"strings"
	"time"
)

// Sign signs a raw email with a DKIM-Signature header per RFC 6376.
// It uses relaxed/simple canonicalization and rsasha256.
// The signed email with the DKIM-Signature header inserted is returned.
func Sign(pemPrivateKey []byte, domain, selector string, rawEmail []byte) ([]byte, error) {
	if len(rawEmail) == 0 {
		return nil, fmt.Errorf("dkim: empty email")
	}
	if domain == "" {
		return nil, fmt.Errorf("dkim: domain is required")
	}
	if selector == "" {
		selector = "default"
	}

	// Parse PEM private key
	block, _ := pem.Decode(pemPrivateKey)
	if block == nil {
		return nil, fmt.Errorf("dkim: no PEM block found in private key")
	}

	var privKey *rsa.PrivateKey

	switch block.Type {
	case "RSA PRIVATE KEY", "PRIVATE KEY":
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			// Try PKCS1
			key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("dkim: failed to parse private key: %w", err)
			}
		}
		var ok bool
		privKey, ok = key.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("dkim: key is not RSA (got %T)", key)
		}
	default:
		return nil, fmt.Errorf("dkim: unsupported PEM type: %s", block.Type)
	}

	// Split raw email into headers and body
	parts := bytes.SplitN(rawEmail, []byte("\r\n\r\n"), 2)
	var headers []byte
	var body []byte
	if len(parts) == 2 {
		headers = parts[0]
		body = parts[1]
	} else {
		headers = rawEmail
		body = []byte{}
	}

	// Canonicalize body (simple: no changes)
	// But we need to ensure body ends with \r\n for hashing
	bodyCanon := canonicalizeBodySimple(body)

	// Hash body
	bodyHash := sha256.Sum256(bodyCanon)
	bodyHashBase64 := base64.StdEncoding.EncodeToString(bodyHash[:])

	// Build the DKIM-Signature header fields (without signature value)
	// Generate a timestamp
	sigTimestamp := time.Now().Unix()
	// Generate a random tag for bh
	// Note: we need to use canonicalized header format for signing
	// relaxed: fold whitespace, lowercase field names

	// Headers to sign (from the email, we sign From, Date, Subject, Message-ID, To, Cc)
	signedHeaders := []string{"From", "Date", "Subject", "Message-ID", "To", "Cc", "MIME-Version", "Content-Type"}

	// Build the signature header value (without the sig)
	headerNames := strings.Join(signedHeaders, ":")

	// DKIM header template without signature
	dkimHeaderNoSig := fmt.Sprintf("DKIM-Signature: v=1; a=rsa-sha256; c=relaxed/simple; d=%s; s=%s; t=%d; bh=%s; h=%s; b=",
		domain, selector, sigTimestamp, bodyHashBase64, headerNames)

	// Canonicalize the existing headers using relaxed algorithm
	// For signing, we need to include the DKIM-Signature header itself (with b= empty)
	// But since we're inserting it at the top, we sign all existing headers + the DKIM-Signature (without b=)

	// Get the canonicalized existing headers
	existingHeadersCanon := canonicalizeHeadersRelaxed(headers, signedHeaders)

	// Canonicalize the DKIM-Signature header (without b= value)
	dkimHeaderCanon := "dkim-signature:" + dkimHeaderNoSig[len("DKIM-Signature:")+1:] + "\r\n"

	// Combine: existing headers + DKIM-Signature header (canonicalized, without b=)
	signHeaders := existingHeadersCanon + dkimHeaderCanon

	// Hash the canonicalized headers
	headerHash := sha256.Sum256([]byte(signHeaders))

	// Sign the header hash
	signature, err := rsa.SignPKCS1v15(rand.Reader, privKey, crypto.SHA256, headerHash[:])
	if err != nil {
		return nil, fmt.Errorf("dkim: signing failed: %w", err)
	}

	sigBase64 := base64.StdEncoding.EncodeToString(signature)

	// Build the final DKIM-Signature header
	finalDKIMHeader := dkimHeaderNoSig + sigBase64 + "\r\n"

	// Insert the DKIM-Signature header at the top of the email
	result := make([]byte, 0, len(finalDKIMHeader)+len(rawEmail))
	result = append(result, []byte(finalDKIMHeader)...)
	result = append(result, rawEmail...)

	return result, nil
}

// canonicalizeBodySimple performs simple body canonicalization per RFC 6376 §3.4.3.
// Simple: no changes to the body content, but ensure it ends with a single CRLF.
func canonicalizeBodySimple(body []byte) []byte {
	// Remove trailing empty lines (CRLF sequences)
	body = bytes.TrimRight(body, "\r\n")

	// Append a single CRLF
	body = append(body, '\r', '\n')

	return body
}

// canonicalizeHeadersRelaxed performs relaxed header canonicalization per RFC 6376 §3.4.2.
// - Unfold header continuation lines
// - Convert field name to lowercase
// - Reduce WSP sequences to single space before the colon
// - Delete all WSP at end of header value
// - Delete any WSP at beginning of the value
// - Compress any WSP in the value to a single space
func canonicalizeHeadersRelaxed(headers []byte, signThese []string) string {
	// Build a set of headers to sign
	signSet := make(map[string]bool)
	for _, h := range signThese {
		signSet[strings.ToLower(h)] = true
	}

	// Split headers into individual header lines
	lines := bytes.Split(headers, []byte("\r\n"))

	var result strings.Builder

	// First pass: unfold and collect header blocks
	var headerBlocks [][][]byte // each block is a header (first line + continuation lines)
	var currentBlock [][]byte

	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			// Continuation line
			currentBlock = append(currentBlock, line)
		} else {
			if currentBlock != nil {
				headerBlocks = append(headerBlocks, currentBlock)
			}
			currentBlock = [][]byte{line}
		}
	}
	if currentBlock != nil {
		headerBlocks = append(headerBlocks, currentBlock)
	}

	// Process each header block
	for _, block := range headerBlocks {
		// Unfold: join all lines, replacing CRLF+WSP with nothing (just concatenate)
		unfolded := block[0]
		for i := 1; i < len(block); i++ {
			unfolded = append(unfolded, block[i]...)
		}

		// Find the colon separating field name from value
		colonIdx := bytes.IndexByte(unfolded, ':')
		if colonIdx == -1 {
			continue
		}

		fieldName := string(unfolded[:colonIdx])
		fieldValue := string(unfolded[colonIdx+1:])

		// Check if this header should be signed
		if !signSet[strings.ToLower(strings.TrimSpace(fieldName))] {
			continue
		}

		// Relaxed canonicalization of the header
		canon := relaxHeader(fieldName, fieldValue)
		result.WriteString(canon)
		result.WriteString("\r\n")
	}

	return result.String()
}

// relaxHeader performs relaxed canonicalization on a single header.
func relaxHeader(name, value string) string {
	// Lowercase field name, trim WSP around it
	name = strings.ToLower(strings.TrimSpace(name))

	// Delete WSP before colon (already done by TrimSpace)
	// Delete leading WSP after colon
	value = strings.TrimLeft(value, " \t")

	// Delete trailing WSP
	value = strings.TrimRight(value, " \t")

	// Compress WSP sequences in the value to single space
	value = compressWSP(value)

	return name + ":" + value
}

// compressWSP compresses whitespace sequences to a single space.
func compressWSP(s string) string {
	var result strings.Builder
	inSpace := false
	for _, r := range s {
		if r == ' ' || r == '\t' {
			if !inSpace {
				result.WriteByte(' ')
				inSpace = true
			}
		} else {
			result.WriteRune(r)
			inSpace = false
		}
	}
	return result.String()
}

// GenerateKeyPair generates a new RSA 2048-bit key pair for DKIM signing.
// Returns PEM-encoded public and private keys.
func GenerateKeyPair() (pub, priv []byte, err error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, fmt.Errorf("dkim: generating RSA key: %w", err)
	}

	// Encode private key as PKCS8 PEM
	privBytes, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("dkim: marshaling private key: %w", err)
	}
	priv = pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privBytes,
	})

	// Encode public key as PKIX PEM
	pubBytes, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		return nil, nil, fmt.Errorf("dkim: marshaling public key: %w", err)
	}
	pub = pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubBytes,
	})

	return pub, priv, nil
}
