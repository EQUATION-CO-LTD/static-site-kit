package sitegen

// どのサイトの Builder でも使う小さな部品（差し込み・改行・ページ送り・画像の URL・日付・本文の要約）。

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/EQUATION-CO-LTD/static-site-kit/sitegen/pyfmt"
)

var tokenRe = regexp.MustCompile(`\{\{([` + pyfmt.Word + `:/-]+)\}\}`)

// Fill は tpl の {{TOKEN}} を values で差し込む。値の無いトークンが残っていたら Die で止める（name は誤りに出す名前）。
// 差し込んだ値は読み直さない（値の中の {{...}} はそのまま残る）
func Fill(tpl string, values map[string]string, name string) string {
	var sb strings.Builder
	last := 0
	for _, m := range tokenRe.FindAllStringSubmatchIndex(tpl, -1) {
		sb.WriteString(tpl[last:m[0]])
		tok := tpl[m[2]:m[3]]
		v, ok := values[tok]
		if !ok {
			Die("%s: {{%s}} に値がない", name, tok)
		}
		sb.WriteString(v)
		last = m[1]
	}
	sb.WriteString(tpl[last:])
	return sb.String()
}

// Br はテキストエリアの改行をそのまま表示に反映する（エスケープして、改行を <br /> にする）
func Br(v any) string {
	return strings.ReplaceAll(strings.ReplaceAll(pyfmt.Esc(v), "\r\n", "\n"), "\n", "<br />\n")
}

// PageChunk はページ送りの 1 ページ分（URL とそのページに載せる項目）
type PageChunk[T any] struct {
	URL   string
	Items []T
}

// Paginate は items を perPage 件ずつに分け、ページごとの URL と項目を返す。2 ページ目以降の URL は {base}/page/N。
// items が空でも 1 ページは返す
func Paginate[T any](base string, items []T, perPage int) []PageChunk[T] {
	var chunks [][]T
	for i := 0; i < len(items); i += perPage {
		chunks = append(chunks, items[i:min(i+perPage, len(items))])
	}
	if len(chunks) == 0 {
		chunks = [][]T{nil}
	}
	out := make([]PageChunk[T], len(chunks))
	for i, c := range chunks {
		u := base
		if i > 0 {
			u = fmt.Sprintf("%s/page/%d", base, i+1)
		}
		out[i] = PageChunk[T]{u, c}
	}
	return out
}

// ImgURL は microCMS の画像フィールド → URL。microCMS の画像配信なら size 四方に切り出した小さい版を使う（size 0 は指定なし）。
// 画像が無ければ空
func ImgURL(image any, size int) string {
	if !pyfmt.Truthy(image) {
		return ""
	}
	u := pyfmt.Str(pyfmt.AsObj(image).At("url"))
	if size > 0 && strings.HasPrefix(u, "https://images.microcms-assets.io") {
		return fmt.Sprintf("%s?w=%d&h=%d&fit=crop", u, size*2, size*2)
	}
	return u
}

// Pick はセレクトフィールド（文字列の配列で返る）の先頭。配列でなければ文字列として使う
func Pick(v any) string {
	if l, ok := v.([]any); ok && len(l) > 0 {
		return pyfmt.Str(l[0])
	}
	return pyfmt.Str(pyfmt.Or(v, ""))
}

// JDate は microCMS の日付（2026-09-25T00:00:00.000Z）の日付部分を sep でつないだ文字列（時刻は使わない）。
// JDate("2026-09-25T00:00:00.000Z", ".") は "2026.09.25"
func JDate(iso, sep string) string {
	return pyfmt.Head(iso, 4) + sep + pyfmt.Slice(iso, 5, 7) + sep + pyfmt.Slice(iso, 8, 10)
}

// JST は日本標準時
var JST = time.FixedZone("JST", 9*3600)

// JSTDotDate は UTC の日時（2026-09-24T15:00:00.000Z）を日本時間の日付にして "2026.09.25" の形にする
func JSTDotDate(iso string) string {
	t, err := time.ParseInLocation("2006-01-02T15:04:05", pyfmt.Head(iso, 19), time.UTC)
	if err != nil {
		panic("ValueError: " + err.Error())
	}
	return t.In(JST).Format("2006.01.02")
}

var (
	tagRe   = regexp.MustCompile(`<[^>]+>`)
	spaceRe = regexp.MustCompile(`[` + pyfmt.Space + `]+`)
)

// Plain は本文の HTML から説明文に使う文字列を作る（タグを外し、空白をまとめ、n 文字まで）
func Plain(htmlText any, n int) string {
	t := tagRe.ReplaceAllString(pyfmt.Str(htmlText), " ")
	return pyfmt.Head(pyfmt.Strip(spaceRe.ReplaceAllString(pyfmt.HTMLUnescape(t), " ")), n)
}
