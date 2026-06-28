package smtpsender

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/banshanhanfu/agentmail/internal/dkim"
)

// SendResult 发件结果
type SendResult struct {
	MessageID string   `json:"message_id"`
	Success   []string `json:"success"`
	Failed    []string `json:"failed"`
	Error     string   `json:"error,omitempty"`
}

// Send 通过 MX 查询 + 直连 SMTP 发送邮件。
// rawEmail 是完整的 RFC 5322 邮件内容（含 Headers + Body）。
// 如果 signKey 不为空，发送前会对邮件进行 DKIM 签名。
func Send(ctx context.Context, from string, to []string, rawEmail []byte, signKey []byte, domain string) *SendResult {
	// DKIM 签名
	emailData := rawEmail
	if len(signKey) > 0 && domain != "" {
		signed, err := dkim.Sign(signKey, domain, "default", rawEmail)
		if err != nil {
			log.Printf("[smtp] DKIM sign failed (non-fatal): %v", err)
		} else {
			emailData = signed
		}
	}

	res := &SendResult{
		MessageID: extractMessageID(rawEmail),
		Success:   []string{},
		Failed:    []string{},
	}

	// 按域名分组收件人
	domainGroups := groupByDomain(to)

	for domain, recipients := range domainGroups {
		addr, err := lookupMX(ctx, domain)
		if err != nil {
			log.Printf("[smtp] MX lookup failed for %s: %v", domain, err)
			res.Failed = append(res.Failed, recipients...)
			res.Error = err.Error()
			continue
		}

		client, err := dialSMTP(ctx, addr, from, domain)
		if err != nil {
			log.Printf("[smtp] dial %s failed: %v", addr, err)
			// Fallback: try A/AAAA record
			addrs, err2 := net.DefaultResolver.LookupHost(ctx, domain)
			if err2 != nil || len(addrs) == 0 {
				res.Failed = append(res.Failed, recipients...)
				res.Error = err.Error()
				continue
			}
			addr2 := fmt.Sprintf("%s:25", addrs[0])
			client, err = dialSMTP(ctx, addr2, from, domain)
			if err != nil {
				log.Printf("[smtp] fallback dial %s failed: %v", addr2, err)
				res.Failed = append(res.Failed, recipients...)
				res.Error = err.Error()
				continue
			}
		}

				// 发送
				if err := sendToClient(client, from, recipients, emailData); err != nil {
			log.Printf("[smtp] send to %s failed: %v", addr, err)
			res.Failed = append(res.Failed, recipients...)
			res.Error = err.Error()
			client.Close()
			continue
		}
		client.Quit()
		client.Close()
		res.Success = append(res.Success, recipients...)
	}

	return res
}

// groupByDomain 将收件人按 @ 后面的域名分组
func groupByDomain(recipients []string) map[string][]string {
	groups := make(map[string][]string)
	for _, r := range recipients {
		r = strings.TrimSpace(r)
		if !strings.Contains(r, "@") {
			continue
		}
		parts := strings.SplitN(r, "@", 2)
		domain := strings.ToLower(parts[1])
		groups[domain] = append(groups[domain], r)
	}
	return groups
}

// lookupMX 查询域名的 MX 记录，返回 MX host:25
func lookupMX(ctx context.Context, domain string) (string, error) {
	resolver := &net.Resolver{}
	mxs, err := resolver.LookupMX(ctx, domain)
	if err != nil {
		return "", fmt.Errorf("MX lookup: %w", err)
	}
	if len(mxs) == 0 {
		return "", fmt.Errorf("no MX records for %s", domain)
	}
	// 使用优先级最高的 MX
	host := strings.TrimSuffix(mxs[0].Host, ".")
	return fmt.Sprintf("%s:25", host), nil
}

// dialSMTP 连接到 SMTP 服务器，尝试 STARTTLS
func dialSMTP(ctx context.Context, addr, from, domain string) (*smtp.Client, error) {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}

	client, err := smtp.NewClient(conn, addr)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("smtp client: %w", err)
	}

	// EHLO
	if err := client.Hello(getHostname()); err != nil {
		client.Close()
		return nil, fmt.Errorf("helo: %w", err)
	}

	// STARTTLS
	if ok, _ := client.Extension("STARTTLS"); ok {
		tlsConfig := &tls.Config{
			ServerName:         extractHost(addr),
			InsecureSkipVerify: false,
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			log.Printf("[smtp] STARTTLS failed (non-fatal): %v", err)
		}
	}

	return client, nil
}

// sendToClient 通过已连接的 SMTP Client 发送邮件
func sendToClient(client *smtp.Client, from string, to []string, raw []byte) error {
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("mail from: %w", err)
	}
	for _, rcpt := range to {
		if err := client.Rcpt(rcpt); err != nil {
			log.Printf("[smtp] rcpt %s failed: %v", rcpt, err)
			continue
		}
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("data: %w", err)
	}
	if _, err := w.Write(raw); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return w.Close()
}

func extractMessageID(raw []byte) string {
	prefix := []byte("Message-ID: ")
	idx := indexOf(raw, prefix)
	if idx < 0 {
		prefixLower := []byte("message-id: ")
		idx = indexOf(raw, prefixLower)
	}
	if idx < 0 {
		return fmt.Sprintf("<%d@localhost>", time.Now().UnixNano())
	}
	start := idx + len(prefix)
	end := start
	for end < len(raw) && raw[end] != '\r' && raw[end] != '\n' {
		end++
	}
	return strings.TrimSpace(string(raw[start:end]))
}

func indexOf(data, pattern []byte) int {
	for i := 0; i <= len(data)-len(pattern); i++ {
		match := true
		for j := 0; j < len(pattern); j++ {
			if data[i+j] != pattern[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func extractHost(addr string) string {
	if idx := strings.Index(addr, ":"); idx >= 0 {
		return addr[:idx]
	}
	return addr
}

func getHostname() string {
	host, err := net.LookupAddr("127.0.0.1")
	if err != nil || len(host) == 0 {
		return "agentmail.local"
	}
	return strings.TrimSuffix(host[0], ".")
}

var _ = log.Println
