package contact

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// ---- キューメッセージ（v2）と旧形式 ----

// Message は 1 件の問い合わせ。site が無い旧形式（equ のフロントが今も送る
// send_* の平らな JSON）は、equ の contact フォームとして読む。
type Message struct {
	Site           string
	Form           string
	To             string
	PageURL        string
	TurnstileToken string
	Fields         map[string]string
}

var reservedKeys = map[string]bool{
	"site": true, "form": true, "to": true, "fields": true, "page_url": true, "turnstile_token": true,
}

// scalar は入力値を文字列にする。文字列と文字列の配列（チェックボックス）だけを受け付け、
// それ以外の型は無かったものとして扱う。
func scalar(raw json.RawMessage) (string, bool) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, true
	}
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		return strings.Join(list, ", "), true
	}
	return "", false
}

func ParseMessage(body []byte) (*Message, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		return nil, err
	}
	str := func(key string) string {
		s, _ := scalar(top[key])
		return s
	}
	m := &Message{
		Site:           str("site"),
		Form:           str("form"),
		To:             str("to"),
		PageURL:        str("page_url"),
		TurnstileToken: str("turnstile_token"),
		Fields:         map[string]string{},
	}
	if m.Site == "" {
		m.Site = "equ"
	}
	if m.Form == "" {
		m.Form = "contact"
	}

	collect := func(src map[string]json.RawMessage, skipReserved bool) {
		for k, raw := range src {
			if skipReserved && reservedKeys[k] {
				continue
			}
			if v, ok := scalar(raw); ok {
				m.Fields[k] = v
			}
		}
	}
	if rawFields, ok := top["fields"]; ok {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(rawFields, &fields); err != nil {
			return nil, fmt.Errorf("fields: %w", err)
		}
		collect(fields, false)
	} else {
		collect(top, true) // 旧形式: send_* が最上位に並ぶ
	}
	return m, nil
}

// ---- メールの組み立て ----

// Mail は送る 1 通。宛先は呼び出し側（Sender）が実際の送信に使う。
type Mail struct {
	To      []string
	ReplyTo string
	Subject string
	Body    string
}

type Prepared struct {
	Site      *Site
	FormID    string
	TargetKey string
	Fields    map[string]string
	Notice    Mail
	Reply     *Mail // 自動返信しないときは nil
	Token     string
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) > max {
		return string(r[:max])
	}
	return s
}

// Prepare はメッセージを検証し、送るメールを組み立てる。破棄する場合は理由を返す
// （破棄は異常ではなく、リトライさせない）。
func Prepare(sites map[string]*Site, body []byte) (*Prepared, string) {
	m, err := ParseMessage(body)
	if err != nil {
		return nil, "malformed json"
	}
	site, ok := sites[m.Site]
	if !ok {
		return nil, "unknown site: " + truncateRunes(m.Site, 40)
	}
	form, ok := site.Forms[m.Form]
	if !ok {
		return nil, "unknown form: " + truncateRunes(m.Form, 40)
	}

	// 設定に無いキーは捨て、残りは上限で切り詰める。合計が大きすぎるものは丸ごと破棄する。
	allowed := form.inputKeys()
	fields := map[string]string{}
	total := 0
	for k, v := range m.Fields {
		if !allowed[k] {
			continue
		}
		limit, ok := form.Limits[k]
		if !ok {
			limit = defaultLimit
		}
		v = truncateRunes(v, limit)
		total += len([]rune(v))
		fields[k] = v
	}
	if total > maxTotalRunes {
		return nil, "message too large"
	}
	for _, k := range form.Required {
		if strings.TrimSpace(fields[k]) == "" {
			return nil, "required fields missing"
		}
	}

	targetKey := resolveTarget(site, &form, m, fields)
	target := site.Targets[targetKey]
	recipients := recipientsOf(target, form.Category)

	details := detailsBlock(&form, fields)
	label := func(s string) string { return strings.ReplaceAll(s, "{target}", target.Label) }

	notice := label(form.NoticeIntro) + "\n\n\n" + details
	if u := cleanPageURL(m.PageURL); u != "" {
		notice += "【送信元ページ】\n" + u + "\n\n"
	}
	p := &Prepared{
		Site:      site,
		FormID:    m.Form,
		TargetKey: targetKey,
		Fields:    fields,
		Token:     m.TurnstileToken,
		Notice: Mail{
			To:      recipients,
			Subject: label(form.NoticeSubject),
			Body:    notice,
		},
	}

	inquirer := fields[form.ReplyToField]
	if form.ReplyToField != "" && AutoReplyable(inquirer) {
		p.Notice.ReplyTo = inquirer
		if form.Reply.Enabled {
			p.Reply = &Mail{
				To:      []string{inquirer},
				ReplyTo: recipients[0],
				Subject: label(form.Reply.Subject),
				Body: fields[form.Reply.NameField] + " 様\n\n" +
					label(form.Reply.Intro) + "\n\n\n" +
					details +
					form.Reply.Outro + "\n\n" +
					site.Signature + "\n",
			}
		}
	}
	return p, ""
}

// resolveTarget は振り分け先のキーを決める。決められない・宛先が無いときは既定先。
func resolveTarget(site *Site, form *Form, m *Message, fields map[string]string) string {
	candidate := ""
	switch {
	case form.TargetFrom == "to":
		candidate = m.To
	case strings.HasPrefix(form.TargetFrom, "field:"):
		candidate = form.Aliases[fields[strings.TrimPrefix(form.TargetFrom, "field:")]]
	}
	if t, ok := site.Targets[candidate]; ok && len(recipientsOf(t, form.Category)) > 0 {
		return candidate
	}
	return form.DefaultTarget
}

// fieldValue は本文に出す 1 項目の値。結合指定があれば入力値を空白でつなぐ。
func fieldValue(fs Field, fields map[string]string) string {
	if len(fs.Join) == 0 {
		return fields[fs.Key]
	}
	var parts []string
	for _, k := range fs.Join {
		if v := fields[k]; v != "" {
			parts = append(parts, fs.Prefix[k]+v)
		}
	}
	return strings.Join(parts, " ")
}

// 担当者宛と自動返信で共通の《お問い合わせ内容》ブロック
func detailsBlock(form *Form, fields map[string]string) string {
	var b bytes.Buffer
	b.WriteString("《お問い合わせ内容》\n\n")
	for _, fs := range form.Fields {
		fmt.Fprintf(&b, "【%s】\n%s\n\n", fs.Label, fieldValue(fs, fields))
	}
	return b.String()
}

// 送信元ページは http(s) の 1 行だけを載せる
func cleanPageURL(u string) string {
	u = truncateRunes(strings.TrimSpace(u), 500)
	if strings.ContainsAny(u, " \t\r\n") {
		return ""
	}
	if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
		return ""
	}
	return u
}
