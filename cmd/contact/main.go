// 静的サイトの問い合わせキュー（contact-requests）を処理し、ACS Email でメール送信する
// Azure Functions カスタムハンドラー。
//
// サイトごとの宛先・件名・本文の文言・項目の並びは、実行ファイルの隣の sites/*.json
// （環境変数 CONTACT_SITES_DIR で変更可）で決まる。設定が不正なら起動しない。
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/EQUATION-CO-LTD/static-site-kit/contact"
	"github.com/EQUATION-CO-LTD/static-site-kit/contact/acs"
)

// ---- Functions カスタムハンドラーの呼び出し規約 ----

type invokeRequest struct {
	Data     map[string]json.RawMessage `json:"Data"`
	Metadata map[string]json.RawMessage `json:"Metadata"`
}

type invokeResponse struct {
	Outputs     map[string]any `json:"Outputs"`
	Logs        []string       `json:"Logs"`
	ReturnValue any            `json:"ReturnValue"`
}

// Data.msg は本文が JSON 文字列として包まれて渡る。包みの枚数が環境で変わり
// 一枚では剥がしきれないことがあったため、JSON オブジェクトに届くまで剥がす。
func unwrap(raw json.RawMessage) ([]byte, error) {
	s := strings.TrimSpace(string(raw))
	for range 4 {
		if strings.HasPrefix(s, "{") {
			return []byte(s), nil
		}
		var inner string
		if err := json.Unmarshal([]byte(s), &inner); err != nil {
			return nil, err
		}
		s = strings.TrimSpace(inner)
	}
	return nil, fmt.Errorf("入れ子が深すぎる")
}

// ---- QueueTrigger 本体 ----

func processContact(proc *contact.Processor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var invoke invokeRequest
		if err := json.NewDecoder(r.Body).Decode(&invoke); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		body, err := unwrap(invoke.Data["msg"])
		if err != nil {
			// 破棄はエラーではなく正常完了として返す（リトライさせない）
			writeInvokeResponse(w, []string{"discarded: unwrap failed: " + err.Error()})
			return
		}

		// Turnstile トークンは 1 回検証すると使用済みになるため、
		// リトライ（DequeueCount > 1）では再検証せずスキップする
		dequeueCount := "1"
		if raw, ok := invoke.Metadata["DequeueCount"]; ok {
			_ = json.Unmarshal(raw, &dequeueCount)
		}

		logs, err := proc.Process(body, dequeueCount == "1")
		if err != nil {
			// 検証の通信エラーと通知メールの送信失敗は 500 で返し、キューに再送させる
			msg := "send mail error"
			if errors.Is(err, contact.ErrVerify) {
				msg = "turnstile verify error"
			}
			http.Error(w, msg, http.StatusInternalServerError)
			return
		}
		writeInvokeResponse(w, logs)
	}
}

func writeInvokeResponse(w http.ResponseWriter, logs []string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(invokeResponse{
		Outputs: map[string]any{},
		Logs:    logs,
	})
}

// newMux は /ProcessContact だけを受ける。
func newMux(proc *contact.Processor) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/ProcessContact", processContact(proc))
	return mux
}

// sitesDir はサイト設定のディレクトリ。環境変数 CONTACT_SITES_DIR、無ければ実行ファイルの隣の sites/。
func sitesDir() (string, error) {
	if dir := os.Getenv("CONTACT_SITES_DIR"); dir != "" {
		return dir, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(exe), "sites"), nil
}

func main() {
	dir, err := sitesDir()
	if err != nil {
		log.Fatalf("サイト設定の場所が決められない: %v", err)
	}
	sites, err := contact.LoadSites(os.DirFS(dir))
	if err != nil {
		log.Fatalf("サイト設定が不正: %v", err)
	}
	proc := &contact.Processor{
		Sites:    sites,
		Sender:   acs.FromEnv(),
		Verifier: contact.TurnstileVerifier{},
		Logf:     log.Printf,
	}

	port := os.Getenv("FUNCTIONS_CUSTOMHANDLER_PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("custom handler listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, newMux(proc)))
}
