package ruleengine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/banshanhanfu/agentmail"
)

// Engine 规则引擎，处理邮件自动匹配和执行动作
type Engine struct {
	store agentmail.Store
}

// New 创建规则引擎
func New(store agentmail.Store) *Engine {
	return &Engine{store: store}
}

// Eval 对一封新收到的邮件执行规则匹配
// 返回触发的动作描述列表
func (e *Engine) Eval(msg *agentmail.Message, rawBytes []byte) []string {
	var results []string

	fromAddr := extractAddr(msg.From)
	toAddrs := msg.To
	subject := msg.Subject

	// 获取匹配的规则
	rules, err := e.store.GetMatchingRules(msg.Identity, fromAddr, strings.Join(toAddrs, ","), subject)
	if err != nil || len(rules) == 0 {
		return nil
	}

	for _, rule := range rules {
		if !rule.Active {
			continue
		}
		result := e.execAction(rule, msg, rawBytes)
		if result != "" {
			results = append(results, result)
		}
	}
	return results
}

func (e *Engine) execAction(rule agentmail.Rule, msg *agentmail.Message, raw []byte) string {
	switch rule.Action {
	case "webhook":
		return e.webhookAction(rule.ActionVal, msg)
	case "reply":
		return e.replyAction(rule, msg)
	case "forward":
		return e.forwardAction(rule.ActionVal, msg, raw)
	case "tag":
		return e.tagAction(rule.ActionVal, msg)
	case "discard":
		return e.discardAction(msg)
	default:
		return ""
	}
}

func (e *Engine) webhookAction(url string, msg *agentmail.Message) string {
	payload, _ := json.Marshal(map[string]interface{}{
		"event":   "new_email",
		"message_id": msg.MessageID,
		"from":    msg.From,
		"subject": msg.Subject,
		"id":      msg.ID,
	})

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		log.Printf("[rule] webhook %s failed: %v", url, err)
		return ""
	}
	resp.Body.Close()
	return fmt.Sprintf("webhook %s (%d)", url, resp.StatusCode)
}

func (e *Engine) replyAction(rule agentmail.Rule, msg *agentmail.Message) string {
	tpl := rule.ActionVal
	if tpl == "" {
		tpl = "已收到您的邮件，我们会尽快处理。"
	}
	// 简单模板替换
	tpl = strings.ReplaceAll(tpl, "{{from}}", msg.From)
	tpl = strings.ReplaceAll(tpl, "{{subject}}", msg.Subject)

	log.Printf("[rule] would auto-reply to %s: %s", msg.From, tpl)
	return fmt.Sprintf("auto-reply to %s", msg.From)
}

func (e *Engine) forwardAction(target string, msg *agentmail.Message, raw []byte) string {
	log.Printf("[rule] would forward to %s", target)
	return fmt.Sprintf("forward to %s", target)
}

func (e *Engine) tagAction(tag string, msg *agentmail.Message) string {
	flags := msg.Flags
	flags = append(flags, tag)
	e.store.UpdateMessageFlags(msg.ID, flags)
	return fmt.Sprintf("tagged: %s", tag)
}

func (e *Engine) discardAction(msg *agentmail.Message) string {
	e.store.MoveMessage(msg.ID, "trash")
	return "discarded"
}

// extractAddr 从 "Name <email>" 中提取 email
func extractAddr(full string) string {
	if idx := strings.Index(full, "<"); idx >= 0 {
		if end := strings.Index(full, ">"); end > idx {
			return full[idx+1 : end]
		}
	}
	return strings.TrimSpace(full)
}
