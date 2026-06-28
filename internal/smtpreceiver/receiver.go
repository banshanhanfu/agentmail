package smtpreceiver

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/banshanhanfu/agentmail"
	"github.com/banshanhanfu/agentmail/internal/mime"
	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
)

// Server wraps an SMTP server that receives incoming emails.
type Server struct {
	srv       *smtp.Server
	ln        net.Listener
	addr      string
	store     agentmail.Store
	onMessage func(email *agentmail.EmailParts)
	wg        sync.WaitGroup
	done      chan struct{}
}

// New creates a new SMTP receiver server.
// addr is the listen address (e.g. ":25").
// store is used to persist messages and look up identities.
// onMessage is called after a complete email is received and stored.
func New(addr string, store agentmail.Store, onMessage func(email *agentmail.EmailParts)) (*Server, error) {
	if store == nil {
		return nil, fmt.Errorf("agentmail: smtpreceiver: store cannot be nil")
	}

	s := &Server{
		addr:      addr,
		store:     store,
		onMessage: onMessage,
		done:      make(chan struct{}),
	}

	backend := &Backend{
		store: store,
		onMessage: onMessage,
		server: s,
	}

	s.srv = smtp.NewServer(backend)
	s.srv.Addr = addr
	s.srv.Domain = "agentmail.local"
	s.srv.MaxMessageBytes = 50 * 1024 * 1024 // 50 MB
	s.srv.MaxRecipients = 100
	s.srv.AllowInsecureAuth = true
	s.srv.ErrorLog = log.New(os.Stderr, "smtp/receiver ", log.LstdFlags)
	s.srv.Debug = nil

	return s, nil
}

// Start begins listening for SMTP connections in a background goroutine.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("agentmail: smtpreceiver: listen on %s: %w", s.addr, err)
	}
	s.ln = ln

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := s.srv.Serve(ln); err != nil {
			select {
			case <-s.done:
				// server shutting down, ignore error
			default:
				log.Printf("agentmail: smtpreceiver: serve error: %v", err)
			}
		}
	}()

	return nil
}

// Close gracefully stops the SMTP server.
func (s *Server) Close() error {
	select {
	case <-s.done:
		return fmt.Errorf("agentmail: smtpreceiver: already closed")
	default:
		close(s.done)
	}

	// Close the listener first so Serve returns
	var err error
	if s.ln != nil {
		err = s.ln.Close()
	}
	// Also close the smtp server to clean up active connections
	if cerr := s.srv.Close(); cerr != nil && err == nil {
		err = cerr
	}
	s.wg.Wait()
	return err
}

// Backend implements the go-smtp Backend interface.
type Backend struct {
	store     agentmail.Store
	onMessage func(email *agentmail.EmailParts)
	server    *Server
}

// NewSession is called by go-smtp when a new connection establishes.
func (b *Backend) NewSession(c *smtp.Conn) (smtp.Session, error) {
	return &Session{
		store:     b.store,
		onMessage: b.onMessage,
		conn:      c,
	}, nil
}

// Session implements the go-smtp Session and AuthSession interfaces.
type Session struct {
	store     agentmail.Store
	onMessage func(email *agentmail.EmailParts)
	conn      *smtp.Conn
	from      string
	recipients []string
}

// Reset discards the currently processed message.
func (s *Session) Reset() {
	s.from = ""
	s.recipients = nil
}

// Logout frees session resources.
func (s *Session) Logout() error {
	s.Reset()
	return nil
}

// Mail sets the reverse-path (sender) for the current message.
func (s *Session) Mail(from string, opts *smtp.MailOptions) error {
	s.from = from
	return nil
}

// Rcpt adds a recipient for the current message.
func (s *Session) Rcpt(to string, opts *smtp.RcptOptions) error {
	s.recipients = append(s.recipients, to)
	return nil
}

// Data reads the full email body from the reader, parses it,
// stores it, and calls the onMessage callback.
func (s *Session) Data(r io.Reader) error {
	rawBytes, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("agentmail: smtpreceiver: read data: %w", err)
	}

	// Parse the raw email using the internal mime package
	parts, err := mime.Parse(rawBytes)
	if err != nil {
		return fmt.Errorf("agentmail: smtpreceiver: mime parse: %w", err)
	}

	parts.ReceivedAt = time.Now()
	parts.From = s.from
	parts.RawBytes = rawBytes

	// Determine which managed identity this message is for.
	// Check the recipients against identities in the store.
	identities, err := s.store.ListIdentities()
	if err != nil {
		return fmt.Errorf("agentmail: smtpreceiver: list identities: %w", err)
	}

	// Build a map of domain -> identity for fast lookup
	domainIdentity := make(map[string]*agentmail.Identity, len(identities))
	for i := range identities {
		id := &identities[i]
		if id.Active {
			domainIdentity[strings.ToLower(id.Domain)] = id
		}
	}

	// Find matching identities for recipients and save messages
	for _, recipient := range s.recipients {
		recipParts := strings.SplitN(recipient, "@", 2)
		if len(recipParts) != 2 {
			continue
		}
		domain := strings.ToLower(recipParts[1])

		id, ok := domainIdentity[domain]
		if !ok {
			// Not a managed domain — skip storing but still process
			continue
		}

		msg := &agentmail.Message{
			Identity:   id.Email,
			Folder:     "inbox",
			From:       parts.From,
			To:         parts.To,
			Cc:         parts.Cc,
			Subject:    parts.Subject,
			BodyText:   parts.BodyText,
			BodyHTML:   parts.BodyHTML,
			Attachments: parts.Attachments,
			Flags:      []string{},
			MessageID:  parts.MessageID,
			InReplyTo:  parts.InReplyTo,
			References: parts.References,
			ReceivedAt: parts.ReceivedAt.Unix(),
			SentAt:     0,
			Size:       len(rawBytes),
		}

		msgID, err := s.store.SaveMessage(msg)
		if err != nil {
			return fmt.Errorf("agentmail: smtpreceiver: save message: %w", err)
		}

		// Save attachments, linking them to the saved message
		for i := range parts.Attachments {
			parts.Attachments[i].MessageID = msgID
			_, err := s.store.SaveAttachment(&parts.Attachments[i])
			if err != nil {
				return fmt.Errorf("agentmail: smtpreceiver: save attachment: %w", err)
			}
		}
	}

	// Call the onMessage callback with the parsed email
	if s.onMessage != nil {
		s.onMessage(parts)
	}

	// Evaluate rules for auto-reply, forward, webhook
	s.evaluateRules(parts, domainIdentity)

	return nil
}

// evaluateRules checks for matching auto-processing rules and executes them.
func (s *Session) evaluateRules(parts *agentmail.EmailParts, domainIdentity map[string]*agentmail.Identity) {
	for _, recipient := range s.recipients {
		recipParts := strings.SplitN(recipient, "@", 2)
		if len(recipParts) != 2 {
			continue
		}
		domain := strings.ToLower(recipParts[1])

		id, ok := domainIdentity[domain]
		if !ok {
			continue
		}

		// Get matching rules for this identity
		rules, err := s.store.GetMatchingRules(id.Email, parts.From, recipient, parts.Subject)
		if err != nil {
			log.Printf("agentmail: smtpreceiver: get matching rules: %v", err)
			continue
		}

		for _, rule := range rules {
			if !rule.Active {
				continue
			}
			switch rule.Action {
			case "webhook":
				// Webhooks are handled asynchronously by the server layer
				log.Printf("agentmail: smtpreceiver: webhook rule matched for %s -> %s", id.Email, rule.ActionVal)
			case "reply":
				log.Printf("agentmail: smtpreceiver: auto-reply rule matched for %s", id.Email)
			case "forward":
				log.Printf("agentmail: smtpreceiver: forward rule matched for %s -> %s", id.Email, rule.ActionVal)
			case "tag":
				// Tagging would be handled at a higher layer
			case "discard":
				log.Printf("agentmail: smtpreceiver: discard rule matched for %s", id.Email)
			}
		}
	}
}

// AuthMechanisms returns the supported SASL authentication mechanisms.
func (s *Session) AuthMechanisms() []string {
	return []string{"PLAIN", "LOGIN"}
}

// Auth returns a SASL server for the given mechanism.
func (s *Session) Auth(mech string) (sasl.Server, error) {
	switch strings.ToUpper(mech) {
	case "PLAIN":
		return sasl.NewPlainServer(func(identity, username, password string) error {
			return s.authenticate(username, password)
		}), nil
	case "LOGIN":
		return &loginServer{authenticate: func(username, password string) error {
			return s.authenticate(username, password)
		}}, nil
	default:
		return nil, smtp.ErrAuthUnknownMechanism
	}
}

// authenticate verifies credentials against the store.
func (s *Session) authenticate(username, password string) error {
	// username is the email address, password is the token
	id, err := s.store.GetIdentityByToken(password)
	if err != nil {
		return smtp.ErrAuthFailed
	}
	if id == nil || !id.Active {
		return smtp.ErrAuthFailed
	}
	// Verify the username matches the identity's email
	if !strings.EqualFold(id.Email, username) {
		return smtp.ErrAuthFailed
	}
	return nil
}

// loginServer implements the SASL LOGIN mechanism server side.
type loginServer struct {
	authenticate func(username, password string) error
	state        int // 0=initial, 1=got username, 2=done
	username     string
}

func (l *loginServer) Next(response []byte) (challenge []byte, done bool, err error) {
	switch l.state {
	case 0:
		// Initial state: send username prompt
		if response != nil {
			// Client sent initial response (username)
			l.username = string(response)
			l.state = 1
			// Send password prompt
			return []byte("Password:"), false, nil
		}
		l.state = 1
		return []byte("Username:"), false, nil
	case 1:
		// We have username, expect password
		if l.username == "" {
			l.username = string(response)
			return []byte("Password:"), false, nil
		}
		password := string(response)
		err := l.authenticate(l.username, password)
		if err != nil {
			return nil, true, err
		}
		l.state = 2
		return nil, true, nil
	default:
		return nil, true, sasl.ErrUnexpectedClientResponse
	}
}

// Ensure Backend implements smtp.Backend
var _ smtp.Backend = (*Backend)(nil)

// Ensure Session implements smtp.AuthSession
var _ smtp.AuthSession = (*Session)(nil)
