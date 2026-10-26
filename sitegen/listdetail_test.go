package sitegen_test

// ListDetail・囲みブロック・画像の差し替えの単体テスト。データはすべて架空。

import (
	"fmt"
	"strings"
	"testing"

	"github.com/EQUATION-CO-LTD/static-site-kit/sitegen"
	"github.com/EQUATION-CO-LTD/static-site-kit/sitegen/pyfmt"
)

const postsConf = `{"site": "https://sample.example/", "company": "サンプル", "description": "サイトの説明", "og_image": "https://sample.example/img/og.png",
 "title_suffix": " - サンプル", "per_page": 2, "posts": {
  "memo": {"label": "メモ", "cats": [["a", "甲"], ["b", "乙"], ["c", "丙"]], "post_url": "memo/{yyyymmdd}/{id}", "cat_url": "memo/cat/{cat}",
   "cat_title": "{name} - メモ", "cat_h1": "メモ（{name}）", "description": "メモの説明", "nav": "/memo", "indent": 2}}}`

func memoFiles(extra map[string]string) map[string]string {
	files := map[string]string{
		"tpl/memo-list.html": "<h1>{{H1}}</h1>\n{{CAT_LIST}}--\n{{ITEMS}}--\n{{PAGINATION}}",
		"tpl/memo-item.html": "<li>{{URL}} {{DATE}} {{TITLE}} [{{TITLE_ATTR}}] {{CATS}} {{EYECATCH}} ({{DESCRIPTION}})</li>\n",
		"tpl/memo-post.html": "<p>{{DATE}} {{TITLE}} {{CATS}} {{EYECATCH}}</p>\n{{BODY}}",
		"content/seed/memo.json": `[
 {"id": 1, "date": "2026-01-01", "title": "一", "category": "a", "body": "b1"},
 {"id": "x2", "date": "2026-02-01", "title": "二（過去）", "category": "b", "body": "b2"},
 {"id": "x3", "date": "2026-03-01", "title": "三", "category": "a", "body": "b3", "description": "d3"}]`,
		"content/cms/memo.json": `[
 {"id": "x2", "date": "2026-02-01", "title": "二（取得）", "category": "b", "body": "c2", "eyecatch": "/img/x.png"},
 {"id": "x4", "date": "2026-03-01", "title": "四", "category": "a", "body": "c4"}]`,
	}
	for k, v := range extra {
		files[k] = v
	}
	return files
}

func runMemo(t *testing.T, files map[string]string, mod func(*sitegen.Options)) (dir string, out string, err error) {
	t.Helper()
	dir = newSite(t, postsConf, files)
	var sb strings.Builder
	err = sitegen.Run(opts(dir, func(o *sitegen.Options) {
		o.Stdout = &sb
		o.Builders = []sitegen.Builder{sitegen.ListDetailsFrom("posts", nil)}
		if mod != nil {
			mod(o)
		}
	}))
	return dir, sb.String(), err
}

func TestListDetailBuildsListsCategoriesAndPosts(t *testing.T) {
	dir, out, err := runMemo(t, memoFiles(nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "memo: 記事 4 件（うち microCMS 2 件）\n") {
		t.Errorf("件数の出力: %q", out)
	}
	// 同じ日付は取得分（x4）が先、過去分（x3）が後。取得分と同じ id の過去分は消える
	all := readFile(t, dir+"/dist/memo/index.html")
	want := "<li>/memo/20260301/x4 2026/03/01 四 [四] <ul class=\"post-cat-list clearfix\"><li><a href=\"/memo/cat/a\">甲</a></li></ul> /img/og.png ()</li>\n" +
		"<li>/memo/20260301/x3 2026/03/01 三 [三] "
	if !strings.Contains(all, want) {
		t.Errorf("一覧の並びが違う:\n%s", all)
	}
	if strings.Contains(all, "二（過去）") || strings.Contains(readFile(t, dir+"/dist/memo/page/2.html"), "二（過去）") {
		t.Errorf("同じ id は取得分が優先のはず")
	}
	// カテゴリの一覧は件数つき・記事の無いカテゴリは出さない（インデントは indent+1 個のタブ）
	for _, w := range []string{"\t\t\t<li><a href=\"/memo\">全表示</a></li>\n", "\t\t\t<li><a href=\"/memo/cat/a\">甲(3)</a></li>\n", "\t\t\t<li><a href=\"/memo/cat/b\">乙(1)</a></li>\n"} {
		if !strings.Contains(all, w) {
			t.Errorf("カテゴリの一覧に %q が無い:\n%s", w, all)
		}
	}
	if strings.Contains(all, "丙") || exists(dir+"/dist/memo/cat/c.html") {
		t.Errorf("記事の無いカテゴリの一覧は作らない")
	}
	// 2 件ずつ 2 ページ。ページ送りは indent 個のタブ
	if !strings.Contains(all, "\t\t<center><ul class=\"post-pagination\">\n\t\t\t<li class=\"active\"><span>1</span></li>\n\t\t\t<li><a href=\"/memo/page/2\">2</a></li>\n\t\t\t<li class=\"next\"><a href=\"/memo/page/2\">&raquo;</a></li>\n\t\t</ul></center>\n") {
		t.Errorf("ページ送り:\n%s", all)
	}
	p2 := readFile(t, dir+"/dist/memo/page/2.html")
	if !strings.Contains(p2, "<li class=\"prev\"><a href=\"/memo\">&laquo;</a></li>") || strings.Contains(p2, "next") {
		t.Errorf("2 ページ目:\n%s", p2)
	}
	// カテゴリ別: 件数が 1 ページに収まればページ送りなし、見出しは形に沿う
	b := readFile(t, dir+"/dist/memo/cat/b.html")
	if !strings.Contains(b, "<h1>メモ（乙）</h1>") || strings.Contains(b, "post-pagination") {
		t.Errorf("カテゴリ別:\n%s", b)
	}
	// 個別ページ: 数の id・日付の形
	if post := readFile(t, dir+"/dist/memo/20260101/1.html"); !strings.Contains(post, "<p>2026/01/01 一 ") || !strings.Contains(post, "b1") {
		t.Errorf("個別ページ:\n%s", post)
	}
}

func TestListDetailPageMeta(t *testing.T) {
	metas := map[string]*sitegen.Meta{}
	_, _, err := runMemo(t, memoFiles(nil), func(o *sitegen.Options) {
		o.Layout = sitegen.LayoutFunc(func(s *sitegen.Site, url string, m *sitegen.Meta, main string) (map[string]string, error) {
			metas[url] = m
			return nil, nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	check := func(url, title, desc, typ, image string) {
		t.Helper()
		m := metas[url]
		if m == nil {
			t.Fatalf("%s のページが無い", url)
		}
		if m.Title != title || m.Description != desc || m.OGType != typ || m.Image != image || m.Get("nav") != "/memo" {
			t.Errorf("%s の Meta = %+v", url, m)
		}
	}
	check("memo", "メモ - サンプル", "メモの説明", "", "")
	check("memo/page/2", "メモ - サンプル - Page 2", "メモの説明", "", "")
	check("memo/cat/a", "甲 - メモ - サンプル", "メモの説明", "", "")
	check("memo/20260301/x3", "三 - サンプル", "d3", "article", "")                                         // 記事の description
	check("memo/20260301/x4", "四 - サンプル", "サイトの説明", "article", "")                                     // 無ければサイトの説明
	check("memo/20260201/x2", "二（取得） - サンプル", "サイトの説明", "article", "https://sample.example/img/x.png") // アイキャッチは絶対 URL
}

func TestListDetailUnknownCategoryIsUserError(t *testing.T) {
	_, _, err := runMemo(t, memoFiles(map[string]string{"content/cms/memo.json": `[{"id": "z", "date": "2026-05-01", "title": "t", "category": "zz", "body": ""}]`}), nil)
	msg := userMsg(t, err)
	if !strings.Contains(msg, "memo z: 未知のカテゴリ zz") || !strings.Contains(msg, "cats") {
		t.Errorf("メッセージ: %q", msg)
	}
}

func TestListDetailIDsOfDifferentTypesAreDifferent(t *testing.T) {
	// 取得分の "1"（文字列）は過去分の 1（数）を上書きしない。どちらも URL が同じになるので重複で止まる
	_, _, err := runMemo(t, memoFiles(map[string]string{"content/cms/memo.json": `[{"id": "1", "date": "2026-01-01", "title": "t", "category": "a", "body": ""}]`}), nil)
	if msg := userMsg(t, err); !strings.Contains(msg, "URL が重複: /memo/20260101/1") {
		t.Errorf("メッセージ: %q", msg)
	}
}

func TestListDetailRejectsUnknownPlaceholder(t *testing.T) {
	conf := strings.Replace(postsConf, "memo/{yyyymmdd}/{id}", "memo/{slug}", 1)
	dir := newSite(t, conf, memoFiles(nil))
	err := sitegen.Run(opts(dir, func(o *sitegen.Options) { o.Builders = []sitegen.Builder{sitegen.ListDetailsFrom("posts", nil)} }))
	if msg := userMsg(t, err); !strings.Contains(msg, "post_url") || !strings.Contains(msg, "{id}") {
		t.Errorf("メッセージ: %q", msg)
	}
}

func TestListDetailCustomization(t *testing.T) {
	// 調整: 件数・タイトルの形・ページ送りの差し替え・過去分の置き場所
	dir := newSite(t, postsConf, memoFiles(map[string]string{
		"content/old/memo.json": `{"contents": [{"id": "o1", "date": "2025-12-01", "title": "古", "category": "c", "body": "ob"}]}`,
	}))
	var sb strings.Builder
	err := sitegen.Run(opts(dir, func(o *sitegen.Options) {
		o.Stdout = &sb
		o.Builders = []sitegen.Builder{sitegen.ListDetailsFrom("posts", func(s *sitegen.Site, d *sitegen.ListDetail) {
			d.PerPage = 1
			d.PastDir = "old"
			d.PartTitle = "（%d）"
			d.AllLabel = "ぜんぶ"
			d.Pagination = func(s *sitegen.Site, indent, current int, urls []string) string {
				return fmt.Sprintf("<p>%d/%d %s</p>\n", current+1, len(urls), s.Href(urls[current]))
			}
		})}
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sb.String(), "memo: 記事 3 件（うち microCMS 2 件）") {
		t.Errorf("出力: %q", sb.String())
	}
	p := readFile(t, dir+"/dist/memo/page/3.html")
	if !strings.Contains(p, "<p>3/3 /memo/page/3</p>\n") || !strings.Contains(p, "ぜんぶ") {
		t.Errorf("差し替えたページ送り:\n%s", p)
	}
	if !exists(dir + "/dist/memo/cat/c.html") {
		t.Errorf("過去分の置き場所を変えたので丙の記事が出るはず")
	}
}

func TestPostPaginationShape(t *testing.T) {
	dir := newSite(t, "", nil)
	var got []string
	err := sitegen.Run(opts(dir, func(o *sitegen.Options) {
		o.Builders = []sitegen.Builder{sitegen.BuilderFunc(func(s *sitegen.Site) error {
			urls := []string{"l", "l/page/2", "l/page/3"}
			got = []string{sitegen.PostPagination(s, 1, 1, urls), sitegen.PostPagination(s, 1, 0, urls[:1])}
			return nil
		})}
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := "\t<center><ul class=\"post-pagination\">\n\t\t<li class=\"prev\"><a href=\"/l\">&laquo;</a></li>\n\t\t<li><a href=\"/l\">1</a></li>\n" +
		"\t\t<li class=\"active\"><span>2</span></li>\n\t\t<li><a href=\"/l/page/3\">3</a></li>\n\t\t<li class=\"next\"><a href=\"/l/page/3\">&raquo;</a></li>\n\t</ul></center>\n"
	if got[0] != want || got[1] != "" {
		t.Errorf("ページ送り = %q と %q", got[0], got[1])
	}
}

func TestListDetailFromConfig(t *testing.T) {
	v, _ := pyfmt.ParseJSON(`{"label": "L", "cats": [["s", "N"]], "post_url": "p/{id}", "cat_url": "c/{cat}", "cat_title": "{name}", "cat_h1": "h", "excerpt": true, "nav": null, "indent": 3}`)
	d := sitegen.ListDetailFromConfig("k", pyfmt.AsObj(v))
	if d.Kind != "k" || d.Label != "L" || len(d.Cats) != 1 || d.Cats[0] != (sitegen.Cat{Slug: "s", Name: "N"}) || !d.Excerpt || d.Nav != "" || d.Indent != 3 || d.Description != "" {
		t.Errorf("設定の読み取り: %+v", d)
	}
}

// ---------------------------------------------------------------- 囲みブロック

func TestParsePageSourceBlocks(t *testing.T) {
	src := "<!--\ntitle: T\nnav: /x\n-->\n<!-- head-meta -->\n<meta name=\"robots\" content=\"noindex\">\n<!-- /head-meta -->\n<!-- head-script -->\n<script>a</script>\n<!-- /head-script -->\n<p>本文</p>\n"
	m, main := sitegen.ParsePageSourceBlocks("p.html", src, []string{"head-meta", "head-script"})
	if m.Get("head_meta") != "<meta name=\"robots\" content=\"noindex\">\n" || m.Get("head_script") != "<script>a</script>\n" || main != "<p>本文</p>\n" {
		t.Errorf("ブロック = %q %q 本文 %q", m.Get("head_meta"), m.Get("head_script"), main)
	}
	// 名前の順に 1 回ずつ探すので、順が違うと後ろのブロックは本文に残る。ブロックの無いページは本文がそのまま
	m, main = sitegen.ParsePageSourceBlocks("p.html", src, []string{"head-script", "head-meta"})
	if _, ok := m.Lookup("head_script"); ok || m.Get("head_meta") == "" || !strings.HasPrefix(main, "<!-- head-script -->") {
		t.Errorf("順が違うとき: %v %q", m.Extra, main)
	}
	if _, main = sitegen.ParsePageSourceBlocks("p.html", "<!--\ntitle: T\n-->\n<p>x</p>\n", []string{"head-meta"}); main != "<p>x</p>\n" {
		t.Errorf("ブロックなし: %q", main)
	}
	if _, main = sitegen.ParsePageSource("p.html", src); !strings.HasPrefix(main, "<!-- head-meta -->") {
		t.Errorf("ParsePageSource はブロックを読まない: %q", main)
	}
}

func TestRunReadsPageBlocks(t *testing.T) {
	dir := newSite(t, "", map[string]string{
		"layout.html":      "{{HEAD}}|{{MAIN}}",
		"pages/index.html": "<!--\ntitle: T\n-->\n<!-- head-meta -->\n<m>\n<!-- /head-meta -->\nbody\n",
	})
	err := sitegen.Run(opts(dir, func(o *sitegen.Options) {
		o.PageBlocks = []string{"head-meta"}
		o.Layout = sitegen.LayoutFunc(func(s *sitegen.Site, url string, m *sitegen.Meta, main string) (map[string]string, error) {
			return map[string]string{"HEAD": m.Get("head_meta")}, nil
		})
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, dir+"/dist/index.html"); got != "<m>\n|body\n" {
		t.Errorf("index.html = %q", got)
	}
}

// ---------------------------------------------------------------- 画像の差し替え・その他の設定

func TestSwapImagesRule(t *testing.T) {
	swaps := []sitegen.ImageSwap{{From: "/img/a.jpg", To: "/img/new-a.jpg"}, {From: "/img/b.png", To: "/img/new-b.png"}}
	in := `<img src="/img/a.jpg"> '/img/a.jpg' /img/a.jpg?w=1 url(/img/a.jpg) /img/a.jpg.bak /img/a.jpgx /img/a.jpg /img/b.png" /img/ab.png`
	want := `<img src="/img/new-a.jpg"> '/img/new-a.jpg' /img/new-a.jpg?w=1 url(/img/new-a.jpg) /img/a.jpg.bak /img/a.jpgx /img/a.jpg /img/new-b.png" /img/ab.png`
	if got := sitegen.SwapImages(in, swaps); got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
	if got := sitegen.SwapImages("末尾 /img/b.png", swaps); got != "末尾 /img/b.png" {
		t.Errorf("直後が無い（文字列の末尾）ときは置かない: %q", got)
	}
	// 書かれた順に当てる: 先の結果に後の元のパスが重なれば連鎖する
	chain := []sitegen.ImageSwap{{From: "/a.png", To: "/b.png"}, {From: "/b.png", To: "/c.png"}}
	if got := sitegen.SwapImages(`"/a.png" "/b.png"`, chain); got != `"/c.png" "/c.png"` {
		t.Errorf("連鎖: %q", got)
	}
	v, _ := pyfmt.ParseJSON(`{"/z.png": "/new/z.png", "/y.png": "/new/y.png"}`)
	if got := sitegen.ImageSwapsFromObj(pyfmt.AsObj(v)); len(got) != 2 || got[0] != (sitegen.ImageSwap{From: "/z.png", To: "/new/z.png"}) || got[1].From != "/y.png" {
		t.Errorf("ImageSwapsFromObj: %v", got)
	}
}

func TestRunSwapsImagesInPagesAndCSS(t *testing.T) {
	dir := newSite(t, "", map[string]string{
		"layout.html":        "<img src=\"/img/a.png\">{{MAIN}}",
		"pages/index.html":   "<!--\ntitle: T\n-->\n<p style=\"background: url(/img/a.png)\"></p><a href=\"/img/a.png.bak\">x</a>\n",
		"static/css/s.css":   "a { background: url(/img/a.png) }\r\nb { background: url('/img/a.png') }\r\n",
		"static/css/keep.js": "x = '/img/a.png'",
	})
	err := sitegen.Run(opts(dir, func(o *sitegen.Options) {
		o.SkipStatic = false
		o.ImageSwaps = []sitegen.ImageSwap{{From: "/img/a.png", To: "/img/n.png"}}
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, dir+"/dist/index.html"); got != "<img src=\"/img/n.png\"><p style=\"background: url(/img/n.png)\"></p><a href=\"/img/a.png.bak\">x</a>\n" {
		t.Errorf("index.html = %q", got)
	}
	// CSS は読み込みで改行が \n にそろう（差し替えがあるときだけ）。CSS 以外は触らない
	if got := readFile(t, dir+"/dist/css/s.css"); got != "a { background: url(/img/n.png) }\nb { background: url('/img/n.png') }\n" {
		t.Errorf("s.css = %q", got)
	}
	if got := readFile(t, dir+"/dist/css/keep.js"); got != "x = '/img/a.png'" {
		t.Errorf("keep.js = %q", got)
	}
}

func TestRunWithoutSwapsKeepsCSSBytes(t *testing.T) {
	dir := newSite(t, "", map[string]string{"static/css/s.css": "a {}\r\n", "pages/index.html": "<!--\ntitle: T\n-->\nx\n"})
	if err := sitegen.Run(opts(dir, func(o *sitegen.Options) { o.SkipStatic = false })); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, dir+"/dist/css/s.css"); got != "a {}\r\n" {
		t.Errorf("差し替えが無ければ CSS はそのまま: %q", got)
	}
}

func TestNoFragmentsKeepsTokensInBody(t *testing.T) {
	dir := newSite(t, "", map[string]string{"pages/index.html": "<!--\ntitle: T\n-->\n{{literal}}\n"})
	if err := sitegen.Run(opts(dir, nil)); err == nil {
		t.Fatal("既定では本文の {{...}} は部品として読み、値が無ければ止まる")
	}
	if err := sitegen.Run(opts(dir, func(o *sitegen.Options) { o.NoFragments = true })); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, dir+"/dist/index.html"); got != "[{{literal}}\n]" {
		t.Errorf("index.html = %q", got)
	}
}

func TestSummaryReplacesLastLine(t *testing.T) {
	dir := newSite(t, "", map[string]string{"pages/index.html": "<!--\ntitle: T\n-->\nx\n"})
	var sb strings.Builder
	if err := sitegen.Run(opts(dir, func(o *sitegen.Options) {
		o.Stdout = &sb
		o.Summary = func(pages int, out string) string { return fmt.Sprintf("全 %d 枚 → %s\n", pages, "出力先") }
	})); err != nil {
		t.Fatal(err)
	}
	if sb.String() != "全 1 枚 → 出力先\n" {
		t.Errorf("出力 = %q", sb.String())
	}
}

func TestContentInReadsOneDirectory(t *testing.T) {
	dir := newSite(t, "", map[string]string{
		"content/a.json":      `[1]`,
		"content/cms/a.json":  `{"contents": [2]}`,
		"content/seed/a.json": `[3]`,
	})
	var got []string
	err := sitegen.Run(opts(dir, func(o *sitegen.Options) {
		o.Builders = []sitegen.Builder{sitegen.BuilderFunc(func(s *sitegen.Site) error {
			for _, d := range []string{".", "cms", "seed", "none"} {
				v, ok := s.ContentIn(d, "a")
				got = append(got, fmt.Sprint(pyfmt.ReprAny(v), ok))
			}
			return nil
		})}
	}))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"[1]true", "[2]true", "[3]true", "Nonefalse"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("ContentIn = %v", got)
	}
}
