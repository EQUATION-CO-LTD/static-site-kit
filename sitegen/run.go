// Package sitegen は、静的サイトを作る共通の道具。サイトのディレクトリ（site.json・layout.html・pages/・tpl/・static/ など）と
// 中身（content/）から、公開用のディレクトリ（HTML・sitemap.xml・Azure Static Web Apps の設定）を作る。
// 同じ入力からは、いつ・どこで作っても同じバイト列になる。
//
// 流れは Run のとおり。サイト固有のページの組み立ては Builder、外枠（layout.html）の {{TOKEN}} の値は Layout を、
// サイト側のコードで差し込む。SEO の部品（canonical・OGP・JSON-LD）は Site のメソッドとして出し、使うかは Layout が決める。
//
// 想定外の入力（足りないキー・型の違い）や設定の誤りは panic で止め、入口の Run が error にして返す。
// 担当者が直せる誤りは Die で止める（*UserError になる）。
package sitegen

import (
	"io"
	"sort"
)

// Options は Run の設定
type Options struct {
	// Dir はサイトのディレクトリ（既定 "."）。site.json・layout.html・pages/・tpl/・static/・redirects.json がある所
	Dir string
	// Content は中身のディレクトリ（その下に Options.ContentDirs の各ディレクトリ）。既定は Dir/content
	Content string
	// ContentDirs は Site.Content が探すディレクトリを優先順に並べた物。既定は cms（取得した物）→ seed（初期の物）
	ContentDirs []string
	// Out は出力先（既定 "dist"）。作る前に消す
	Out string
	// SkipStatic が真なら static/ を写さない（検証用。大きな静的ファイルを毎回写さない）
	SkipStatic bool
	// Today は基準日（YYYY-MM-DD）。日付で出し分ける物のテスト用。空なら実行時の日本時間の日付
	Today string
	// Staging が非 nil なら site.json の staging の代わりに使う（検証環境と本番で同じ site.json を使えるように）
	Staging *bool
	// Builders は固定ページを足した後に、順に実行するサイト固有のページの組み立て
	Builders []Builder
	// Layout は外枠（layout.html）の値を返す。nil なら MAIN だけ
	Layout Layout
	// FixedPageFilter が非 nil なら、すべてのページが揃った後、固定ページの本文を差し込みの前に加工する
	// （他のページの有無でリンクを外す、など）
	FixedPageFilter func(s *Site, url, main string) string
	// SWAConfig が真なら staticwebapp.config.json を作る（転送・staging の閲覧制限と noindex・20KB の上限検査）
	SWAConfig bool
	// PruneUploads が空でなければ、出力のこのディレクトリ（例 "wp/wp-content/uploads"）の下の画像のうち、
	// どのページ・CSS・JS・転送先からも参照されていない物を消す（SkipStatic のときは何もしない）
	PruneUploads string
	// Stdout は進捗の出力先（既定は標準出力）
	Stdout io.Writer
}

// Builder はサイト固有のページを足す。Site の中身を読み、AddPage で足し、SetFragment で部品を登録する
type Builder interface {
	Build(s *Site) error
}

// BuilderFunc は関数を Builder にする
type BuilderFunc func(s *Site) error

// Build は f(s)
func (f BuilderFunc) Build(s *Site) error { return f(s) }

// Layout は外枠（layout.html）の {{TOKEN}} の値をサイトが返す差し込み口。
// url はサイト内の URL、m はページのメタ情報、main は部品を差し込んだ後の本文。
// 返す値に MAIN が無ければ、キットが main を入れる
type Layout interface {
	Values(s *Site, url string, m *Meta, main string) (map[string]string, error)
}

// LayoutFunc は関数を Layout にする
type LayoutFunc func(s *Site, url string, m *Meta, main string) (map[string]string, error)

// Values は f(s, url, m, main)
func (f LayoutFunc) Values(s *Site, url string, m *Meta, main string) (map[string]string, error) {
	return f(s, url, m, main)
}

// Run はサイトを作る。読み込み → 固定ページ（pages/）→ Builder を順に実行 → 外枠に差し込み → 書き出し。
// 書き出しは、すべてのページの組み立てが成功してから始める（失敗したときに前の出力を消さない）。
// 誤りは error で返す（担当者が直せる物は *UserError、想定外の物は *InternalError）
func Run(o Options) (err error) {
	defer Recover(&err)
	s := newSite(o)
	s.loadFixedPages()
	for _, b := range o.Builders {
		if err := b.Build(s); err != nil {
			return err
		}
	}
	html, err := s.render()
	if err != nil {
		return err
	}
	s.emit(html)
	return nil
}

// render は全ページを外枠に差し込んで HTML にする（URL → HTML）
func (s *Site) render() (map[string]string, error) {
	urls := make([]string, 0, len(s.pages))
	for u := range s.pages {
		urls = append(urls, u)
	}
	sort.Strings(urls) // 誤りの出る順を安定させる
	out := make(map[string]string, len(urls))
	for _, u := range urls {
		p := s.pages[u]
		main := p.main
		if p.fixed && s.opts.FixedPageFilter != nil {
			main = s.opts.FixedPageFilter(s, u, main)
		}
		main = s.FillFragments(main, "/"+u)
		var values map[string]string
		if s.opts.Layout != nil {
			v, err := s.opts.Layout.Values(s, u, p.meta, main)
			if err != nil {
				return nil, err
			}
			values = v
		}
		if values == nil {
			values = map[string]string{}
		}
		if _, ok := values["MAIN"]; !ok {
			values["MAIN"] = main
		}
		out[u] = Fill(s.layout, values, "layout.html")
	}
	return out, nil
}
