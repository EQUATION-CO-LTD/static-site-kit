// Package contact は、静的サイト（ホームページ）の問い合わせフォームから届いた 1 件を
// 検証し、担当者宛の通知メールと問い合わせ者宛の自動返信を組み立てて送る。
//
// サイトごとの宛先・件名・本文の文言・項目の並びはサイト設定（JSON）で決まり、
// コードはサイトに依存しない。メール送信とボット対策の検証は差し替え口（Sender・Verifier）
// にしてあり、HTTP や Azure Functions には依存しない。外部モジュール依存なし（標準ライブラリのみ）。
package contact

import (
	"errors"
	"fmt"
	"strings"
)

// Sender はメールを送る。送れた（受け付けられた）ときだけ nil を返す。
type Sender interface {
	Send(Mail) error
}

// Verifier はボット対策（Cloudflare Turnstile など）のトークンを検証する。
// ボットと判定したときだけ false。通信エラー等はエラーとして返す（呼び出し側でリトライ）。
type Verifier interface {
	Verify(site *Site, token string) (bool, error)
}

// 再送させるべき失敗。Process が返すエラーは必ずどちらかを包む。
var (
	ErrVerify = errors.New("turnstile verify error")
	ErrSend   = errors.New("send mail error")
)

// Processor は問い合わせ 1 件の処理に必要なものをまとめる。
type Processor struct {
	Sites    map[string]*Site
	Sender   Sender
	Verifier Verifier
	Logf     func(format string, args ...any) // 任意。サーバーログ向け（nil なら出さない）
}

func (p *Processor) logf(format string, args ...any) {
	if p.Logf != nil {
		p.Logf(format, args...)
	}
}

// Process は問い合わせ 1 件（キューメッセージの本文）を処理し、実行ログを返す。
// 破棄はエラーではなく正常完了として返す（リトライさせない）。エラーを返すのは
// 検証の通信エラー（ErrVerify）と通知メールの送信失敗（ErrSend）で、呼び出し側はこれを再送にする。
//
// Turnstile トークンは 1 回検証すると使用済みになるため、リトライ（firstAttempt が false）
// では再検証しない。
func (p *Processor) Process(body []byte, firstAttempt bool) ([]string, error) {
	logs := []string{}

	pr, reason := Prepare(p.Sites, body)
	if pr == nil {
		return append(logs, "discarded: "+reason), nil
	}

	if firstAttempt {
		ok, err := p.Verifier.Verify(pr.Site, pr.Token)
		if err != nil {
			p.logf("turnstile verify error: %v", err)
			return logs, fmt.Errorf("%w: %w", ErrVerify, err)
		}
		if !ok {
			return append(logs, "discarded: turnstile verification failed"), nil
		}
	}

	if err := p.Sender.Send(pr.Notice); err != nil {
		p.logf("send notice mail error: %v", err)
		return logs, fmt.Errorf("%w: %w", ErrSend, err)
	}
	logs = append(logs, fmt.Sprintf("notice mail sent (site=%s form=%s target=%s)", pr.Site.ID, pr.FormID, pr.TargetKey))

	// 自動返信の失敗ではリトライさせない。ここでエラーを返すと担当者宛の
	// 通知メールまで再送され、同じ問い合わせが二重に届いてしまうため。
	if pr.Reply != nil {
		if err := p.Sender.Send(*pr.Reply); err != nil {
			p.logf("send auto-reply error: %v", err)
			logs = append(logs, "auto-reply failed: "+err.Error())
		} else {
			logs = append(logs, "auto-reply sent to inquirer")
		}
	}
	return logs, nil
}

// AutoReplyable は自動返信を送ってよい宛先か。フォームの入力値をそのまま宛先にするため、
// 明らかに不正なアドレスへ送って ACS の到達不能扱いを積み上げないようにする。
func AutoReplyable(addr string) bool {
	if strings.ContainsAny(addr, " \t\r\n,;<>\"") {
		return false
	}
	at := strings.IndexByte(addr, '@')
	if at <= 0 || at == len(addr)-1 {
		return false
	}
	domain := addr[at+1:]
	return !strings.ContainsRune(domain, '@') && strings.Contains(domain, ".")
}
