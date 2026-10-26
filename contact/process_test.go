package contact

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// 送ったメールを記録する偽物。failOn の番目（1 始まり）の送信を失敗させる。
type fakeSender struct {
	sent   []Mail
	failOn map[int]bool
}

func (f *fakeSender) Send(m Mail) error {
	f.sent = append(f.sent, m)
	if f.failOn[len(f.sent)] {
		return errors.New("boom")
	}
	return nil
}

// 検証の偽物。呼ばれたときのサイトとトークンを記録する。
type fakeVerifier struct {
	ok    bool
	err   error
	calls []string
}

func (f *fakeVerifier) Verify(site *Site, token string) (bool, error) {
	f.calls = append(f.calls, site.ID+":"+token)
	return f.ok, f.err
}

func newProcessor(s *fakeSender, v *fakeVerifier) (*Processor, *[]string) {
	var logged []string
	return &Processor{
		Sites:    sites,
		Sender:   s,
		Verifier: v,
		Logf:     func(format string, args ...any) { logged = append(logged, format) },
	}, &logged
}

func contactBody(t *testing.T, email string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"site": "multi", "form": "contact", "to": "group03", "turnstile_token": "tok",
		"fields": map[string]string{"your_company": "x", "your_name": "山田", "addr": "東京", "your_tel": "090", "your_email": email},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// 通知と自動返信を、この順で 1 通ずつ送る。
func TestProcessSendsNoticeThenReply(t *testing.T) {
	s, v := &fakeSender{}, &fakeVerifier{ok: true}
	p, _ := newProcessor(s, v)

	logs, err := p.Process(contactBody(t, "a@b.jp"), true)
	if err != nil {
		t.Fatal(err)
	}
	wantLogs := []string{
		"notice mail sent (site=multi form=contact target=group03)",
		"auto-reply sent to inquirer",
	}
	if !reflect.DeepEqual(logs, wantLogs) {
		t.Errorf("logs = %v, want %v", logs, wantLogs)
	}
	if len(s.sent) != 2 {
		t.Fatalf("送信数 = %d, want 2", len(s.sent))
	}
	if !reflect.DeepEqual(s.sent[0].To, []string{"g03@example.jp"}) || s.sent[0].ReplyTo != "a@b.jp" {
		t.Errorf("通知 = %+v", s.sent[0])
	}
	if !reflect.DeepEqual(s.sent[1].To, []string{"a@b.jp"}) || s.sent[1].ReplyTo != "g03@example.jp" {
		t.Errorf("自動返信 = %+v", s.sent[1])
	}
	if !reflect.DeepEqual(v.calls, []string{"multi:tok"}) {
		t.Errorf("検証の呼び出し = %v", v.calls)
	}
}

// 自動返信しない問い合わせ（宛先が不正）は通知だけを送る。
func TestProcessNoticeOnly(t *testing.T) {
	s, v := &fakeSender{}, &fakeVerifier{ok: true}
	p, _ := newProcessor(s, v)

	logs, err := p.Process(contactBody(t, "a@b.jp, evil@x.jp"), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.sent) != 1 || len(logs) != 1 || strings.Contains(logs[0], "auto-reply") {
		t.Errorf("通知だけのはず: sent=%d logs=%v", len(s.sent), logs)
	}
}

// 破棄は正常完了（エラーなし）で、メールも検証もしない。
func TestProcessDiscards(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"壊れた JSON", `{not json`, "discarded: malformed json"},
		{"未知の site", `{"site":"nothing","fields":{}}`, "discarded: unknown site: nothing"},
		{"未知の form", `{"site":"multi","form":"nothing","fields":{}}`, "discarded: unknown form: nothing"},
		{"必須の欠け", `{"site":"multi","form":"contact","fields":{}}`, "discarded: required fields missing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, v := &fakeSender{}, &fakeVerifier{ok: true}
			p, _ := newProcessor(s, v)
			logs, err := p.Process([]byte(tt.body), true)
			if err != nil {
				t.Fatalf("破棄はエラーにしない: %v", err)
			}
			if !reflect.DeepEqual(logs, []string{tt.want}) {
				t.Errorf("logs = %v, want [%s]", logs, tt.want)
			}
			if len(s.sent) != 0 || len(v.calls) != 0 {
				t.Errorf("送信 %d 件 / 検証 %d 回、どちらも 0 のはず", len(s.sent), len(v.calls))
			}
		})
	}
}

// ボットと判定されたら破棄（正常完了）。メールは送らない。
func TestProcessDiscardsWhenBot(t *testing.T) {
	s, v := &fakeSender{}, &fakeVerifier{ok: false}
	p, _ := newProcessor(s, v)
	logs, err := p.Process(contactBody(t, "a@b.jp"), true)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(logs, []string{"discarded: turnstile verification failed"}) || len(s.sent) != 0 {
		t.Errorf("logs=%v sent=%d", logs, len(s.sent))
	}
}

// 検証の通信エラーは再送させる（ErrVerify）。メールは送らない。
func TestProcessVerifyErrorIsRetried(t *testing.T) {
	s, v := &fakeSender{}, &fakeVerifier{err: errors.New("network")}
	p, logged := newProcessor(s, v)
	_, err := p.Process(contactBody(t, "a@b.jp"), true)
	if !errors.Is(err, ErrVerify) {
		t.Fatalf("err = %v, want ErrVerify", err)
	}
	if len(s.sent) != 0 || len(*logged) != 1 {
		t.Errorf("sent=%d logged=%v", len(s.sent), *logged)
	}
}

// 2 回目以降の処理（リトライ）では、使用済みのトークンを再検証しない。
func TestProcessRetrySkipsVerify(t *testing.T) {
	s, v := &fakeSender{}, &fakeVerifier{ok: false, err: errors.New("呼ばれたら失敗")}
	p, _ := newProcessor(s, v)
	if _, err := p.Process(contactBody(t, "a@b.jp"), false); err != nil {
		t.Fatal(err)
	}
	if len(v.calls) != 0 || len(s.sent) != 2 {
		t.Errorf("検証 %d 回 / 送信 %d 件", len(v.calls), len(s.sent))
	}
}

// 通知の送信失敗は再送させる（ErrSend）。自動返信は送らない。
func TestProcessNoticeFailureIsRetried(t *testing.T) {
	s, v := &fakeSender{failOn: map[int]bool{1: true}}, &fakeVerifier{ok: true}
	p, _ := newProcessor(s, v)
	_, err := p.Process(contactBody(t, "a@b.jp"), true)
	if !errors.Is(err, ErrSend) {
		t.Fatalf("err = %v, want ErrSend", err)
	}
	if len(s.sent) != 1 {
		t.Errorf("通知が失敗したら自動返信は送らない: sent=%d", len(s.sent))
	}
}

// 自動返信の失敗では再送させない（通知が二重に届くため）。ログにだけ残す。
func TestProcessReplyFailureIsNotRetried(t *testing.T) {
	s, v := &fakeSender{failOn: map[int]bool{2: true}}, &fakeVerifier{ok: true}
	p, logged := newProcessor(s, v)
	logs, err := p.Process(contactBody(t, "a@b.jp"), true)
	if err != nil {
		t.Fatalf("自動返信の失敗はエラーにしない: %v", err)
	}
	if len(logs) != 2 || logs[1] != "auto-reply failed: boom" {
		t.Errorf("logs = %v", logs)
	}
	if len(*logged) != 1 {
		t.Errorf("サーバーログ = %v", *logged)
	}
}

// Logf が無くても動く。
func TestProcessWithoutLogf(t *testing.T) {
	p := &Processor{Sites: sites, Sender: &fakeSender{failOn: map[int]bool{1: true}}, Verifier: &fakeVerifier{ok: true}}
	if _, err := p.Process(contactBody(t, "a@b.jp"), true); !errors.Is(err, ErrSend) {
		t.Errorf("err = %v", err)
	}
}
