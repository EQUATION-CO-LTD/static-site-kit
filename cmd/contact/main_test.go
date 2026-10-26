package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/EQUATION-CO-LTD/static-site-kit/contact"
)

type fakeSender struct {
	sent   []contact.Mail
	failOn map[int]bool
}

func (f *fakeSender) Send(m contact.Mail) error {
	f.sent = append(f.sent, m)
	if f.failOn[len(f.sent)] {
		return errors.New("boom")
	}
	return nil
}

type fakeVerifier struct {
	ok    bool
	err   error
	calls int
}

func (f *fakeVerifier) Verify(*contact.Site, string) (bool, error) {
	f.calls++
	return f.ok, f.err
}

// testdata の架空サイトで組んだ処理系を httptest で起動する。
func start(t *testing.T, s *fakeSender, v *fakeVerifier) *httptest.Server {
	t.Helper()
	sites, err := contact.LoadSites(os.DirFS(filepath.Join("..", "..", "contact", "testdata", "sites")))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(newMux(&contact.Processor{Sites: sites, Sender: s, Verifier: v}))
	t.Cleanup(srv.Close)
	return srv
}

// 包みが何枚でも本文に辿り着けること（実際に一枚と二枚の両方を観測した）。
func TestUnwrap(t *testing.T) {
	payload := `{"send_name":"山田 太郎","send_email":"a@b.jp"}`
	once, _ := json.Marshal(payload)
	twice, _ := json.Marshal(string(once))

	for _, tt := range []struct {
		name  string
		input string
	}{
		{"包みなし", payload},
		{"一枚", string(once)},
		{"二枚", string(twice)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := unwrap(json.RawMessage(tt.input))
			if err != nil {
				t.Fatalf("unwrap() = %v", err)
			}
			m, err := contact.ParseMessage(got)
			if err != nil {
				t.Fatalf("解析できない: %v (%q)", err, got)
			}
			if m.Fields["send_name"] != "山田 太郎" {
				t.Errorf("send_name = %q, want %q", m.Fields["send_name"], "山田 太郎")
			}
		})
	}

	if _, err := unwrap(json.RawMessage(`"包みしかない"`)); err == nil {
		t.Error("本文に辿り着けない場合はエラーになるべき")
	}
}

// Functions の呼び出し規約の JSON を組み立てる。msg は Data.msg にそのまま入る。
func invoke(msg string, metadata string) string {
	if metadata == "" {
		metadata = "{}"
	}
	return `{"Data":{"msg":` + msg + `},"Metadata":` + metadata + `}`
}

// キューは本文を JSON 文字列として包んで渡す。
func wrapped(body string) string {
	b, _ := json.Marshal(body)
	return string(b)
}

const legacyBody = `{"send_name":"山田 太郎","send_email":"a@b.jp","send_comment":"テスト"}`

func post(t *testing.T, srv *httptest.Server, path, body string) (*http.Response, string) {
	t.Helper()
	resp, err := http.Post(srv.URL+path, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

// 正常系の応答は Outputs（空）・Logs・ReturnValue（null）の JSON。
func TestInvokeSuccess(t *testing.T) {
	s, v := &fakeSender{}, &fakeVerifier{ok: true}
	srv := start(t, s, v)

	resp, body := post(t, srv, "/ProcessContact", invoke(wrapped(legacyBody), `{"DequeueCount":"1"}`))
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("status=%d content-type=%q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	want := `{"Outputs":{},"Logs":["notice mail sent (site=equ form=contact target=main)","auto-reply sent to inquirer"],"ReturnValue":null}`
	if strings.TrimSpace(body) != want {
		t.Errorf("応答\n got: %s\nwant: %s", body, want)
	}
	if len(s.sent) != 2 || v.calls != 1 {
		t.Errorf("送信 %d 件 / 検証 %d 回", len(s.sent), v.calls)
	}
}

// 包みの枚数が違っても同じ結果になる。
func TestInvokeWrapping(t *testing.T) {
	for name, msg := range map[string]string{
		"包みなし": legacyBody,
		"一枚":   wrapped(legacyBody),
		"二枚":   wrapped(wrapped(legacyBody)),
	} {
		t.Run(name, func(t *testing.T) {
			s := &fakeSender{}
			srv := start(t, s, &fakeVerifier{ok: true})
			resp, _ := post(t, srv, "/ProcessContact", invoke(msg, ""))
			if resp.StatusCode != http.StatusOK || len(s.sent) != 2 {
				t.Errorf("status=%d 送信 %d 件", resp.StatusCode, len(s.sent))
			}
		})
	}
}

// 破棄はエラーではなく 200 の正常完了（リトライさせない）。
func TestInvokeDiscards(t *testing.T) {
	tests := []struct {
		name string
		msg  string
		want string
	}{
		{"包みしかない", `"包みしかない"`, "discarded: unwrap failed: "},
		{"msg が無い", `null`, "discarded: unwrap failed: "},
		{"壊れた本文", wrapped(`{not json`), "discarded: malformed json"},
		{"未知の site", wrapped(`{"site":"nothing","fields":{}}`), "discarded: unknown site: nothing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &fakeSender{}
			srv := start(t, s, &fakeVerifier{ok: true})
			resp, body := post(t, srv, "/ProcessContact", invoke(tt.msg, ""))
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d", resp.StatusCode)
			}
			var got struct{ Logs []string }
			if err := json.Unmarshal([]byte(body), &got); err != nil || len(got.Logs) != 1 || !strings.HasPrefix(got.Logs[0], tt.want) {
				t.Errorf("body=%s err=%v, want ログが %q で始まる 1 件", body, err, tt.want)
			}
			if len(s.sent) != 0 {
				t.Errorf("破棄なのに送信した: %d", len(s.sent))
			}
		})
	}

	// Data.msg の無い呼び出しも破棄
	srv := start(t, &fakeSender{}, &fakeVerifier{ok: true})
	if resp, _ := post(t, srv, "/ProcessContact", `{"Data":{},"Metadata":{}}`); resp.StatusCode != http.StatusOK {
		t.Errorf("Data.msg なし: status = %d", resp.StatusCode)
	}
}

// ボットと判定されたら破棄（200）。
func TestInvokeBotDiscarded(t *testing.T) {
	s := &fakeSender{}
	srv := start(t, s, &fakeVerifier{ok: false})
	resp, body := post(t, srv, "/ProcessContact", invoke(wrapped(legacyBody), ""))
	want := `{"Outputs":{},"Logs":["discarded: turnstile verification failed"],"ReturnValue":null}`
	if resp.StatusCode != http.StatusOK || strings.TrimSpace(body) != want || len(s.sent) != 0 {
		t.Errorf("status=%d body=%s sent=%d", resp.StatusCode, body, len(s.sent))
	}
}

// 再送の判定: 検証の通信エラーと通知の送信失敗は 500、自動返信の失敗は 200。
func TestInvokeRetryRules(t *testing.T) {
	t.Run("検証の通信エラーは 500", func(t *testing.T) {
		s := &fakeSender{}
		srv := start(t, s, &fakeVerifier{err: errors.New("network")})
		resp, body := post(t, srv, "/ProcessContact", invoke(wrapped(legacyBody), ""))
		if resp.StatusCode != http.StatusInternalServerError || strings.TrimSpace(body) != "turnstile verify error" || len(s.sent) != 0 {
			t.Errorf("status=%d body=%q sent=%d", resp.StatusCode, body, len(s.sent))
		}
	})
	t.Run("通知の送信失敗は 500", func(t *testing.T) {
		s := &fakeSender{failOn: map[int]bool{1: true}}
		srv := start(t, s, &fakeVerifier{ok: true})
		resp, body := post(t, srv, "/ProcessContact", invoke(wrapped(legacyBody), ""))
		if resp.StatusCode != http.StatusInternalServerError || strings.TrimSpace(body) != "send mail error" || len(s.sent) != 1 {
			t.Errorf("status=%d body=%q sent=%d", resp.StatusCode, body, len(s.sent))
		}
	})
	t.Run("自動返信の失敗は 200 で再送しない", func(t *testing.T) {
		s := &fakeSender{failOn: map[int]bool{2: true}}
		srv := start(t, s, &fakeVerifier{ok: true})
		resp, body := post(t, srv, "/ProcessContact", invoke(wrapped(legacyBody), ""))
		var got struct{ Logs []string }
		_ = json.Unmarshal([]byte(body), &got)
		want := []string{"notice mail sent (site=equ form=contact target=main)", "auto-reply failed: boom"}
		if resp.StatusCode != http.StatusOK || !reflect.DeepEqual(got.Logs, want) {
			t.Errorf("status=%d logs=%v", resp.StatusCode, got.Logs)
		}
	})
}

// DequeueCount が "1" のとき（無いときも）だけ検証する。2 回目以降は使用済みのトークンを再検証しない。
func TestInvokeDequeueCount(t *testing.T) {
	tests := []struct {
		name      string
		metadata  string
		wantCalls int
	}{
		{"1 回目", `{"DequeueCount":"1"}`, 1},
		{"Metadata に無い", `{}`, 1},
		{"2 回目", `{"DequeueCount":"2"}`, 0},
		{"5 回目", `{"DequeueCount":"5"}`, 0},
		{"文字列でない値は従来どおり 1 回目扱い", `{"DequeueCount":2}`, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &fakeVerifier{ok: true}
			srv := start(t, &fakeSender{}, v)
			if resp, _ := post(t, srv, "/ProcessContact", invoke(wrapped(legacyBody), tt.metadata)); resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d", resp.StatusCode)
			}
			if v.calls != tt.wantCalls {
				t.Errorf("検証 %d 回, want %d", v.calls, tt.wantCalls)
			}
		})
	}
}

// リクエストが JSON でなければ 400。/ProcessContact 以外は受けない。
func TestInvokeBadRequestAndPaths(t *testing.T) {
	srv := start(t, &fakeSender{}, &fakeVerifier{ok: true})
	if resp, _ := post(t, srv, "/ProcessContact", `{not json`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("壊れたリクエスト: status = %d", resp.StatusCode)
	}
	if resp, _ := post(t, srv, "/Other", invoke(wrapped(legacyBody), "")); resp.StatusCode != http.StatusNotFound {
		t.Errorf("別のパス: status = %d", resp.StatusCode)
	}
}

// サイト設定の場所は環境変数 CONTACT_SITES_DIR、無ければ実行ファイルの隣の sites/。
func TestSitesDir(t *testing.T) {
	t.Setenv("CONTACT_SITES_DIR", "/custom/sites")
	if got, err := sitesDir(); err != nil || got != "/custom/sites" {
		t.Errorf("sitesDir() = %q, %v", got, err)
	}
	t.Setenv("CONTACT_SITES_DIR", "")
	exe, _ := os.Executable()
	if got, err := sitesDir(); err != nil || got != filepath.Join(filepath.Dir(exe), "sites") {
		t.Errorf("sitesDir() = %q, %v", got, err)
	}
}
