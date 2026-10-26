package contact

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
)

const turnstileVerifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

// TurnstileVerifier は Cloudflare Turnstile のトークンを検証する Verifier。
// シークレットはサイト設定の turnstile_secret_env が指す環境変数から読む。
type TurnstileVerifier struct {
	Getenv func(string) string // 無ければ os.Getenv
	URL    string              // 無ければ Cloudflare の siteverify
	Client *http.Client        // 無ければ http.DefaultClient
}

// 検証に失敗した（= bot と判定した）場合のみ false。シークレット（環境変数 secretEnv）
// が未設定なら検証をスキップし、通信エラー等はエラーとして返す（呼び出し側でリトライ）。
func (v TurnstileVerifier) Verify(site *Site, token string) (bool, error) {
	getenv := v.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	secret := ""
	if site.TurnstileSecretEnv != "" {
		secret = getenv(site.TurnstileSecretEnv)
	}
	if secret == "" {
		return true, nil
	}
	if token == "" {
		return false, nil
	}
	endpoint := v.URL
	if endpoint == "" {
		endpoint = turnstileVerifyURL
	}
	client := v.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.PostForm(endpoint, url.Values{"secret": {secret}, "response": {token}})
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	var result struct {
		Success bool `json:"success"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, err
	}
	return result.Success, nil
}
