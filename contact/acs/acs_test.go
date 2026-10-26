package acs

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/EQUATION-CO-LTD/static-site-kit/contact"
)

type fakeAzure struct {
	tokenCalls int
	expiresOn  int64
	acsStatus  int
	gotAuth    string
	gotBody    map[string]any
	gotURL     string
}

func (f *fakeAzure) start(t *testing.T) *Sender {
	t.Helper()
	msi := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.tokenCalls++
		if r.Header.Get("X-IDENTITY-HEADER") != "hdr" || r.URL.Query().Get("resource") != "https://communication.azure.com" ||
			r.URL.Query().Get("api-version") != "2019-08-01" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"tok` + strconv.Itoa(f.tokenCalls) + `","expires_on":"` + strconv.FormatInt(f.expiresOn, 10) + `"}`))
	}))
	acsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.gotAuth = r.Header.Get("Authorization")
		f.gotURL = r.URL.String()
		b, _ := io.ReadAll(r.Body)
		f.gotBody = nil
		_ = json.Unmarshal(b, &f.gotBody)
		w.WriteHeader(f.acsStatus)
		_, _ = w.Write([]byte("reason"))
	}))
	t.Cleanup(msi.Close)
	t.Cleanup(acsSrv.Close)
	return &Sender{SenderAddress: "noreply@example.com", Endpoint: acsSrv.URL, IdentityEndpoint: msi.URL, IdentityHeader: "hdr"}
}

// 202 Accepted で完了。宛先・Reply-To・件名・本文・送信元とトークンが ACS に渡る。
func TestSend(t *testing.T) {
	f := &fakeAzure{expiresOn: time.Now().Add(time.Hour).Unix(), acsStatus: http.StatusAccepted}
	s := f.start(t)

	err := s.Send(contact.Mail{To: []string{"a@example.com", "b@example.com"}, ReplyTo: "r@example.com", Subject: "件名", Body: "本文"})
	if err != nil {
		t.Fatal(err)
	}
	if f.gotAuth != "Bearer tok1" || f.gotURL != "/emails:send?api-version=2023-03-31" {
		t.Errorf("auth=%q url=%q", f.gotAuth, f.gotURL)
	}
	if f.gotBody["senderAddress"] != "noreply@example.com" {
		t.Errorf("senderAddress = %v", f.gotBody["senderAddress"])
	}
	raw, _ := json.Marshal(f.gotBody)
	for _, want := range []string{
		`"recipients":{"to":[{"address":"a@example.com"},{"address":"b@example.com"}]}`,
		`"replyTo":[{"address":"r@example.com"}]`,
		`"content":{"plainText":"本文","subject":"件名"}`,
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("本文 JSON に %s が無い\n%s", want, raw)
		}
	}

	// Reply-To が空なら replyTo を付けない
	if err := s.Send(contact.Mail{To: []string{"a@example.com"}, Subject: "s", Body: "b"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.gotBody["replyTo"]; ok {
		t.Error("Reply-To が空なのに replyTo がある")
	}
}

// トークンは期限まで使い回し、期限が近づいたら取り直す。
func TestTokenCache(t *testing.T) {
	f := &fakeAzure{expiresOn: time.Now().Add(time.Hour).Unix(), acsStatus: http.StatusAccepted}
	s := f.start(t)
	m := contact.Mail{To: []string{"a@example.com"}}
	for range 3 {
		if err := s.Send(m); err != nil {
			t.Fatal(err)
		}
	}
	if f.tokenCalls != 1 {
		t.Errorf("トークン取得 = %d 回, want 1", f.tokenCalls)
	}

	f.tokenCalls = 0
	f.expiresOn = time.Now().Add(time.Minute).Unix() // 残り 2 分未満は取り直す
	s2 := f.start(t)
	for range 2 {
		if err := s2.Send(m); err != nil {
			t.Fatal(err)
		}
	}
	if f.tokenCalls != 2 {
		t.Errorf("トークン取得 = %d 回, want 2", f.tokenCalls)
	}
}

// 202 以外・マネージド ID が無い・トークン取得失敗は、すべてエラー（呼び出し側が再送する）。
func TestSendErrors(t *testing.T) {
	m := contact.Mail{To: []string{"a@example.com"}}

	f := &fakeAzure{expiresOn: time.Now().Add(time.Hour).Unix(), acsStatus: http.StatusBadRequest}
	if err := f.start(t).Send(m); err == nil || !strings.Contains(err.Error(), "acs send failed") || !strings.Contains(err.Error(), "reason") {
		t.Errorf("202 以外: %v", err)
	}

	if err := (&Sender{}).Send(m); err == nil || !strings.Contains(err.Error(), "managed identity endpoint not available") {
		t.Errorf("マネージド ID なし: %v", err)
	}

	bad := f.start(t)
	bad.IdentityHeader = "wrong"
	if err := bad.Send(m); err == nil || !strings.Contains(err.Error(), "msi token request failed") {
		t.Errorf("トークン取得失敗: %v", err)
	}
}

func TestFromEnv(t *testing.T) {
	t.Setenv("SENDER_ADDRESS", "from@example.com")
	t.Setenv("ACS_EMAIL_ENDPOINT", "https://acs.example.com")
	t.Setenv("IDENTITY_ENDPOINT", "http://msi.example.com")
	t.Setenv("IDENTITY_HEADER", "h")
	s := FromEnv()
	if s.SenderAddress != "from@example.com" || s.Endpoint != "https://acs.example.com" ||
		s.IdentityEndpoint != "http://msi.example.com" || s.IdentityHeader != "h" {
		t.Errorf("FromEnv = %+v", s)
	}
}
