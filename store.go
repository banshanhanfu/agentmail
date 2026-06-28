package agentmail

// Store 是存储层接口，默认实现为 SQLite。
// 需要切换数据库（如 PostgreSQL）时实现此接口即可。
type Store interface {
	// ── 身份管理 ──
	CreateIdentity(email, name, token, signKey, domain string) error
	GetIdentity(email string) (*Identity, error)
	GetIdentityByToken(token string) (*Identity, error)
	ListIdentities() ([]Identity, error)
	UpdateIdentity(email, name, token, signKey string) error
	DeleteIdentity(email string) error

	// ── 邮件 ──
	SaveMessage(msg *Message) (int64, error)
	GetMessage(id int64) (*Message, error)
	ListMessages(identity, folder string, limit, offset int) ([]Message, error)
	SearchMessages(identity, query string, limit, offset int) ([]Message, error)
	CountMessages(identity, folder string) (int, error)
	UpdateMessageFlags(id int64, flags []string) error
	MoveMessage(id int64, folder string) error
	DeleteMessage(id int64) error

	// ── 附件 ──
	SaveAttachment(att *Attachment) (int64, error)
	GetAttachment(id int64) (*Attachment, error)
	ListAttachments(messageID int64) ([]Attachment, error)

	// ── 规则 ──
	CreateRule(rule *Rule) (int64, error)
	GetRule(id int64) (*Rule, error)
	ListRules(identity string) ([]Rule, error)
	UpdateRule(rule *Rule) error
	DeleteRule(id int64) error
	GetMatchingRules(identity, fromAddr, toAddr, subject string) ([]Rule, error)

	// ── 生命周期 ──
	Close() error
}
