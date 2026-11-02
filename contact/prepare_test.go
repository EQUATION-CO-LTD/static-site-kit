package contact

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

// テスト用のサイト設定（testdata/sites）。実在の宛先は入れていない。
var sites = mustLoadTestSites()

func mustLoadTestSites() map[string]*Site {
	s, err := LoadSites(os.DirFS("testdata/sites"))
	if err != nil {
		panic("テスト用のサイト設定が不正: " + err.Error())
	}
	return s
}

// 旧形式（site なし・send_* が最上位）のメッセージを equ の contact フォームとして処理する。
func legacyPrepare(t *testing.T, fields map[string]string) *Prepared {
	t.Helper()
	raw, _ := json.Marshal(fields)
	p, reason := Prepare(sites, raw)
	if p == nil {
		t.Fatalf("破棄された: %s", reason)
	}
	return p
}

// v2 形式のメッセージを組み立てて処理する。
func v2Prepare(site, form, to string, fields map[string]string) (*Prepared, string) {
	msg := map[string]any{"site": site, "form": form, "fields": fields}
	if to != "" {
		msg["to"] = to
	}
	raw, _ := json.Marshal(msg)
	return Prepare(sites, raw)
}

func mustV2(t *testing.T, site, form, to string, fields map[string]string) *Prepared {
	t.Helper()
	p, reason := v2Prepare(site, form, to, fields)
	if p == nil {
		t.Fatalf("破棄された: %s", reason)
	}
	return p
}

// ---- equ の既存動作（期待値は従来のまま） ----

// 旧 WordPress フォームが送っていた通知メールと同じ見た目になること。
// 実物のサンプル（2020-10-01 の問い合わせ）をそのまま期待値にしている。
func TestNoticeBodyMatchesLegacyLayout(t *testing.T) {
	p := legacyPrepare(t, map[string]string{
		"send_company":  "サンプル大学所属学生",
		"send_name":     "山田 太郎",
		"send_furigana": "ヤマダ タロウ",
		"send_email":    "user@example.ac.jp",
		"send_asktype":  "サービスについて",
		"send_comment":  "授業でVerdeを使ったC言語のコンパイラの遠隔接続をしようとしているのですが、\n何度も「もう一度デスクトップに接続して下さい」と表示されます。",
	})

	want := `『株式会社サンプル』よりお問い合わせがありました。
お問い合わせ内容は以下になります。


《お問い合わせ内容》

【会社名/部署名】
サンプル大学所属学生

【お名前】
山田 太郎

【フリガナ】
ヤマダ タロウ

【電話番号】


【メールアドレス】
user@example.ac.jp

【住所】


【お問い合わせ内容】
サービスについて

【お問い合わせ詳細】
授業でVerdeを使ったC言語のコンパイラの遠隔接続をしようとしているのですが、
何度も「もう一度デスクトップに接続して下さい」と表示されます。

`

	if got := p.Notice.Body; got != want {
		t.Errorf("通知メール本文が想定と違う\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	if want := "コーポレートサイト（sample.example.com）からのお問い合わせ"; p.Notice.Subject != want {
		t.Errorf("件名 = %q, want %q", p.Notice.Subject, want)
	}
	if !reflect.DeepEqual(p.Notice.To, []string{"info@example.com"}) {
		t.Errorf("宛先 = %v, want [info@example.com]", p.Notice.To)
	}
}

// 郵便番号・都道府県・番地は 1 行の【住所】にまとまり、未入力分は詰められる。
func TestAddressLine(t *testing.T) {
	var spec Field
	for _, fs := range sites["equ"].Forms["contact"].Fields {
		if fs.Label == "住所" {
			spec = fs
		}
	}
	tests := []struct {
		name            string
		zip, pref, addr string
		want            string
	}{
		{"全部あり", "100-0000", "東京都", "千代田区サンプル1-1-1 サンプルビル1階", "〒100-0000 東京都 千代田区サンプル1-1-1 サンプルビル1階"},
		{"郵便番号なし", "", "東京都", "千代田区サンプル1-1-1", "東京都 千代田区サンプル1-1-1"},
		{"全部なし", "", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fields := map[string]string{"send_zip": tt.zip, "send_pref": tt.pref, "send_add": tt.addr}
			if got := fieldValue(spec, fields); got != tt.want {
				t.Errorf("住所 = %q, want %q", got, tt.want)
			}
		})
	}
}

// 自動返信は問い合わせ者に宛てて送る。壊れた入力値には送らない。
// 担当者アドレスからの問い合わせにも通常どおり返信する。
func TestAutoReplyable(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{"user@example.ac.jp", true},
		{"info@example.com", true}, // 担当者アドレスでも送る
		{"", false},                // 未入力
		{"noatmark", false},        // @ なし
		{"a@b", false},             // ドメインにドットなし
		{"a@b@c.jp", false},        // @ が複数
		{"a@b.jp, x@y.jp", false},  // 宛先の追加を狙った入力
		{"a@b.jp\nbcc: x@y.jp", false},
	}
	for _, tt := range tests {
		if got := AutoReplyable(tt.addr); got != tt.want {
			t.Errorf("AutoReplyable(%q) = %v, want %v", tt.addr, got, tt.want)
		}
	}
}

// 自動返信は宛名・自動送信である旨・署名を含む。
func TestReplyBody(t *testing.T) {
	p := legacyPrepare(t, map[string]string{
		"send_name": "山田 太郎", "send_email": "a@b.jp", "send_asktype": "その他", "send_comment": "テストです",
	})
	if p.Reply == nil {
		t.Fatal("自動返信が組み立てられていない")
	}
	got := p.Reply.Body

	for _, want := range []string{
		"山田 太郎 様",
		"お問い合わせいただき、誠にありがとうございます",
		"《お問い合わせ内容》",
		"※本メールはシステムからの自動返信です。",
		"株式会社サンプル",
		"TEL: 00-0000-0000",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("自動返信に %q が含まれていない\n--- got ---\n%s", want, got)
		}
	}
	if p.Reply.Subject != "サンプルへのお問い合わせありがとうございます" {
		t.Errorf("自動返信の件名 = %q", p.Reply.Subject)
	}
	if !reflect.DeepEqual(p.Reply.To, []string{"a@b.jp"}) || p.Reply.ReplyTo != "info@example.com" {
		t.Errorf("自動返信の宛先 = %v / Reply-To = %q", p.Reply.To, p.Reply.ReplyTo)
	}
	if p.Notice.ReplyTo != "a@b.jp" {
		t.Errorf("担当者通知の Reply-To = %q, want 問い合わせ者", p.Notice.ReplyTo)
	}
}

// ---- 複数サイト ----

func TestSiteConfigsLoad(t *testing.T) {
	for _, id := range []string{"equ", "multi", "multi-demo"} {
		if sites[id] == nil {
			t.Errorf("サイト %q が読み込まれていない", id)
		}
	}
	if len(sites) != 3 {
		t.Errorf("サイトの数 = %d, want 3", len(sites))
	}
}

// multi の 4 フォームそれぞれが、設定どおりの宛先・件名で組み立てられる。
func TestMultiForms(t *testing.T) {
	const (
		itSystem = "it@example.jp"
		recruit  = "recruit@example.jp"
		g03      = "g03@example.jp"
	)
	tests := []struct {
		name        string
		form, to    string
		fields      map[string]string
		wantTarget  string
		wantTo      []string
		wantSubject string
		wantInBody  []string
	}{
		{
			name: "contact 既定は hd", form: "contact",
			fields:     map[string]string{"your_company": "株式会社○○", "your_name": "山田", "addr": "東京", "your_tel": "090", "your_email": "a@b.jp", "your_comment": "質問です"},
			wantTarget: "hd", wantTo: []string{itSystem},
			wantSubject: "【サンプル】お問い合わせフォームからのお問い合わせ（サンプルホールディングス）",
			wantInBody:  []string{"【貴社名】\n株式会社○○", "【お問い合わせ内容】\n質問です"},
		},
		{
			name: "contact は to で振り分け", form: "contact", to: "group03",
			fields:     map[string]string{"your_company": "x", "your_name": "山田", "addr": "東京", "your_tel": "090", "your_email": "a@b.jp"},
			wantTarget: "group03", wantTo: []string{g03},
			wantSubject: "【サンプル】お問い合わせフォームからのお問い合わせ（サンプル商事株式会社）",
			wantInBody:  []string{"サンプル商事株式会社宛のお問い合わせ"},
		},
		{
			name: "contact は未知の to なら既定先", form: "contact", to: "nothing",
			fields:     map[string]string{"your_company": "x", "your_name": "山田", "addr": "東京", "your_tel": "090", "your_email": "a@b.jp"},
			wantTarget: "hd", wantTo: []string{itSystem},
			wantSubject: "【サンプル】お問い合わせフォームからのお問い合わせ（サンプルホールディングス）",
		},
		{
			name: "entry は to（会社の URL 名）で振り分け。社名を変えても外れない", form: "entry", to: "group04",
			fields:     map[string]string{"your-name": "山田", "your-name2": "ヤマダ", "your-age": "1990年1月1日", "your-tel": "090", "your-email": "a@b.jp", "company": "サンプル自動車", "job": "事務職"},
			wantTarget: "group04", wantTo: []string{"g04@example.jp"},
			wantSubject: "【サンプル】採用問い合わせフォームからのお問い合わせ（サンプル自動車工業有限会社）",
			wantInBody:  []string{"【会社名】\nサンプル自動車", "【応募職種】\n事務職"},
		},
		{
			name: "entry は会社未選択なら既定先（求人）", form: "entry",
			fields:     map[string]string{"your-name": "山田", "your-name2": "ヤマダ", "your-age": "1990年1月1日", "your-tel": "090", "your-email": "a@b.jp"},
			wantTarget: "hd", wantTo: []string{recruit},
			wantSubject: "【サンプル】採用問い合わせフォームからのお問い合わせ（サンプルホールディングス）",
		},
		{
			name: "apply は to（求人の会社の URL 名）で振り分け", form: "apply", to: "group01",
			fields:     map[string]string{"your-tag": "サンプル運輸株式会社", "your-title": "ドライバー", "your-url": "https://multi.example.jp/requirements/detail1/", "your-name": "山田", "your-name2": "ヤマダ", "your-tel": "090"},
			wantTarget: "group01", wantTo: []string{recruit},
			wantSubject: "【サンプル】募集要項ページからの応募（サンプル運輸株式会社）",
			wantInBody:  []string{"【応募した募集要項】\nドライバー", "【募集要項のURL】\nhttps://multi.example.jp/requirements/detail1/"},
		},
		{
			name: "apply は宛先未設定の会社（宛先の設定が無い会社）なら既定先", form: "apply", to: "unconfigured",
			fields:     map[string]string{"your-tag": "宛先未設定の会社", "your-name": "山田", "your-name2": "ヤマダ", "your-tel": "090"},
			wantTarget: "hd", wantTo: []string{recruit},
			wantSubject: "【サンプル】募集要項ページからの応募（サンプルホールディングス）",
		},
		{
			name: "briefing は常に hd（to を無視）", form: "briefing", to: "group03",
			fields:     map[string]string{"date": "11月1日", "base": "泉南営業所", "your-name": "山田", "your-tel": "090", "your-email": "a@b.jp"},
			wantTarget: "hd", wantTo: []string{recruit},
			wantSubject: "【サンプル】会社説明会のお申し込み",
			wantInBody:  []string{"【希望日】\n11月1日", "【希望営業所名】\n泉南営業所"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := mustV2(t, "multi", tt.form, tt.to, tt.fields)
			if p.TargetKey != tt.wantTarget {
				t.Errorf("振り分け先 = %q, want %q", p.TargetKey, tt.wantTarget)
			}
			if !reflect.DeepEqual(p.Notice.To, tt.wantTo) {
				t.Errorf("宛先 = %v, want %v", p.Notice.To, tt.wantTo)
			}
			if p.Notice.Subject != tt.wantSubject {
				t.Errorf("件名 = %q, want %q", p.Notice.Subject, tt.wantSubject)
			}
			for _, w := range tt.wantInBody {
				if !strings.Contains(p.Notice.Body, w) {
					t.Errorf("本文に %q が無い\n%s", w, p.Notice.Body)
				}
			}
		})
	}
}

// 宛先が複数の振り分け先は To に上限の 5 件まで並べ、自動返信の Reply-To は 1 件目になる。
func TestMultipleRecipientsAndReplyTo(t *testing.T) {
	site := *sites["multi"]
	site.Targets = map[string]Target{}
	for k, v := range sites["multi"].Targets {
		site.Targets[k] = v
	}
	site.Targets["group03"] = Target{
		Label:   "サンプル商事株式会社",
		General: []string{"first@example.jp", "second@example.jp", "third@example.jp", "fourth@example.jp", "fifth@example.jp", "sixth@example.jp"}, // 6 件目は切る
		Recruit: []string{"r@example.jp"},
	}
	two := map[string]*Site{"multi": &site}

	raw, _ := json.Marshal(map[string]any{
		"site": "multi", "form": "contact", "to": "group03",
		"fields": map[string]string{"your_company": "x", "your_name": "山田", "addr": "東京", "your_tel": "090", "your_email": "a@b.jp"},
	})
	p, reason := Prepare(two, raw)
	if p == nil {
		t.Fatalf("破棄された: %s", reason)
	}
	if !reflect.DeepEqual(p.Notice.To, []string{"first@example.jp", "second@example.jp", "third@example.jp", "fourth@example.jp", "fifth@example.jp"}) {
		t.Errorf("To = %v, want 5 件", p.Notice.To)
	}
	if p.Reply == nil || p.Reply.ReplyTo != "first@example.jp" {
		t.Errorf("自動返信の Reply-To は 1 件目のはず: %+v", p.Reply)
	}
	if !reflect.DeepEqual(p.Reply.To, []string{"a@b.jp"}) {
		t.Errorf("自動返信の宛先 = %v", p.Reply.To)
	}
}

// 宛先が 0 件の振り分け先は既定先に落とす。
func TestEmptyRecipientsFallsBackToDefault(t *testing.T) {
	site := *sites["multi"]
	site.Targets = map[string]Target{}
	for k, v := range sites["multi"].Targets {
		site.Targets[k] = v
	}
	site.Targets["group03"] = Target{Label: "空", General: nil, Recruit: nil}
	raw, _ := json.Marshal(map[string]any{
		"site": "multi", "form": "contact", "to": "group03",
		"fields": map[string]string{"your_company": "x", "your_name": "山田", "addr": "東京", "your_tel": "090", "your_email": "a@b.jp"},
	})
	p, reason := Prepare(map[string]*Site{"multi": &site}, raw)
	if p == nil {
		t.Fatalf("破棄された: %s", reason)
	}
	if p.TargetKey != "hd" {
		t.Errorf("振り分け先 = %q, want hd", p.TargetKey)
	}
}

// 未知の site / form、必須項目の欠け、壊れた JSON は破棄する（リトライしない）。
func TestDiscards(t *testing.T) {
	ok := map[string]string{"your_company": "x", "your_name": "山田", "addr": "東京", "your_tel": "090", "your_email": "a@b.jp"}
	without := func(key string) map[string]string {
		m := map[string]string{}
		for k, v := range ok {
			if k != key {
				m[k] = v
			}
		}
		return m
	}
	tests := []struct {
		name         string
		site, form   string
		fields       map[string]string
		wantContains string
	}{
		{"未知の site", "nothing", "contact", ok, "unknown site"},
		{"未知の form", "multi", "nothing", ok, "unknown form"},
		{"必須の欠け: 会社名", "multi", "contact", without("your_company"), "required"},
		{"必須の欠け: メール", "multi", "contact", without("your_email"), "required"},
		{"必須が空白だけ", "multi", "contact", map[string]string{"your_company": "x", "your_name": "  ", "addr": "a", "your_tel": "1", "your_email": "a@b.jp"}, "required"},
		{"別のフォームのキーしか無い", "multi", "briefing", ok, "required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, reason := v2Prepare(tt.site, tt.form, "", tt.fields)
			if p != nil {
				t.Fatal("破棄されるべき")
			}
			if !strings.Contains(reason, tt.wantContains) {
				t.Errorf("理由 = %q, want %q を含む", reason, tt.wantContains)
			}
		})
	}
	if p, reason := Prepare(sites, []byte(`{not json`)); p != nil || reason != "malformed json" {
		t.Errorf("壊れた JSON: p=%v reason=%q", p, reason)
	}
	// 旧形式で必須（お名前・メール）が欠けるのも破棄
	if p, _ := Prepare(sites, []byte(`{"send_name":"山田"}`)); p != nil {
		t.Error("旧形式でメール欠けは破棄されるべき")
	}
}

// 設定に無いキーは本文にも宛先にも影響しない。
func TestUnknownKeysIgnored(t *testing.T) {
	p := mustV2(t, "multi", "contact", "", map[string]string{
		"your_company": "x", "your_name": "山田", "addr": "東京", "your_tel": "090", "your_email": "a@b.jp",
		"evil":       "混入した値",
		"company":    "サンプル自動車", // entry 用のキー。contact では使わない
		"your-email": "x@y.jp",
	})
	if strings.Contains(p.Notice.Body, "混入した値") || strings.Contains(p.Notice.Body, "サンプル自動車") {
		t.Errorf("設定外のキーが本文に出ている\n%s", p.Notice.Body)
	}
	if _, ok := p.Fields["evil"]; ok {
		t.Error("設定外のキーが残っている")
	}
	if p.TargetKey != "hd" {
		t.Errorf("振り分け先 = %q", p.TargetKey)
	}
}

// 項目は上限文字数で切り詰め、合計が大きすぎるメッセージは破棄する。
func TestLimits(t *testing.T) {
	long := strings.Repeat("あ", 6000)
	p := mustV2(t, "multi", "contact", "", map[string]string{
		"your_company": "x", "your_name": strings.Repeat("名", 150), "addr": "東京", "your_tel": "090", "your_email": "a@b.jp",
		"your_comment": long,
	})
	if got := len([]rune(p.Fields["your_name"])); got != 100 {
		t.Errorf("お名前 = %d 文字, want 100", got)
	}
	if got := len([]rune(p.Fields["your_comment"])); got != 5000 {
		t.Errorf("お問い合わせ内容 = %d 文字, want 5000", got)
	}

	// 上限の無い項目には既定の上限（defaultLimit）がかかる
	site := *sites["multi"]
	form := site.Forms["contact"]
	form.Limits = map[string]int{}
	site.Forms = map[string]Form{"contact": form}
	raw, _ := json.Marshal(map[string]any{"site": "multi", "form": "contact", "fields": map[string]string{
		"your_company": "x", "your_name": "山田", "addr": "東京", "your_tel": "090", "your_email": "a@b.jp", "your_comment": long,
	}})
	q, reason := Prepare(map[string]*Site{"multi": &site}, raw)
	if q == nil {
		t.Fatalf("破棄された: %s", reason)
	}
	if got := len([]rune(q.Fields["your_comment"])); got != defaultLimit {
		t.Errorf("既定上限 = %d 文字, want %d", got, defaultLimit)
	}

	// 合計が上限を超えるものは破棄（項目の上限を緩めた設定で確認）
	form.Limits = map[string]int{"your_comment": 100000, "your_company": 100000}
	site.Forms = map[string]Form{"contact": form}
	raw, _ = json.Marshal(map[string]any{"site": "multi", "form": "contact", "fields": map[string]string{
		"your_company": strings.Repeat("a", 15000), "your_name": "山田", "addr": "東京", "your_tel": "090", "your_email": "a@b.jp",
		"your_comment": strings.Repeat("b", 15000),
	}})
	if q, reason := Prepare(map[string]*Site{"multi": &site}, raw); q != nil || !strings.Contains(reason, "too large") {
		t.Errorf("合計超過は破棄されるべき: q=%v reason=%q", q, reason)
	}
}

// 自動返信は問い合わせ者のアドレスが妥当なときだけ。応募フォームはメールが任意。
func TestAutoReplyOnlyWhenAddressValid(t *testing.T) {
	base := map[string]string{"your-tag": "サンプル運輸株式会社", "your-name": "山田", "your-name2": "ヤマダ", "your-tel": "090"}
	if p := mustV2(t, "multi", "apply", "", base); p.Reply != nil || p.Notice.ReplyTo != "" {
		t.Errorf("メール未入力では自動返信も Reply-To も付けない: %+v", p)
	}
	withMail := map[string]string{"your-email": "a@b.jp"}
	for k, v := range base {
		withMail[k] = v
	}
	p := mustV2(t, "multi", "apply", "", withMail)
	if p.Reply == nil || p.Reply.ReplyTo != "recruit@example.jp" {
		t.Errorf("自動返信 = %+v", p.Reply)
	}
	withMail["your-email"] = "a@b.jp, evil@x.jp"
	if p := mustV2(t, "multi", "apply", "", withMail); p.Reply != nil {
		t.Error("宛先追加を狙った入力に自動返信してはいけない")
	}
}

// 自動返信は設定 1 つで止められる。
func TestReplyCanBeDisabled(t *testing.T) {
	site := *sites["multi"]
	form := site.Forms["briefing"]
	form.Reply.Enabled = false
	site.Forms = map[string]Form{"briefing": form}
	raw, _ := json.Marshal(map[string]any{"site": "multi", "form": "briefing", "fields": map[string]string{
		"date": "11月1日", "base": "泉南", "your-name": "山田", "your-tel": "090", "your-email": "a@b.jp",
	}})
	p, reason := Prepare(map[string]*Site{"multi": &site}, raw)
	if p == nil {
		t.Fatalf("破棄された: %s", reason)
	}
	if p.Reply != nil {
		t.Error("無効にした自動返信が組み立てられている")
	}
	if p.Notice.ReplyTo != "a@b.jp" {
		t.Errorf("通知の Reply-To = %q", p.Notice.ReplyTo)
	}
}

// 自動返信は multi の署名と宛名、Reply-To は振り分け先の 1 件目。送信元ページは担当者通知だけに載る。
func TestMultiReplyAndPageURL(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{
		"site": "multi", "form": "contact", "to": "group04",
		"page_url": "https://multi.example.jp/contact/?to=group04",
		"fields":   map[string]string{"your_company": "株式会社○○", "your_name": "山田 太郎", "addr": "東京", "your_tel": "090", "your_email": "a@b.jp"},
	})
	p, reason := Prepare(sites, raw)
	if p == nil {
		t.Fatalf("破棄された: %s", reason)
	}
	if !strings.Contains(p.Notice.Body, "【送信元ページ】\nhttps://multi.example.jp/contact/?to=group04") {
		t.Errorf("通知に送信元ページが無い\n%s", p.Notice.Body)
	}
	if strings.Contains(p.Reply.Body, "送信元ページ") {
		t.Error("自動返信に送信元ページを載せてはいけない")
	}
	for _, w := range []string{"山田 太郎 様", "サンプルホールディングス", "https://multi.example.jp/"} {
		if !strings.Contains(p.Reply.Body, w) {
			t.Errorf("自動返信に %q が無い\n%s", w, p.Reply.Body)
		}
	}
	if p.Reply.ReplyTo != "g04@example.jp" {
		t.Errorf("自動返信の Reply-To = %q", p.Reply.ReplyTo)
	}
	// 不正な page_url は載せない
	raw, _ = json.Marshal(map[string]any{
		"site": "multi", "form": "contact", "page_url": "javascript:alert(1)",
		"fields": map[string]string{"your_company": "x", "your_name": "山田", "addr": "東京", "your_tel": "090", "your_email": "a@b.jp"},
	})
	if q, _ := Prepare(sites, raw); strings.Contains(q.Notice.Body, "送信元ページ") {
		t.Error("http(s) 以外の page_url を載せてはいけない")
	}
}

// 目視確認用。DUMP_MAILS=1 go test -run TestDumpMails -v で実際に送られるメールを表示する。
func TestDumpMails(t *testing.T) {
	if os.Getenv("DUMP_MAILS") == "" {
		t.Skip("DUMP_MAILS=1 のときだけ出力する")
	}
	dump := func(title string, p *Prepared) {
		t.Logf("\n########## %s ##########\n===== 担当者宛 =====\nTo: %s\nReply-To: %s\nSubject: %s\n\n%s",
			title, strings.Join(p.Notice.To, ", "), p.Notice.ReplyTo, p.Notice.Subject, p.Notice.Body)
		if p.Reply != nil {
			t.Logf("\n===== 問い合わせ者宛（自動返信）=====\nTo: %s\nReply-To: %s\nSubject: %s\n\n%s",
				strings.Join(p.Reply.To, ", "), p.Reply.ReplyTo, p.Reply.Subject, p.Reply.Body)
		}
	}

	dump("equ（旧形式）", legacyPrepare(t, map[string]string{
		"send_company": "サンプル大学所属学生", "send_name": "山田 太郎", "send_furigana": "ヤマダ タロウ",
		"send_tel": "022-000-0000", "send_email": "user@example.ac.jp",
		"send_zip": "980-8577", "send_pref": "宮城県", "send_add": "仙台市青葉区荒巻字青葉6-6",
		"send_asktype": "サービスについて", "send_comment": "授業でVerdeを使ったC言語のコンパイラの遠隔接続をしようとしています。",
	}))
	dump("multi contact（to=group03）", mustV2(t, "multi", "contact", "group03", map[string]string{
		"your_company": "株式会社テスト", "your_name": "山田 太郎", "addr": "東京都千代田区1-1-1",
		"your_tel": "090-0000-0000", "your_email": "taro@example.jp", "your_comment": "車両の購入について相談したいです。",
	}))
	dump("multi entry（サンプル自動車）", mustV2(t, "multi", "entry", "", map[string]string{
		"your-name": "山田 太郎", "your-name2": "ヤマダ タロウ", "your-age": "1990年1月1日", "your-tel": "090-0000-0000",
		"your-email": "taro@example.jp", "addr": "東京都", "company": "サンプル自動車", "job": "事務職",
	}))
	dump("multi apply（運輸会社の募集要項）", mustV2(t, "multi", "apply", "", map[string]string{
		"your-tag": "サンプル運輸株式会社", "your-title": "大型ドライバー", "your-url": "https://multi.example.jp/requirements/detail3840_20260829/",
		"your-name": "山田 太郎", "your-name2": "ヤマダ タロウ", "your-tel": "090-0000-0000", "your-email": "taro@example.jp",
		"job": "ドライバー職", "your-comment": "平日は19時以降にご連絡ください",
	}))
	dump("multi briefing", mustV2(t, "multi", "briefing", "", map[string]string{
		"date": "11月15日", "base": "泉南営業所", "your-name": "山田 太郎", "your-tel": "090-0000-0000",
		"your-email": "taro@example.jp", "your-time": "平日19時以降",
	}))
}

// 検証環境（デモ）の設定は、どの振り分け先も検証用の宛先（example.com）にしか送らない。
// 先方の本物の宛先へデモの送信が届かないことを固定する
func TestDemoSendsOnlyToSandbox(t *testing.T) {
	demo := sites["multi-demo"]
	if demo == nil {
		t.Fatal("multi-demo が無い")
	}
	if len(demo.Targets) != len(sites["multi"].Targets) {
		t.Errorf("振り分け先の数が本番と違う: demo %d / multi %d", len(demo.Targets), len(sites["multi"].Targets))
	}
	for key, tg := range demo.Targets {
		for _, a := range append(append([]string{}, tg.General...), tg.Recruit...) {
			if !strings.HasSuffix(a, "@example.com") {
				t.Errorf("%s の宛先 %s が検証用以外", key, a)
			}
		}
	}
}

// 設定ファイルの中身を読む（テスト用）
func sitesFSFile(name string) ([]byte, error) { return os.ReadFile(name) }
