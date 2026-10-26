package contact

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"strings"
)

// サイト設定（sites/*.json）。宛先・件名・本文の文言・項目の並びはすべてここで決まり、
// コードはサイトに依存しない。宛先を変えるときは JSON を直して関数を再デプロイする。
// 設定はキットに含めず、利用側が fs.FS で渡す（お客様の宛先をキットに入れないため）。

type Site struct {
	ID                 string            `json:"id"`
	Origins            []string          `json:"origins"` // 設置先の Origin（CORS は Terraform 側。ここは台帳として持つ）
	SenderName         string            `json:"sender_name"`
	TurnstileSecretEnv string            `json:"turnstile_secret_env"`
	Signature          string            `json:"signature"`
	Targets            map[string]Target `json:"targets"`
	Forms              map[string]Form   `json:"forms"`
}

type Target struct {
	Label   string   `json:"label"`
	General []string `json:"general"`
	Recruit []string `json:"recruit"`
}

type Form struct {
	Category      string            `json:"category"` // targets の general / recruit
	DefaultTarget string            `json:"default_target"`
	TargetFrom    string            `json:"target_from"` // "to" | "field:<キー>" | "none"
	Aliases       map[string]string `json:"aliases"`
	NoticeSubject string            `json:"notice_subject"`
	NoticeIntro   string            `json:"notice_intro"`
	Reply         Reply             `json:"reply"`
	Required      []string          `json:"required"`
	ReplyToField  string            `json:"reply_to_field"`
	Limits        map[string]int    `json:"limits"` // 項目ごとの上限文字数（rune）。無ければ defaultLimit
	Fields        []Field           `json:"fields"`
}

type Reply struct {
	Enabled   bool   `json:"enabled"`
	Subject   string `json:"subject"`
	NameField string `json:"name_field"`
	Intro     string `json:"intro"`
	Outro     string `json:"outro"`
}

// Field は本文に出す 1 項目。Join があるときは複数の入力値を空白でつないで 1 行にする
// （Prefix はつなぐ前に各値の頭へ付ける文字。未入力の値は詰める）。
type Field struct {
	Key    string            `json:"key"`
	Label  string            `json:"label"`
	Join   []string          `json:"join"`
	Prefix map[string]string `json:"prefix"`
}

const (
	defaultLimit  = 1000  // limits に無い項目の上限
	maxTotalRunes = 20000 // 1 メッセージの項目値の合計上限。超えたら破棄
	maxRecipients = 2
)

// inputKeys はフォームが受け取ってよい入力キー（本文の項目・結合元・振り分けの項目）。
// これ以外のキーは無視する。
func (f *Form) inputKeys() map[string]bool {
	keys := map[string]bool{}
	for _, fs := range f.Fields {
		if len(fs.Join) > 0 {
			for _, k := range fs.Join {
				keys[k] = true
			}
		} else {
			keys[fs.Key] = true
		}
	}
	for _, k := range f.Required {
		keys[k] = true
	}
	if k, ok := strings.CutPrefix(f.TargetFrom, "field:"); ok {
		keys[k] = true
	}
	return keys
}

func (s *Site) validate() error {
	if s.ID == "" {
		return fmt.Errorf("id が空")
	}
	if len(s.Targets) == 0 || len(s.Forms) == 0 {
		return fmt.Errorf("targets / forms が空")
	}
	for tk, t := range s.Targets {
		for _, list := range [][]string{t.General, t.Recruit} {
			if len(list) > maxRecipients {
				return fmt.Errorf("targets.%s: 宛先は最大 %d 件", tk, maxRecipients)
			}
			for _, a := range list {
				if !AutoReplyable(a) {
					return fmt.Errorf("targets.%s: 宛先 %q が不正", tk, a)
				}
			}
		}
	}
	for fk, f := range s.Forms {
		where := "forms." + fk
		if f.Category != "general" && f.Category != "recruit" {
			return fmt.Errorf("%s: category は general / recruit", where)
		}
		if _, ok := s.Targets[f.DefaultTarget]; !ok {
			return fmt.Errorf("%s: default_target %q が targets に無い", where, f.DefaultTarget)
		}
		if len(recipientsOf(s.Targets[f.DefaultTarget], f.Category)) == 0 {
			return fmt.Errorf("%s: 既定の振り分け先に宛先が無い", where)
		}
		if f.TargetFrom != "to" && f.TargetFrom != "none" && !strings.HasPrefix(f.TargetFrom, "field:") {
			return fmt.Errorf("%s: target_from が不正 %q", where, f.TargetFrom)
		}
		for a, t := range f.Aliases {
			if _, ok := s.Targets[t]; !ok {
				return fmt.Errorf("%s: aliases[%s] の %q が targets に無い", where, a, t)
			}
		}
		if len(f.Fields) == 0 {
			return fmt.Errorf("%s: fields が空", where)
		}
		if f.Reply.Enabled && (f.ReplyToField == "" || f.Reply.Subject == "") {
			return fmt.Errorf("%s: 自動返信には reply_to_field と reply.subject が要る", where)
		}
	}
	return nil
}

func recipientsOf(t Target, category string) []string {
	list := t.General
	if category == "recruit" {
		list = t.Recruit
	}
	if len(list) > maxRecipients {
		list = list[:maxRecipients]
	}
	return list
}

// LoadSites は fsys 直下の *.json をサイト設定として読み、サイト ID で引ける形にする。
// 1 つでも不正なら全体を失敗させる（壊れた設定のまま起動させない）。
func LoadSites(fsys fs.FS) (map[string]*Site, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	sites := map[string]*Site{}
	for _, e := range entries {
		if e.IsDir() || path.Ext(e.Name()) != ".json" {
			continue
		}
		raw, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, err
		}
		var s Site
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields() // 綴り間違いの設定を黙って無視しない
		if err := dec.Decode(&s); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		if err := s.validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		if _, dup := sites[s.ID]; dup {
			return nil, fmt.Errorf("%s: id %q が重複", e.Name(), s.ID)
		}
		sites[s.ID] = &s
	}
	return sites, nil
}
