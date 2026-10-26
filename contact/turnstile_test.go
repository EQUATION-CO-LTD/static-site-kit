package contact

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// シークレットが未設定なら検証をスキップする。設定済みなら Cloudflare の判定に従う。
func TestTurnstileVerifier(t *testing.T) {
	var gotSecret, gotToken string
	calls := 0
	respond := `{"success":true}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = r.ParseForm()
		gotSecret, gotToken = r.PostForm.Get("secret"), r.PostForm.Get("response")
		_, _ = w.Write([]byte(respond))
	}))
	defer srv.Close()

	env := map[string]string{"SECRET_A": "s3cret"}
	v := TurnstileVerifier{Getenv: func(k string) string { return env[k] }, URL: srv.URL}
	site := func(secretEnv string) *Site { return &Site{ID: "x", TurnstileSecretEnv: secretEnv} }

	// 環境変数名が空・環境変数が未設定: スキップして通す（通信しない）
	for _, name := range []string{"", "SECRET_UNSET"} {
		if ok, err := v.Verify(site(name), ""); !ok || err != nil {
			t.Errorf("%q: スキップのはず: ok=%v err=%v", name, ok, err)
		}
	}
	if calls != 0 {
		t.Fatalf("スキップなのに通信した: %d", calls)
	}

	// シークレットありでトークンが空: ボット扱い（通信しない）
	if ok, err := v.Verify(site("SECRET_A"), ""); ok || err != nil || calls != 0 {
		t.Errorf("トークン空: ok=%v err=%v calls=%d", ok, err, calls)
	}

	if ok, err := v.Verify(site("SECRET_A"), "tok"); !ok || err != nil {
		t.Errorf("成功のはず: ok=%v err=%v", ok, err)
	}
	if gotSecret != "s3cret" || gotToken != "tok" {
		t.Errorf("送った値 secret=%q response=%q", gotSecret, gotToken)
	}

	respond = `{"success":false}`
	if ok, err := v.Verify(site("SECRET_A"), "tok"); ok || err != nil {
		t.Errorf("失敗のはず: ok=%v err=%v", ok, err)
	}

	// 応答が読めない・通信できないときはエラー（呼び出し側でリトライ）
	respond = `<html>`
	if _, err := v.Verify(site("SECRET_A"), "tok"); err == nil {
		t.Error("壊れた応答はエラー")
	}
	srv.Close()
	if _, err := v.Verify(site("SECRET_A"), "tok"); err == nil {
		t.Error("通信できなければエラー")
	}
}
