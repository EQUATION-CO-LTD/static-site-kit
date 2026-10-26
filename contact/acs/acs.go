// Package acs は contact.Sender の実装。Azure Communication Services (ACS) Email へ
// REST で直接送る。認証は App Service の Managed Identity エンドポイント
// （IDENTITY_ENDPOINT）を直接叩く。外部モジュール依存なし（標準ライブラリのみ）。
package acs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/EQUATION-CO-LTD/static-site-kit/contact"
)

var _ contact.Sender = (*Sender)(nil)

// Sender は ACS Email で送る。設定は環境変数から FromEnv で読む。
type Sender struct {
	SenderAddress    string       // 送信元アドレス（SENDER_ADDRESS）
	Endpoint         string       // ACS Email のエンドポイント（ACS_EMAIL_ENDPOINT）
	IdentityEndpoint string       // Managed Identity のエンドポイント（IDENTITY_ENDPOINT）
	IdentityHeader   string       // 同ヘッダー値（IDENTITY_HEADER）
	Client           *http.Client // 無ければ http.DefaultClient

	mu     sync.Mutex
	token  string
	expiry time.Time
}

// FromEnv は環境変数（SENDER_ADDRESS・ACS_EMAIL_ENDPOINT・IDENTITY_ENDPOINT・IDENTITY_HEADER）
// から設定を読む。マネージド ID が無い環境でも作れる（足りないときは送信時にエラーになる）。
func FromEnv() *Sender {
	return &Sender{
		SenderAddress:    os.Getenv("SENDER_ADDRESS"),
		Endpoint:         os.Getenv("ACS_EMAIL_ENDPOINT"),
		IdentityEndpoint: os.Getenv("IDENTITY_ENDPOINT"),
		IdentityHeader:   os.Getenv("IDENTITY_HEADER"),
	}
}

func (s *Sender) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return http.DefaultClient
}

// ---- Managed Identity トークン（App Service の MSI エンドポイント） ----

func (s *Sender) accessToken() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != "" && time.Until(s.expiry) > 2*time.Minute {
		return s.token, nil
	}

	endpoint := s.IdentityEndpoint
	header := s.IdentityHeader
	if endpoint == "" || header == "" {
		return "", fmt.Errorf("managed identity endpoint not available")
	}
	u := fmt.Sprintf("%s?resource=%s&api-version=2019-08-01",
		endpoint, url.QueryEscape("https://communication.azure.com"))
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-IDENTITY-HEADER", header)
	resp, err := s.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("msi token request failed: %s: %s", resp.Status, body)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresOn   string `json:"expires_on"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", err
	}
	s.token = tok.AccessToken
	if sec, err := strconv.ParseInt(tok.ExpiresOn, 10, 64); err == nil {
		s.expiry = time.Unix(sec, 0)
	} else {
		s.expiry = time.Now().Add(30 * time.Minute)
	}
	return s.token, nil
}

// ---- ACS Email 送信（REST 直叩き、202 Accepted で完了扱い） ----

// Send は m を ACS Email に渡す。202 Accepted で完了扱い。
func (s *Sender) Send(m contact.Mail) error {
	token, err := s.accessToken()
	if err != nil {
		return err
	}

	to := make([]map[string]string, 0, len(m.To))
	for _, a := range m.To {
		to = append(to, map[string]string{"address": a})
	}
	payload := map[string]any{
		"senderAddress": s.SenderAddress,
		"recipients":    map[string]any{"to": to},
		"content": map[string]string{
			"subject":   m.Subject,
			"plainText": m.Body,
		},
	}
	if m.ReplyTo != "" {
		payload["replyTo"] = []map[string]string{{"address": m.ReplyTo}}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	u := s.Endpoint + "/emails:send?api-version=2023-03-31"
	req, err := http.NewRequest(http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := s.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("acs send failed: %s: %s", resp.Status, respBody)
	}
	return nil
}
