// agentmail — Agent 专用邮件引擎
//
// AgentMail 是为 AI Agent 设计的轻量邮件系统，
// 让 Agent 拥有自己的邮箱，可以收发邮件、自动处理邮件。
//
// 不是给人类用的——没有 Web 界面、没有 IMAP/POP3、
// 没有密码登录。Agent 通过 REST API + Bearer Token 操作。
package agentmail

import "time"

// Identity 表示一个邮件身份（一个 Agent 的邮箱账号）
type Identity struct {
	Email   string `json:"email"`    // agent@wsai.chat
	Token   string `json:"token"`    // API 认证 Token
	Name    string `json:"name"`     // 显示名称，如 "文殊客服"
	SignKey string `json:"sign_key"` // DKIM 私钥（PEM 格式）
	Domain  string `json:"domain"`   // 域名，如 wsai.chat
	Active  bool   `json:"active"`
	Created int64  `json:"created"`
}

// Message 表示一封邮件
type Message struct {
	ID          int64    `json:"id"`
	Identity    string   `json:"identity"`     // 所属身份邮箱
	Folder      string   `json:"folder"`       // inbox / sent / trash / drafts
	From        string   `json:"from"`         // "Name <email>"
	To          []string `json:"to"`
	Cc          []string `json:"cc"`
	Subject     string   `json:"subject"`
	BodyText    string   `json:"body_text"`
	BodyHTML    string   `json:"body_html"`
	Attachments []Attachment `json:"attachments,omitempty"`
	Flags       []string `json:"flags"`        // seen, flagged, replied, forwarded
	MessageID   string   `json:"message_id"`
	InReplyTo   string   `json:"in_reply_to"`
	References  []string `json:"references"`
	ReceivedAt  int64    `json:"received_at"`
	SentAt      int64    `json:"sent_at"`
	Size        int      `json:"size"`         // 邮件总字节数
}

// Attachment 表示一个附件
type Attachment struct {
	ID        int64  `json:"id"`
	MessageID int64  `json:"message_id"`
	Filename  string `json:"filename"`
	MimeType  string `json:"mime_type"`
	Size      int    `json:"size"`
	Path      string `json:"path"`      // 磁盘上的存储路径
	CID       string `json:"cid,omitempty"` // Content-ID，内嵌图片用
}

// Rule 表示邮件自动处理规则
type Rule struct {
	ID       int64  `json:"id"`
	Identity string `json:"identity"`   // 所属身份
	MatchFrom string `json:"match_from"` // 发件人匹配（glob）
	MatchTo   string `json:"match_to"`   // 收件人匹配（glob）
	MatchSubj string `json:"match_subj"`// 主题匹配（glob）
	Action   string `json:"action"`     // webhook / reply / forward / tag / discard
	ActionVal string `json:"action_val"` // webhook URL / 回复模板 / 转发地址 / 标签名
	Active   bool   `json:"active"`
	Priority int    `json:"priority"`   // 数字越小优先级越高
	Created  int64  `json:"created"`
}

// Outbox 发件请求（API 用）
type Outbox struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Cc      []string `json:"cc,omitempty"`
	Subject string   `json:"subject"`
	Body    string   `json:"body"`     // 纯文本
	BodyHTML string  `json:"body_html,omitempty"` // HTML（可选）
}

// SendResult 发件结果
type SendResult struct {
	MessageID string `json:"message_id"`
	Success   []string `json:"success"`   // 成功送达的地址
	Failed    []string `json:"failed"`    // 失败的地址
	Error     string   `json:"error,omitempty"`
}

// Config 程序配置
type Config struct {
	SMTPListen string `yaml:"smtp_listen"` // SMTP 监听地址，如 ":25"
	HTTPListen string `yaml:"http_listen"` // HTTP API 监听地址，如 ":8080"
	DataDir    string `yaml:"data_dir"`    // 数据目录
	Domain     string `yaml:"domain"`      // 默认域名
	LogLevel   string `yaml:"log_level"`   // debug / info / warn / error
}

// DefaultConfig 返回默认配置
func DefaultConfig() Config {
	return Config{
		SMTPListen: ":25",
		HTTPListen: ":8081",
		DataDir:    "./data",
		Domain:     "",
		LogLevel:   "info",
	}
}

// EmailParts 解析后的邮件各部分
type EmailParts struct {
	From        string
	To          []string
	Cc          []string
	Subject     string
	BodyText    string
	BodyHTML    string
	Attachments []Attachment
	MessageID   string
	InReplyTo   string
	References  []string
	ReceivedAt  time.Time
	RawBytes    []byte // 原始 SMTP 数据（用于 DKIM 签名验证）
}
