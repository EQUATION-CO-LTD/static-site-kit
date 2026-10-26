package sitegen

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/EQUATION-CO-LTD/static-site-kit/sitegen/pyfmt"
)

// Crumb はパンくずの 1 段（名前とサイト内のパス）
type Crumb struct{ Name, URL string }

// Meta はページのメタ情報。SEO の部品が読む標準の項目と、サイト固有のキーを持つ Extra からなる。
// 文字列の項目の空は「無い」と同じに扱う（Description が空なら description タグを出さない、など）
type Meta struct {
	Title       string // <title> にそのまま入る（HTML のエスケープは呼び出し側）
	Description string
	Image       string // og:image。空ならサイト既定（site.json の og_image）
	OGType      string // 空なら website
	Crumbs      []Crumb
	// HasCrumbs が真のとき、Crumbs が空でもパンくず（HOME → このページ）を構造化データに出す
	HasCrumbs bool
	LD        []*pyfmt.Obj      // ページ固有の構造化データ（記事など）。mainEntityOfPage が付く
	Extra     map[string]string // サイト固有のキー（body_id など）。キットは読まない
}

// 標準の項目に対応するキー（ページ先頭のコメントのキーと同じ）
const (
	KeyTitle       = "title"
	KeyDescription = "description"
	KeyImage       = "image"
	KeyOGType      = "og_type"
)

// NewMeta はキーと値を交互に並べて Meta を作る。title・description・image・og_type は標準の項目に、他は Extra に入る
func NewMeta(kv ...string) *Meta {
	m := &Meta{Extra: map[string]string{}}
	for i := 0; i+1 < len(kv); i += 2 {
		m.Set(kv[i], kv[i+1])
	}
	return m
}

// Set はキーに値を入れる。標準のキーは標準の項目に、他は Extra に入る
func (m *Meta) Set(k, v string) *Meta {
	switch k {
	case KeyTitle:
		m.Title = v
	case KeyDescription:
		m.Description = v
	case KeyImage:
		m.Image = v
	case KeyOGType:
		m.OGType = v
	default:
		if m.Extra == nil {
			m.Extra = map[string]string{}
		}
		m.Extra[k] = v
	}
	return m
}

// Lookup はキーの値と、あるか（標準の項目は空なら無い、Extra は入っていれば有る）
func (m *Meta) Lookup(k string) (string, bool) {
	var v string
	switch k {
	case KeyTitle:
		v = m.Title
	case KeyDescription:
		v = m.Description
	case KeyImage:
		v = m.Image
	case KeyOGType:
		v = m.OGType
	default:
		v, ok := m.Extra[k]
		return v, ok
	}
	return v, v != ""
}

// Get はキーの値（無ければ空）。標準の項目も Extra も同じ引き方
func (m *Meta) Get(k string) string {
	v, _ := m.Lookup(k)
	return v
}

// WithCrumbs はパンくずを足す（引数なしでも HasCrumbs が真になる）
func (m *Meta) WithCrumbs(c ...Crumb) *Meta {
	m.HasCrumbs = true
	m.Crumbs = c
	return m
}

// WithLD はページ固有の構造化データを足す
func (m *Meta) WithLD(n ...*pyfmt.Obj) *Meta {
	m.LD = n
	return m
}

var metaBlockRe = regexp.MustCompile(`(?s)^<!--\n(.*?)-->\n`)

// ParsePageSource は固定ページの原稿を、先頭のコメントのメタ情報（1 行 1 つ `キー: 値`）と本文に分ける。
// path は誤りのメッセージに出すだけ
func ParsePageSource(path, src string) (*Meta, string) { return ParsePageSourceBlocks(path, src, nil) }

// ParsePageSourceBlocks は ParsePageSource に加えて、メタ情報の直後に続く囲みブロック
// （`<!-- 名前 -->` から `<!-- /名前 -->` まで。どちらも行末の改行まで）を blocks の順に読む。
// 読んだ中身は、名前の - を _ にしたキー（head-meta なら head_meta）の Meta.Extra に入り、本文から除かれる。
// ブロックは書かなくてよく、書くなら blocks の順で隙間なく続ける（順が違うブロックは本文として残る）
func ParsePageSourceBlocks(path, src string, blocks []string) (*Meta, string) {
	loc := metaBlockRe.FindStringSubmatchIndex(src)
	if loc == nil {
		Die("%s: 先頭にメタ情報のコメントがない", path)
	}
	meta := NewMeta()
	hasTitle := false
	for _, line := range pyfmt.SplitLines(src[loc[2]:loc[3]]) {
		k, v, ok := strings.Cut(line, ": ")
		if !ok {
			panic(fmt.Sprintf("ValueError: メタ情報の行に ': ' がない: %s", pyfmt.Repr(line)))
		}
		meta.Set(k, v)
		hasTitle = hasTitle || k == KeyTitle
	}
	if !hasTitle { // 空の title は許すが、書き忘れは止める
		Die("%s: メタ情報に title がない", path)
	}
	main := src[loc[1]:]
	for _, name := range blocks {
		q := regexp.QuoteMeta(name)
		re := regexp.MustCompile(`(?s)^<!-- ` + q + ` -->\n(.*?)<!-- /` + q + ` -->\n`)
		if b := re.FindStringSubmatchIndex(main); b != nil {
			meta.Set(strings.ReplaceAll(name, "-", "_"), main[b[2]:b[3]])
			main = main[b[1]:]
		}
	}
	return meta, main
}
