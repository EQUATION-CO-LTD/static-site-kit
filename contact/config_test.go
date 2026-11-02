package contact

import (
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

// 設定の fs.FS は直下の *.json だけを読む。それ以外（ディレクトリ・別の拡張子）は無視する。
func TestLoadSitesIgnoresNonJSON(t *testing.T) {
	valid, err := sitesFSFile("testdata/sites/equ.json")
	if err != nil {
		t.Fatal(err)
	}
	fsys := fstest.MapFS{
		"equ.json":        {Data: valid},
		"README.md":       {Data: []byte("設定ではない")},
		"sub/other.json":  {Data: []byte("{壊れている")},
		"backup.json.bak": {Data: []byte("{壊れている")},
	}
	loaded, err := LoadSites(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded["equ"] == nil {
		t.Errorf("読み込まれたサイト = %v, want equ のみ", loaded)
	}
}

// 不正な設定は起動させない（綴り間違い・重複・検査違反）。
func TestLoadSitesRejectsInvalid(t *testing.T) {
	valid, err := sitesFSFile("testdata/sites/equ.json")
	if err != nil {
		t.Fatal(err)
	}
	replace := func(old, new string) []byte {
		if !strings.Contains(string(valid), old) {
			t.Fatalf("テストの前提が崩れた: %q が無い", old)
		}
		return []byte(strings.Replace(string(valid), old, new, 1))
	}
	tests := []struct {
		name  string
		files fstest.MapFS
		want  string
	}{
		{"壊れた JSON", fstest.MapFS{"a.json": {Data: []byte("{")}}, "a.json"},
		{"未知のキー（綴り間違い）", fstest.MapFS{"a.json": {Data: replace(`"signature"`, `"signatur"`)}}, "unknown field"},
		{"id が空", fstest.MapFS{"a.json": {Data: replace(`"id": "equ"`, `"id": ""`)}}, "id が空"},
		{"id の重複", fstest.MapFS{"a.json": {Data: valid}, "b.json": {Data: valid}}, "重複"},
		{"宛先が不正", fstest.MapFS{"a.json": {Data: replace(`["info@example.com"]`, `["not-an-address"]`)}}, "不正"},
		{"宛先が 6 件", fstest.MapFS{"a.json": {Data: replace(`"general": ["info@example.com"]`, `"general": ["a@example.com","b@example.com","c@example.com","d@example.com","e@example.com","f@example.com"]`)}}, "最大"},
		{"category が不正", fstest.MapFS{"a.json": {Data: replace(`"category": "general"`, `"category": "other"`)}}, "category"},
		{"default_target が無い", fstest.MapFS{"a.json": {Data: replace(`"default_target": "main"`, `"default_target": "nothing"`)}}, "default_target"},
		{"target_from が不正", fstest.MapFS{"a.json": {Data: replace(`"target_from": "to"`, `"target_from": "x"`)}}, "target_from"},
		{"自動返信に reply_to_field が無い", fstest.MapFS{"a.json": {Data: replace(`"reply_to_field": "send_email"`, `"reply_to_field": ""`)}}, "reply_to_field"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadSites(tt.files)
			if err == nil {
				t.Fatal("エラーになるべき")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want %q を含む", err, tt.want)
			}
		})
	}
}

// 項目の値で振り分ける（target_from が field:<キー>）。別名表に無い値は既定先。
func TestTargetFromFieldAliases(t *testing.T) {
	base := map[string]string{"name": "山田", "email": "a@b.jp"}
	with := func(dept string) map[string]string {
		m := map[string]string{"dept": dept}
		for k, v := range base {
			m[k] = v
		}
		return m
	}
	tests := []struct {
		dept       string
		wantTarget string
		wantTo     []string
	}{
		{"営業", "group03", []string{"g03@example.jp"}},
		{"採用", "group04", []string{"g04@example.jp"}},
		{"総務", "hd", []string{"it@example.jp"}}, // 別名表に無い
		{"", "hd", []string{"it@example.jp"}},
	}
	for _, tt := range tests {
		t.Run(tt.dept, func(t *testing.T) {
			p := mustV2(t, "multi", "inquiry", "group04", with(tt.dept)) // to は無視される
			if p.TargetKey != tt.wantTarget || !reflect.DeepEqual(p.Notice.To, tt.wantTo) {
				t.Errorf("振り分け先 = %q %v, want %q %v", p.TargetKey, p.Notice.To, tt.wantTarget, tt.wantTo)
			}
		})
	}

	// field: の項目は入力として受け付けるキーに含まれる（本文の項目でなくても捨てられない）
	p := mustV2(t, "multi", "inquiry", "", with("営業"))
	if p.Fields["dept"] != "営業" {
		t.Errorf("dept = %q", p.Fields["dept"])
	}
}

// 結合指定の項目（郵便番号の接頭辞つき）は複数の入力値から 1 行になり、結合元のキーも受け付ける。
func TestJoinedFieldInBody(t *testing.T) {
	p := mustV2(t, "multi", "inquiry", "", map[string]string{
		"name": "山田", "email": "a@b.jp", "zip": "100-0000", "city": "東京都千代田区",
	})
	if want := "【住所】\n〒100-0000 東京都千代田区\n\n"; !strings.Contains(p.Notice.Body, want) {
		t.Errorf("本文に %q が無い\n%s", want, p.Notice.Body)
	}
}

// 返信先の項目が無いフォーム（memo）は、Reply-To も自動返信も付けない。項目の上限も効く。
func TestFormWithoutReplyToField(t *testing.T) {
	p := mustV2(t, "multi", "memo", "group03", map[string]string{"text": strings.Repeat("あ", 80), "email": "a@b.jp"})
	if p.Reply != nil || p.Notice.ReplyTo != "" {
		t.Errorf("返信先が無いのに Reply-To / 自動返信がある: %+v", p)
	}
	if got := len([]rune(p.Fields["text"])); got != 50 {
		t.Errorf("text = %d 文字, want 50", got)
	}
	if p.TargetKey != "hd" { // target_from が none のとき to は無視される
		t.Errorf("振り分け先 = %q, want hd", p.TargetKey)
	}
}

// 旧形式（site なし・フラットな send_*）と新形式（fields の包み）は同じ結果になる。
func TestLegacyAndV2AreEquivalent(t *testing.T) {
	fields := map[string]string{"send_name": "山田 太郎", "send_email": "a@b.jp", "send_comment": "テスト"}
	legacy := legacyPrepare(t, fields)
	v2 := mustV2(t, "equ", "contact", "", fields)
	if !reflect.DeepEqual(legacy, v2) {
		t.Errorf("旧形式と新形式で結果が違う\nlegacy: %+v\nv2: %+v", legacy, v2)
	}
}

// チェックボックスの配列は ", " でつなぐ。文字列・配列以外の値（数値・オブジェクト）は無かったものにする。
func TestParseMessageValueTypes(t *testing.T) {
	m, err := ParseMessage([]byte(`{"site":"multi","form":"contact","to":"group03","page_url":"https://x.example.jp/",
		"turnstile_token":"tok","fields":{"a":"文字列","b":["x","y"],"c":123,"d":{"k":1},"e":null}}`))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"a": "文字列", "b": "x, y", "e": ""} // null は空文字になる（従来どおり）
	if !reflect.DeepEqual(m.Fields, want) {
		t.Errorf("Fields = %v, want %v", m.Fields, want)
	}
	if m.Site != "multi" || m.Form != "contact" || m.To != "group03" || m.TurnstileToken != "tok" || m.PageURL != "https://x.example.jp/" {
		t.Errorf("解析結果 = %+v", m)
	}

	// 旧形式は予約キーを除いた最上位をすべて項目として読む
	legacy, err := ParseMessage([]byte(`{"send_name":"山田","turnstile_token":"t","page_url":"https://x.example.jp/"}`))
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Site != "equ" || legacy.Form != "contact" || !reflect.DeepEqual(legacy.Fields, map[string]string{"send_name": "山田"}) {
		t.Errorf("旧形式 = %+v", legacy)
	}

	if _, err := ParseMessage([]byte(`{"fields":"文字列"}`)); err == nil {
		t.Error("fields がオブジェクトでなければエラー")
	}
}
