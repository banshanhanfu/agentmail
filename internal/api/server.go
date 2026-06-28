package api

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/banshanhanfu/agentmail"
	"github.com/banshanhanfu/agentmail/internal/ruleengine"
	"github.com/banshanhanfu/agentmail/internal/smtpsender"
)

// Server 是 AgentMail 的 REST API 服务
type Server struct {
	store  agentmail.Store
	engine *ruleengine.Engine
	mux    *http.ServeMux
	addr   string
}

// New 创建 API 服务
func New(addr string, store agentmail.Store, engine *ruleengine.Engine) *Server {
	s := &Server{
		store:  store,
		engine: engine,
		mux:    http.NewServeMux(),
		addr:   addr,
	}
	s.register()
	return s
}

func (s *Server) register() {
	s.mux.HandleFunc("GET /v1/identities", s.auth(s.listIdentities))
	s.mux.HandleFunc("POST /v1/identities", s.auth(s.createIdentity))
	s.mux.HandleFunc("DELETE /v1/identities/{email}", s.auth(s.deleteIdentity))

	s.mux.HandleFunc("GET /v1/mailbox/{folder}", s.auth(s.listMessages))
	s.mux.HandleFunc("GET /v1/mailbox/{folder}/{id}", s.auth(s.getMessage))
	s.mux.HandleFunc("DELETE /v1/mailbox/{folder}/{id}", s.auth(s.deleteMessage))
	s.mux.HandleFunc("PATCH /v1/mailbox/{folder}/{id}/flags", s.auth(s.updateFlags))
	s.mux.HandleFunc("POST /v1/mailbox/{folder}/{id}/move", s.auth(s.moveMessage))
	s.mux.HandleFunc("GET /v1/search", s.auth(s.searchMessages))
	s.mux.HandleFunc("GET /v1/stats", s.auth(s.stats))

	s.mux.HandleFunc("POST /v1/outbox", s.auth(s.sendMail))

	s.mux.HandleFunc("GET /v1/rules", s.auth(s.listRules))
	s.mux.HandleFunc("POST /v1/rules", s.auth(s.createRule))
	s.mux.HandleFunc("PUT /v1/rules/{id}", s.auth(s.updateRule))
	s.mux.HandleFunc("DELETE /v1/rules/{id}", s.auth(s.deleteRule))

	s.mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
}

func (s *Server) Start() error {
	log.Printf("[api] listening on %s", s.addr)
	return http.ListenAndServe(s.addr, s.mux)
}

// ── 身份 ──

func (s *Server) createIdentity(w http.ResponseWriter, r *http.Request, _ string) {
	var req struct {
		Email   string `json:"email"`
		Token   string `json:"token"`
		Name    string `json:"name"`
		Domain  string `json:"domain"`
		SignKey string `json:"sign_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "invalid_json")
		return
	}
	if req.Email == "" {
		writeErr(w, 400, "email required")
		return
	}
	if req.Token == "" {
		b := make([]byte, 16)
		rand.Read(b)
		req.Token = "am_" + hex.EncodeToString(b)
	}
	if req.Domain == "" && strings.Contains(req.Email, "@") {
		req.Domain = strings.SplitN(req.Email, "@", 2)[1]
	}
	if err := s.store.CreateIdentity(req.Email, req.Name, req.Token, req.SignKey, req.Domain); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, map[string]string{"email": req.Email, "token": req.Token})
}

func (s *Server) listIdentities(w http.ResponseWriter, r *http.Request, _ string) {
	idents, err := s.store.ListIdentities()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if idents == nil {
		idents = []agentmail.Identity{}
	}
	writeJSON(w, 200, map[string]interface{}{"identities": idents})
}

func (s *Server) deleteIdentity(w http.ResponseWriter, r *http.Request, _ string) {
	if err := s.store.DeleteIdentity(r.PathValue("email")); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ── 邮件 ──

func (s *Server) listMessages(w http.ResponseWriter, r *http.Request, identity string) {
	folder := r.PathValue("folder")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	msgs, err := s.store.ListMessages(identity, folder, limit, offset)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if msgs == nil {
		msgs = []agentmail.Message{}
	}
	writeJSON(w, 200, map[string]interface{}{"messages": msgs, "count": len(msgs)})
}

func (s *Server) getMessage(w http.ResponseWriter, r *http.Request, identity string) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	msg, err := s.store.GetMessage(id)
	if err != nil || msg.Identity != identity {
		writeErr(w, 404, "not_found")
		return
	}
	atts, _ := s.store.ListAttachments(id)
	msg.Attachments = atts
	writeJSON(w, 200, msg)
}

func (s *Server) deleteMessage(w http.ResponseWriter, r *http.Request, identity string) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	msg, err := s.store.GetMessage(id)
	if err != nil || msg.Identity != identity {
		writeErr(w, 404, "not_found")
		return
	}
	s.store.DeleteMessage(id)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) updateFlags(w http.ResponseWriter, r *http.Request, identity string) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var req struct{ Flags []string `json:"flags"` }
	json.NewDecoder(r.Body).Decode(&req)
	s.store.UpdateMessageFlags(id, req.Flags)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) moveMessage(w http.ResponseWriter, r *http.Request, identity string) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	msg, err := s.store.GetMessage(id)
	if err != nil || msg.Identity != identity {
		writeErr(w, 404, "not_found")
		return
	}
	var req struct{ Folder string `json:"folder"` }
	json.NewDecoder(r.Body).Decode(&req)
	if req.Folder != "" {
		s.store.MoveMessage(id, req.Folder)
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) searchMessages(w http.ResponseWriter, r *http.Request, identity string) {
	q := r.URL.Query().Get("q")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	msgs, err := s.store.SearchMessages(identity, q, limit, offset)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if msgs == nil {
		msgs = []agentmail.Message{}
	}
	writeJSON(w, 200, map[string]interface{}{"messages": msgs, "count": len(msgs)})
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request, identity string) {
	stats := make(map[string]int)
	for _, f := range []string{"inbox", "sent", "trash"} {
		c, _ := s.store.CountMessages(identity, f)
		stats[f] = c
	}
	writeJSON(w, 200, stats)
}

// ── 发件 ──

func (s *Server) sendMail(w http.ResponseWriter, r *http.Request, identity string) {
	var req struct {
		From     string   `json:"from"`
		To       []string `json:"to"`
		Cc       []string `json:"cc"`
		Subject  string   `json:"subject"`
		Body     string   `json:"body"`
		BodyHTML string   `json:"body_html"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "invalid_json")
		return
	}
	if len(req.To) == 0 {
		writeErr(w, 400, "to required")
		return
	}

	ident, err := s.store.GetIdentity(identity)
	if err != nil {
		writeErr(w, 400, "identity not found")
		return
	}

	mimeRaw := buildMIME(req.From, req.Subject, req.To, req.Cc, req.Body, req.BodyHTML)

	result := smtpsender.Send(r.Context(), req.From, req.To, mimeRaw, []byte(ident.SignKey), ident.Domain)

	if result.MessageID != "" {
		s.store.SaveMessage(&agentmail.Message{
			Identity:  identity,
			Folder:    "sent",
			From:      req.From,
			To:        req.To,
			Cc:        req.Cc,
			Subject:   req.Subject,
			BodyText:  req.Body,
			BodyHTML:  req.BodyHTML,
			Flags:     []string{"seen"},
			MessageID: result.MessageID,
			SentAt:    time.Now().Unix(),
			Size:      len(mimeRaw),
		})
	}
	writeJSON(w, 200, result)
}

// ── 规则 ──

func (s *Server) listRules(w http.ResponseWriter, r *http.Request, identity string) {
	rules, err := s.store.ListRules(identity)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if rules == nil {
		rules = []agentmail.Rule{}
	}
	writeJSON(w, 200, map[string]interface{}{"rules": rules})
}

func (s *Server) createRule(w http.ResponseWriter, r *http.Request, identity string) {
	var rule agentmail.Rule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		writeErr(w, 400, "invalid_json")
		return
	}
	rule.Identity = identity
	id, err := s.store.CreateRule(&rule)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, map[string]int64{"id": id})
}

func (s *Server) updateRule(w http.ResponseWriter, r *http.Request, identity string) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var rule agentmail.Rule
	json.NewDecoder(r.Body).Decode(&rule)
	rule.ID = id
	rule.Identity = identity
	if err := s.store.UpdateRule(&rule); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) deleteRule(w http.ResponseWriter, r *http.Request, identity string) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	s.store.DeleteRule(id)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ── 中间件 ──

func (s *Server) auth(next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == "" {
			writeErr(w, 401, "需要 Authorization: Bearer *** token")
			return
		}
		ident, err := s.store.GetIdentityByToken(token)
		if err != nil {
			writeErr(w, 401, "invalid token")
			return
		}
		next(w, r, ident.Email)
	}
}

// ── 工具 ──

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func buildMIME(from, subject string, to, cc []string, bodyText, bodyHTML string) []byte {
	var buf bytes.Buffer
	buf.WriteString(fmt.Sprintf("From: %s\r\n", from))
	buf.WriteString(fmt.Sprintf("To: %s\r\n", strings.Join(to, ", ")))
	if len(cc) > 0 {
		buf.WriteString(fmt.Sprintf("Cc: %s\r\n", strings.Join(cc, ", ")))
	}
	buf.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	buf.WriteString("MIME-Version: 1.0\r\n")
	domain := "localhost"
	if idx := strings.LastIndex(from, "@"); idx >= 0 {
		domain = from[idx+1:]
	}
	msgID := fmt.Sprintf("<%d.%x@%s>", time.Now().UnixNano(), time.Now().UnixNano(), domain)
	buf.WriteString(fmt.Sprintf("Message-ID: %s\r\n", msgID))
	buf.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")

	if bodyHTML != "" {
		boundary := fmt.Sprintf("=_%x", time.Now().UnixNano())
		buf.WriteString(fmt.Sprintf("Content-Type: multipart/alternative; boundary=\"%s\"\r\n", boundary))
		buf.WriteString("\r\n--" + boundary + "\r\n")
		buf.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
		buf.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
		buf.WriteString(bodyText + "\r\n")
		buf.WriteString("\r\n--" + boundary + "\r\n")
		buf.WriteString("Content-Type: text/html; charset=utf-8\r\n")
		buf.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
		buf.WriteString(bodyHTML + "\r\n")
		buf.WriteString("\r\n--" + boundary + "--\r\n")
	} else {
		buf.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
		buf.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
		buf.WriteString(bodyText + "\r\n")
	}
	return buf.Bytes()
}
