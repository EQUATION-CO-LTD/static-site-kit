package sitegen

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/EQUATION-CO-LTD/static-site-kit/sitegen/pyfmt"
)

// Site は読み込み済みのサイト（設定・中身・テンプレート）と、組み立て中のページ。Builder と Layout に渡される。
//
// サイトのディレクトリの約束（名前は固定）:
//
//	site.json    設定（順序を保って持つ）。キットが読むキーは Config のコメントを参照
//	layout.html  全ページ共通の外枠。{{TOKEN}} の値は Layout が返す
//	pages/       固定ページ。先頭のコメントにメタ情報。置き場所がそのまま URL（pages/a/b.html → a/b）
//	tpl/         動的ページと部品のテンプレート
//	static/      そのまま配るファイル
//	redirects.json  旧 URL → 新 URL（301）
type Site struct {
	opts    Options
	dir     string
	out     string
	stdout  io.Writer
	conf    *pyfmt.Obj
	base    string // site.json の site（末尾 / つき）
	slash   string // trailing_slash が真なら "/"
	suffix  string
	today   string
	staging bool
	layout  string

	pages     map[string]*page
	frag      map[string]string
	redirects map[string]string
}

type page struct {
	url   string
	meta  *Meta
	main  string
	fixed bool // pages/ の固定ページ
}

func newSite(o Options) *Site {
	s := &Site{opts: o, dir: o.Dir, out: o.Out, stdout: o.Stdout, pages: map[string]*page{}, frag: map[string]string{}, redirects: map[string]string{}}
	if s.dir == "" {
		s.dir = "."
	}
	if s.out == "" {
		s.out = "dist"
	}
	if s.stdout == nil {
		s.stdout = os.Stdout
	}
	conf, ok := pyfmt.LoadJSONFile(s.path("site.json"))
	if !ok {
		Die("site.json がない（サイトのディレクトリ %s を確かめる）", s.dir)
	}
	s.conf = pyfmt.AsObj(conf)
	s.base = pyfmt.Str(s.conf.At("site"))
	if pyfmt.Truthy(s.conf.Get("trailing_slash")) {
		s.slash = "/"
	}
	s.suffix = pyfmt.Str(pyfmt.Getd(s.conf, "title_suffix", ""))
	s.today = o.Today
	if s.today == "" {
		s.today = time.Now().In(JST).Format("2006-01-02") // 日付で出し分ける物の判定に使う日付（JST）
	}
	s.staging = pyfmt.Truthy(s.conf.Get("staging"))
	if o.Staging != nil {
		s.staging = *o.Staging
	}
	s.layout = pyfmt.ReadText(s.path("layout.html"))
	if v, ok := pyfmt.LoadJSONFile(s.path("redirects.json")); ok {
		ro := pyfmt.AsObj(v)
		for _, k := range ro.Keys() {
			s.redirects[k] = pyfmt.Str(ro.Get(k))
		}
	}
	return s
}

func (s *Site) path(rel string) string { return filepath.Join(s.dir, filepath.FromSlash(rel)) }

// ---------------------------------------------------------------- 設定・ファイル

// Config は site.json。キットが読むキーは次のとおり（他のキーは Builder や Layout が自由に使う）
//
//	site            サイトの URL（末尾 /）。必須
//	company         会社名。og:site_name と構造化データに使う
//	og_image        og:image の既定の画像（サイト内のパス）
//	logo            構造化データの Organization の logo（サイト内のパス）
//	title_suffix    ページの title の末尾に付く文字列（構造化データの name から除く）
//	trailing_slash  真ならページの URL を /x/ の形にする（偽なら /x）
//	staging         真なら検証環境向けの SWA 設定にする（Options.Staging が優先）
//	swa             staticwebapp.config.json にそのまま重ねるキー
//	link_check_skip / link_check_known  check パッケージが読む
func (s *Site) Config() *pyfmt.Obj { return s.conf }

// BaseURL は site.json の site（サイトの URL、末尾 /）
func (s *Site) BaseURL() string { return s.base }

// TitleSuffix は site.json の title_suffix
func (s *Site) TitleSuffix() string { return s.suffix }

// Today は基準日（YYYY-MM-DD）。Options.Today、無ければ実行時の日本時間の日付
func (s *Site) Today() string { return s.today }

// Staging は検証環境向けにするか（Options.Staging、無ければ site.json の staging）
func (s *Site) Staging() bool { return s.staging }

// Read はサイトのディレクトリ内のテキストファイル（/ 区切りの相対パス）。改行は \n にそろう。無ければ止まる
func (s *Site) Read(rel string) string { return pyfmt.ReadText(s.path(rel)) }

// Exists はサイトのディレクトリ内にファイルかディレクトリがあるか
func (s *Site) Exists(rel string) bool { return pyfmt.FileExists(s.path(rel)) }

// LoadJSON はサイトのディレクトリ内の JSON ファイル。無ければ ok=false
func (s *Site) LoadJSON(rel string) (any, bool) { return pyfmt.LoadJSONFile(s.path(rel)) }

// ListDir はサイトのディレクトリ内のディレクトリにあるものの名前（名前順）
func (s *Site) ListDir(rel string) []string { return pyfmt.ListDirSorted(s.path(rel)) }

// Tpl は tpl/{name}.html
func (s *Site) Tpl(name string) string { return s.Read(fmt.Sprintf("tpl/%s.html", name)) }

// Content は microCMS などから取得した中身 {name}.json。Options.ContentDirs の順（既定は cms → seed）に探して最初の物を返す。
// {"contents": [...]} の形ならその配列、そうでなければ中身そのまま。無ければ ok=false。
// 呼ぶたびに読み直す（呼び出し側が書き換えても他に影響しない）
func (s *Site) Content(name string) (data any, ok bool) {
	for _, d := range s.contentDirs() {
		v, _ := pyfmt.LoadJSONFile(fmt.Sprintf("%s/%s/%s.json", s.contentRoot(), d, name))
		if v != nil {
			if o, isObj := v.(*pyfmt.Obj); isObj && o.Has("contents") {
				return o.At("contents"), true
			}
			return v, true
		}
	}
	return nil, false
}

func (s *Site) contentRoot() string {
	if s.opts.Content != "" {
		return s.opts.Content
	}
	return filepath.ToSlash(filepath.Join(s.dir, "content"))
}

func (s *Site) contentDirs() []string {
	if len(s.opts.ContentDirs) > 0 {
		return s.opts.ContentDirs
	}
	return []string{"cms", "seed"}
}

// ---------------------------------------------------------------- URL

// Href はサイト内の URL（先頭の / なし、トップは ""）→ リンク先（/x または /x/、トップは /）
func (s *Site) Href(url string) string {
	if url != "" {
		return "/" + url + s.slash
	}
	return "/"
}

// PageURL はサイト内の URL → 絶対 URL
func (s *Site) PageURL(url string) string { return s.base + s.Href(url)[1:] }

// AbsURL はサイト内のパス（/images/…）→ 絶対 URL。og:image と構造化データは絶対 URL でないと読まれない
func (s *Site) AbsURL(path string) string {
	if strings.HasPrefix(path, "http") {
		return path
	}
	return s.base + pyfmt.Quote(strings.TrimLeft(path, "/"), "/?=&%")
}

// ---------------------------------------------------------------- 部品とページ

// SetFragment は全ページの本文に差し込む部品（{{NAME}}）を登録する。固定ページも動的ページも、書き出す直前に差し込む
func (s *Site) SetFragment(name, html string) { s.frag[name] = html }

// Fragment は登録済みの部品（無ければ空）
func (s *Site) Fragment(name string) string { return s.frag[name] }

// FillFragments は本文 main の {{NAME}} を登録済みの部品で差し込む。値の無いトークンは Die で止める
func (s *Site) FillFragments(main, name string) string { return Fill(main, s.frag, name) }

// AddPage はページを 1 枚足す。url はサイト内の URL（先頭の / なし、トップは ""、404 は "404"）。
// main は本文（Layout の MAIN になる。{{NAME}} は部品で差し込まれる）。URL が重複したら Die で止める
func (s *Site) AddPage(url string, m *Meta, main string) { s.add(url, m, main, false) }

func (s *Site) add(url string, m *Meta, main string, fixed bool) {
	if _, dup := s.pages[url]; dup {
		Die("URL が重複: %s", s.Href(url))
	}
	s.pages[url] = &page{url: url, meta: m, main: main, fixed: fixed}
}

// HasPage は URL のページが既にあるか
func (s *Site) HasPage(url string) bool { _, ok := s.pages[url]; return ok }

// PageURLs は足したページの URL を名前順に返す
func (s *Site) PageURLs() []string {
	urls := make([]string, 0, len(s.pages))
	for u := range s.pages {
		urls = append(urls, u)
	}
	sort.Strings(urls)
	return urls
}

// AddRedirect は転送（301）を足す。redirects.json に同じ転送元が既にあればそちらを優先する（上書きしない）
func (s *Site) AddRedirect(from, to string) {
	if _, ok := s.redirects[from]; !ok {
		s.redirects[from] = to
	}
}

// urlOf は固定ページのパス（pages/a/b.html）→ サイト内の URL（a/b）。index は親の URL になる
func urlOf(path string) string {
	u := pyfmt.Slice(path, len("pages/"), pyfmt.RuneLen(path)-len(".html"))
	if u == "index" {
		return ""
	}
	return strings.TrimSuffix(u, "/index")
}

// loadFixedPages は pages/ の固定ページをすべて足す
func (s *Site) loadFixedPages() {
	if !pyfmt.IsDir(s.path("pages")) {
		return
	}
	pyfmt.WalkFiles(s.path("pages"), func(rel string) {
		path := "pages/" + rel
		meta, main := ParsePageSourceBlocks(path, s.Read(path), s.opts.PageBlocks)
		s.add(urlOf(path), meta, main, true)
	})
}

// ContentIn は Content と同じ読み方で、中身のディレクトリ（Options.Content）の下の dir/{name}.json を 1 か所だけ読む。
// dir が "." なら中身のディレクトリそのもの。無ければ ok=false
func (s *Site) ContentIn(dir, name string) (data any, ok bool) {
	p := fmt.Sprintf("%s/%s/%s.json", s.contentRoot(), dir, name)
	if dir == "." {
		p = fmt.Sprintf("%s/%s.json", s.contentRoot(), name)
	}
	v, _ := pyfmt.LoadJSONFile(p)
	if v == nil {
		return nil, false
	}
	if o, isObj := v.(*pyfmt.Obj); isObj && o.Has("contents") {
		return o.At("contents"), true
	}
	return v, true
}
