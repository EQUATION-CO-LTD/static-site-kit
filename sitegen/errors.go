package sitegen

import (
	"fmt"
	"runtime/debug"
)

// UserError は、サイトを触る人が直せる言葉で止める誤り（Python の SystemExit(メッセージ) と同じ位置づけ）。
// Msg をそのまま利用者に見せてよい
type UserError struct{ Msg string }

// Error は Msg
func (e *UserError) Error() string { return e.Msg }

// Die は UserError で止める（panic する）。Builder や部品の中から呼ぶ。Run が受けて error として返す
func Die(format string, a ...any) {
	panic(&UserError{fmt.Sprintf(format, a...)})
}

// InternalError は想定外の panic（型の不一致・キーの欠落・通信の失敗など）。Value が元の値、Stack が発生時のスタック
type InternalError struct {
	Value any
	Stack []byte
}

// Error は「エラー: 」に元の値を続けた文字列
func (e *InternalError) Error() string { return "エラー: " + fmt.Sprint(e.Value) }

// Unwrap は元の値が error ならそれを返す（errors.As で通信の誤りなどを取り出せる）
func (e *InternalError) Unwrap() error {
	err, _ := e.Value.(error)
	return err
}

// Recover は panic を error にする。`defer sitegen.Recover(&err)` の形で、入口の関数の先頭に置く。
// UserError はそのまま、他は InternalError にして返す
func Recover(errp *error) {
	r := recover()
	if r == nil {
		return
	}
	if ue, ok := r.(*UserError); ok {
		*errp = ue
		return
	}
	*errp = &InternalError{Value: r, Stack: debug.Stack()}
}
