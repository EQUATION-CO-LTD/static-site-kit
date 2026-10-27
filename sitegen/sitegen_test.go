package sitegen_test

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EQUATION-CO-LTD/static-site-kit/sitegen"
	"github.com/EQUATION-CO-LTD/static-site-kit/sitegen/pyfmt"
)

// ---------------------------------------------------------------- 道具

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

const baseConf = `{"site": "https://sample.example/", "company": "サンプル株式会社", "og_image": "/og.png", "logo": "/logo.png", "title_suffix": " | S"}`

// newSite は最小のサイト（site.json と layout.html）に files を足した一時ディレクトリを作る
func newSite(t *testing.T, conf string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if conf == "" {
		conf = baseConf
	}
	writeFiles(t, dir, map[string]string{"site.json": conf, "layout.html": "[{{MAIN}}]"})
	writeFiles(t, dir, files)
	return dir
}

func opts(dir string, mod func(*sitegen.Options)) sitegen.Options {
	o := sitegen.Options{Dir: dir, Out: dir + "/dist", SkipStatic: true, Stdout: io.Discard, Today: "2026-06-01"}
	if mod != nil {
		mod(&o)
	}
	return o
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func userMsg(t *testing.T, err error) string {
	t.Helper()
	var ue *sitegen.UserError
	if !errors.As(err, &ue) {
		t.Fatalf("UserError のはず: %T %v", err, err)
	}
	return ue.Msg
}

// addPages は Builder を作る（url → 本文）
func addPages(pages map[string]string) sitegen.Builder {
	return sitegen.BuilderFunc(func(s *sitegen.Site) error {
		for u, main := range pages {
			s.AddPage(u, sitegen.NewMeta("title", u), main)
		}
		return nil
	})
}

var goldenFile = func() *pyfmt.Obj {
	b, err := os.ReadFile("pyfmt/testdata/py_golden.json")
	if err != nil {
		panic(err)
	}
	v, err := pyfmt.ParseJSON(string(b))
	if err != nil {
		panic(err)
	}
	return pyfmt.AsObj(v)
}()

// ---------------------------------------------------------------- 部品

func TestFill(t *testing.T) {
	vals := map[string]string{"A": "1", "B": "{{A}}"}
	if got := sitegen.Fill("x{{A}}y{{B}}z", vals, "t"); got != "x1y{{A}}z" { // 差し込んだ値は読み直さない
		t.Errorf("Fill = %q", got)
	}
	if got := sitegen.Fill("no tokens {single} {{ spaced }}", vals, "t"); got != "no tokens {single} {{ spaced }}" {
		t.Errorf("Fill = %q", got)
	}
	if got := sitegen.Fill("{{日本語_1:a/b-c}}", map[string]string{"日本語_1:a/b-c": "ok"}, "t"); got != "ok" {
		t.Errorf("トークンの文字種: %q", got)
	}
	err := sitegen.Run(opts(newSite(t, "", map[string]string{"pages/index.html": "<!--\ntitle: T\n-->\n{{MISSING}}"}), nil))
	if msg := userMsg(t, err); msg != "/: {{MISSING}} に値がない" {
		t.Errorf("msg = %q", msg)
	}
}

func TestBrMatchesPython(t *testing.T) {
	for _, c := range pyfmt.AsObjs(goldenFile.At("esc")) {
		if got, want := sitegen.Br(pyfmt.Str(c.At("in"))), pyfmt.Str(c.At("br")); got != want {
			t.Errorf("Br(%q) = %q, want %q", pyfmt.Str(c.At("in")), got, want)
		}
	}
}

func TestPlainMatchesPython(t *testing.T) {
	for _, c := range pyfmt.AsObjs(goldenFile.At("plain")) {
		if got, want := sitegen.Plain(pyfmt.Str(c.At("in")), 110), pyfmt.Str(c.At("out")); got != want {
			t.Errorf("Plain(%q) = %q, want %q", pyfmt.Str(c.At("in")), got, want)
		}
	}
}

func TestPaginate(t *testing.T) {
	got := sitegen.Paginate("blog", []int{1, 2, 3, 4, 5}, 2)
	if len(got) != 3 || got[0].URL != "blog" || got[1].URL != "blog/page/2" || got[2].URL != "blog/page/3" || len(got[2].Items) != 1 {
		t.Errorf("Paginate = %+v", got)
	}
	if e := sitegen.Paginate("x", []int{}, 10); len(e) != 1 || e[0].URL != "x" || len(e[0].Items) != 0 {
		t.Errorf("空でもページは 1 つ: %+v", e)
	}
	if e := sitegen.Paginate("x", []int{1, 2, 3, 4}, 2); len(e) != 2 || len(e[1].Items) != 2 {
		t.Errorf("ちょうど割り切れるとき: %+v", e)
	}
}

func TestDates(t *testing.T) {
	if got := sitegen.JDate("2026-09-25T00:00:00.000Z", "."); got != "2026.09.25" {
		t.Errorf("JDate = %q", got)
	}
	if got := sitegen.JDate("2026-09-25T00:00:00.000Z", "-"); got != "2026-09-25" {
		t.Errorf("JDate = %q", got)
	}
	// UTC の 15:00 以降は日本時間では翌日
	for in, want := range map[string]string{"2026-09-24T15:00:00.000Z": "2026.09.25", "2026-09-24T14:59:59.999Z": "2026.09.24", "2026-12-31T23:59:59.999Z": "2027.01.01"} {
		if got := sitegen.JSTDotDate(in); got != want {
			t.Errorf("JSTDotDate(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestImgURLAndPick(t *testing.T) {
	img := pyfmt.O("url", "https://images.microcms-assets.io/assets/x/y/a.png")
	if got := sitegen.ImgURL(img, 0); got != "https://images.microcms-assets.io/assets/x/y/a.png" {
		t.Errorf("ImgURL = %q", got)
	}
	if got := sitegen.ImgURL(img, 200); got != "https://images.microcms-assets.io/assets/x/y/a.png?w=400&h=400&fit=crop" {
		t.Errorf("ImgURL(200) = %q", got)
	}
	if got := sitegen.ImgURL(pyfmt.O("url", "/images/cms/a.png"), 200); got != "/images/cms/a.png" { // 取得済みのローカル画像は切り出さない
		t.Errorf("ImgURL(local) = %q", got)
	}
	if sitegen.ImgURL(nil, 0) != "" {
		t.Errorf("画像が無ければ空")
	}
	if sitegen.Pick([]any{"お知らせ", "x"}) != "お知らせ" || sitegen.Pick("単独") != "単独" || sitegen.Pick(nil) != "" || sitegen.Pick([]any{}) != "" {
		t.Errorf("Pick: %q", sitegen.Pick([]any{}))
	}
}

// ---------------------------------------------------------------- メタ情報

func TestParsePageSource(t *testing.T) {
	m, main := sitegen.ParsePageSource("pages/a.html", "<!--\ntitle: タイトル: 副題\ndescription: 説明\nbody_id: x\nog_type: article\nempty: \n-->\n<p>本文</p>\n")
	if m.Title != "タイトル: 副題" || m.Description != "説明" || m.OGType != "article" || m.Get("body_id") != "x" || main != "<p>本文</p>\n" {
		t.Errorf("%+v %q", m, main)
	}
	if v, ok := m.Lookup("empty"); !ok || v != "" {
		t.Errorf("値が空のキーも Extra に入る: %q %v", v, ok)
	}
	if _, ok := m.Lookup("nothing"); ok {
		t.Errorf("無いキー")
	}
	for src, want := range map[string]string{
		"<p>先頭にコメントが無い</p>":                  "pages/a.html: 先頭にメタ情報のコメントがない",
		"<!--\ndescription: だけ\n-->\n":       "pages/a.html: メタ情報に title がない",
		"<!--\ntitle: \nbody_id: ok\n-->\nx": "",
	} {
		func() {
			defer func() {
				r := recover()
				if want == "" {
					if r != nil {
						t.Errorf("空の title は許す: %v", r)
					}
					return
				}
				ue, ok := r.(*sitegen.UserError)
				if !ok || ue.Msg != want {
					t.Errorf("%q: %v, want %q", src, r, want)
				}
			}()
			sitegen.ParsePageSource("pages/a.html", src)
		}()
	}
}

func TestMetaStandardAndExtraKeys(t *testing.T) {
	m := sitegen.NewMeta("title", "T", "description", "D", "image", "/i.png", "og_type", "article", "body_id", "b")
	if m.Title != "T" || m.Description != "D" || m.Image != "/i.png" || m.OGType != "article" || m.Extra["body_id"] != "b" || len(m.Extra) != 1 {
		t.Errorf("%+v", m)
	}
	if m.Get("title") != "T" || m.Get("body_id") != "b" || m.Get("nothing") != "" {
		t.Errorf("Get")
	}
	m.Set("nav", "/x/")
	if v, ok := m.Lookup("nav"); !ok || v != "/x/" {
		t.Errorf("Set/Lookup")
	}
	if m.HasCrumbs {
		t.Errorf("パンくずは既定で無し")
	}
	if !m.WithCrumbs().HasCrumbs {
		t.Errorf("引数なしでも HasCrumbs が真になる")
	}
}

// ---------------------------------------------------------------- 読み込み

func TestContentPrefersCmsOverSeedAndUnwrapsContents(t *testing.T) {
	dir := newSite(t, "", map[string]string{
		"content/seed/a.json":    `{"contents": [{"id": "seed"}], "totalCount": 1}`,
		"content/cms/a.json":     `{"contents": [{"id": "cms"}], "totalCount": 1}`,
		"content/seed/b.json":    `{"contents": [{"id": "seed-b"}]}`,
		"content/seed/obj.json":  `{"tel": "000"}`,
		"content/cms/null.json":  `null`,
		"content/seed/null.json": `[1]`,
	})
	var seen []string
	o := opts(dir, func(o *sitegen.Options) {
		o.Builders = []sitegen.Builder{sitegen.BuilderFunc(func(s *sitegen.Site) error {
			for _, n := range []string{"a", "b", "obj", "null", "none"} {
				v, ok := s.Content(n)
				seen = append(seen, fmt.Sprintf("%s=%v/%s", n, ok, pyfmt.Dump(v, pyfmt.DumpCompact)))
			}
			return nil
		})}
	})
	if err := sitegen.Run(o); err != nil {
		t.Fatal(err)
	}
	want := `a=true/[{"id":"cms"}] b=true/[{"id":"seed-b"}] obj=true/{"tel":"000"} null=true/[1] none=false/null`
	if got := strings.Join(seen, " "); got != want {
		t.Errorf("Content:\n got %s\nwant %s", got, want)
	}

	// 読む先は Options で変えられる
	var got2 string
	o.ContentDirs = []string{"seed"}
	o.Builders = []sitegen.Builder{sitegen.BuilderFunc(func(s *sitegen.Site) error {
		v, _ := s.Content("a")
		got2 = pyfmt.Dump(v, pyfmt.DumpCompact)
		return nil
	})}
	if err := sitegen.Run(o); err != nil || got2 != `[{"id":"seed"}]` {
		t.Errorf("ContentDirs: %q %v", got2, err)
	}

	// Content を指定しなければ Dir/content
	o.ContentDirs, o.Content = nil, dir+"/elsewhere"
	got2 = "unset"
	o.Builders = []sitegen.Builder{sitegen.BuilderFunc(func(s *sitegen.Site) error { _, ok := s.Content("a"); got2 = fmt.Sprint(ok); return nil })}
	if err := sitegen.Run(o); err != nil || got2 != "false" {
		t.Errorf("Content ディレクトリの指定: %q %v", got2, err)
	}
}

func TestSiteAccessors(t *testing.T) {
	dir := newSite(t, `{"site": "https://sample.example/", "company": "C", "trailing_slash": false, "title_suffix": " | S", "staging": true}`,
		map[string]string{"tpl/x.html": "tpl\r\nbody\n", "data/d.json": `{"k": 1}`, "pages/p.html": "<!--\ntitle: P\n-->\n"})
	err := sitegen.Run(opts(dir, func(o *sitegen.Options) {
		o.Builders = []sitegen.Builder{sitegen.BuilderFunc(func(s *sitegen.Site) error {
			check := func(name string, got, want any) {
				if fmt.Sprint(got) != fmt.Sprint(want) {
					t.Errorf("%s = %v, want %v", name, got, want)
				}
			}
			check("BaseURL", s.BaseURL(), "https://sample.example/")
			check("TitleSuffix", s.TitleSuffix(), " | S")
			check("Today", s.Today(), "2026-06-01")
			check("Staging", s.Staging(), true)
			check("Href", s.Href("a/b")+s.Href(""), "/a/b/")
			check("PageURL", s.PageURL("a")+" "+s.PageURL(""), "https://sample.example/a https://sample.example/")
			check("AbsURL", s.AbsURL("/images/日本.png")+" "+s.AbsURL("https://other.example/x"), "https://sample.example/images/%E6%97%A5%E6%9C%AC.png https://other.example/x")
			check("Tpl", s.Tpl("x"), "tpl\nbody\n")
			check("Exists", s.Exists("data/d.json"), true)
			check("ListDir", strings.Join(s.ListDir("data"), ","), "d.json")
			v, ok := s.LoadJSON("data/d.json")
			check("LoadJSON", pyfmt.Dump(v, pyfmt.DumpCompact), `{"k":1}`)
			check("LoadJSON ok", ok, true)
			check("HasPage", s.HasPage("p"), true)
			check("PageURLs", strings.Join(s.PageURLs(), ","), "p")
			check("Config", pyfmt.Str(s.Config().At("company")), "C")
			check("OrgID", s.OrgID(), "https://sample.example/#organization")
			return nil
		})}
	}))
	if err != nil {
		t.Fatal(err)
	}
}

func TestStagingOptionOverridesSiteJSON(t *testing.T) {
	on, off := true, false
	for _, c := range []struct {
		conf     string
		override *bool
		staging  bool
	}{
		{`{"site": "https://s.example/", "staging": true}`, nil, true},
		{`{"site": "https://s.example/", "staging": true}`, &off, false},
		{`{"site": "https://s.example/"}`, nil, false},
		{`{"site": "https://s.example/"}`, &on, true},
	} {
		dir := newSite(t, c.conf, nil)
		err := sitegen.Run(opts(dir, func(o *sitegen.Options) { o.SWAConfig = true; o.Staging = c.override }))
		if err != nil {
			t.Fatal(err)
		}
		cfg := readFile(t, dir+"/dist/staticwebapp.config.json")
		got := strings.Contains(cfg, `"allowedRoles"`) && strings.Contains(cfg, `"X-Robots-Tag": "noindex"`) && strings.Contains(cfg, "/.auth/login/aad")
		if got != c.staging {
			t.Errorf("staging=%v override=%v: 閲覧制限と noindex の有無 %v\n%s", c.conf, c.override, got, cfg)
		}
	}
}

func TestStagingRoles(t *testing.T) {
	for _, c := range []struct {
		conf string
		want string
	}{
		{`{"site": "https://s.example/", "staging": true}`, `"allowedRoles":["tester"]`},
		{`{"site": "https://s.example/", "staging": true, "staging_roles": ["authenticated"]}`, `"allowedRoles":["authenticated"]`},
	} {
		dir := newSite(t, c.conf, nil)
		if err := sitegen.Run(opts(dir, func(o *sitegen.Options) { o.SWAConfig = true })); err != nil {
			t.Fatal(err)
		}
		cfg := readFile(t, dir+"/dist/staticwebapp.config.json")
		if !strings.Contains(strings.Join(strings.Fields(cfg), ""), c.want) {
			t.Errorf("%s: 閲覧できる役割が %s になっていない\n%s", c.conf, c.want, cfg)
		}
	}
}

// ---------------------------------------------------------------- 書き出し

func TestPagePathRules(t *testing.T) {
	pages := map[string]string{"": "top", "404": "nf", "about": "a", "news": "n", "news/a": "na", "news/page/2": "p2", "docs/x/y": "y"}
	for _, c := range []struct {
		slash string
		want  []string
	}{
		// 末尾スラッシュあり: 全ページ {url}/index.html
		{"true", []string{"index.html", "404.html", "about/index.html", "news/index.html", "news/a/index.html", "news/page/2/index.html", "docs/x/y/index.html"}},
		// なし: 子を持つ URL（news・news/page・docs・docs/x）だけ index.html、他は {url}.html
		{"false", []string{"index.html", "404.html", "about.html", "news/index.html", "news/a.html", "news/page/2.html", "docs/x/y.html"}},
	} {
		dir := newSite(t, fmt.Sprintf(`{"site": "https://s.example/", "trailing_slash": %s}`, c.slash), nil)
		if err := sitegen.Run(opts(dir, func(o *sitegen.Options) { o.Builders = []sitegen.Builder{addPages(pages)} })); err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, w := range c.want {
			if !exists(dir + "/dist/" + w) {
				t.Errorf("trailing_slash=%s: %s が出来ていない", c.slash, w)
			}
			got = append(got, w)
		}
		n := 0
		filepath.WalkDir(dir+"/dist", func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && !strings.HasSuffix(p, "sitemap.xml") {
				n++
			}
			return nil
		})
		if n != len(got) {
			t.Errorf("trailing_slash=%s: ファイル数 %d、期待 %d", c.slash, n, len(got))
		}
	}
}

func TestSitemapExcludes404AndQuotesURLs(t *testing.T) {
	dir := newSite(t, `{"site": "https://s.example/", "trailing_slash": true}`, nil)
	err := sitegen.Run(opts(dir, func(o *sitegen.Options) {
		o.Builders = []sitegen.Builder{addPages(map[string]string{"": "t", "404": "n", "日本語": "j", "a&b": "x"})}
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<urlset xmlns=\"http://www.sitemaps.org/schemas/sitemap/0.9\">\n" +
		"  <url><loc>https://s.example/</loc></url>\n" +
		"  <url><loc>https://s.example/a%26b/</loc></url>\n" +
		"  <url><loc>https://s.example/%E6%97%A5%E6%9C%AC%E8%AA%9E/</loc></url>\n</urlset>\n"
	if got := readFile(t, dir+"/dist/sitemap.xml"); got != want {
		t.Errorf("sitemap.xml =\n%s\nwant\n%s", got, want)
	}
}

func TestStaticIsCopiedUnlessSkipped(t *testing.T) {
	dir := newSite(t, "", map[string]string{"static/css/a.css": "a{}", "static/robots.txt": "x"})
	if err := sitegen.Run(opts(dir, func(o *sitegen.Options) { o.SkipStatic = false })); err != nil {
		t.Fatal(err)
	}
	if readFile(t, dir+"/dist/css/a.css") != "a{}" || !exists(dir+"/dist/robots.txt") {
		t.Errorf("static が写っていない")
	}
	if err := sitegen.Run(opts(dir, nil)); err != nil { // SkipStatic
		t.Fatal(err)
	}
	if exists(dir+"/dist/css/a.css") || !exists(dir+"/dist/sitemap.xml") {
		t.Errorf("SkipStatic なのに static を写した")
	}
}

func TestOutIsClearedBeforeWriting(t *testing.T) {
	dir := newSite(t, "", nil)
	writeFiles(t, dir, map[string]string{"dist/old.html": "old"})
	if err := sitegen.Run(opts(dir, nil)); err != nil {
		t.Fatal(err)
	}
	if exists(dir + "/dist/old.html") {
		t.Errorf("前の出力が残っている")
	}
}

func TestFailedBuildLeavesPreviousOutput(t *testing.T) {
	dir := newSite(t, "", map[string]string{"pages/index.html": "<!--\ntitle: T\n-->\n{{NOPE}}"})
	writeFiles(t, dir, map[string]string{"dist/keep.html": "keep"})
	if err := sitegen.Run(opts(dir, nil)); err == nil {
		t.Fatal("差し込みの誤りで止まるはず")
	}
	if readFile(t, dir+"/dist/keep.html") != "keep" {
		t.Errorf("失敗したビルドが前の出力を消した")
	}
}

func TestSWAConfig(t *testing.T) {
	dir := newSite(t, `{"site": "https://s.example/", "swa": {"navigationFallback": {"rewrite": "/404.html"}}}`, map[string]string{
		"redirects.json": `{"/z/": "/a/", "/old/": "/日本/"}`,
	})
	err := sitegen.Run(opts(dir, func(o *sitegen.Options) {
		o.SWAConfig = true
		o.Builders = []sitegen.Builder{sitegen.BuilderFunc(func(s *sitegen.Site) error {
			s.AddRedirect("/old/", "/ignored/") // redirects.json が優先
			s.AddRedirect("/b/", "/c/")
			return nil
		})}
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n \"routes\": [\n  {\n   \"route\": \"/b/\",\n   \"redirect\": \"/c/\",\n   \"statusCode\": 301\n  },\n" +
		"  {\n   \"route\": \"/old/\",\n   \"redirect\": \"/\\u65e5\\u672c/\",\n   \"statusCode\": 301\n  },\n" +
		"  {\n   \"route\": \"/z/\",\n   \"redirect\": \"/a/\",\n   \"statusCode\": 301\n  }\n ],\n" +
		" \"navigationFallback\": {\n  \"rewrite\": \"/404.html\"\n }\n}"
	if got := readFile(t, dir+"/dist/staticwebapp.config.json"); got != want {
		t.Errorf("staticwebapp.config.json =\n%q\nwant\n%q", got, want)
	}

	// 生成しない設定
	if err := sitegen.Run(opts(dir, func(o *sitegen.Options) { o.SWAConfig = false })); err != nil {
		t.Fatal(err)
	}
	if exists(dir + "/dist/staticwebapp.config.json") {
		t.Errorf("SWAConfig が偽なのに作った")
	}
}

func TestSWAConfigOverLimitStops(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("{")
	for i := 0; i < 600; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, `"/old-page-%04d/": "/new-page-%04d/"`, i, i)
	}
	sb.WriteString("}")
	dir := newSite(t, "", map[string]string{"redirects.json": sb.String()})
	err := sitegen.Run(opts(dir, func(o *sitegen.Options) { o.SWAConfig = true }))
	if msg := userMsg(t, err); !strings.Contains(msg, "SWA の上限（20KB）を超えた") {
		t.Errorf("msg = %q", msg)
	}
}

func TestPruneUploads(t *testing.T) {
	dir := newSite(t, "", map[string]string{
		"static/up/uploads/used-in-page.png":   "1",
		"static/up/uploads/used-in-css.png":    "2",
		"static/up/uploads/日本語 名前.png":         "3",
		"static/up/uploads/sub/used-in-js.png": "4",
		"static/up/uploads/redirected.png":     "5",
		"static/up/uploads/unused.png":         "6",
		"static/up/uploads/sub/unused-too.png": "7",
		"static/css/a.css":                     "a{background:url(/up/uploads/used-in-css.png)}",
		"static/js/a.js":                       `var u = "/up/uploads/sub/used-in-js.png";`,
		"redirects.json":                       `{"/r/": "/up/uploads/redirected.png"}`,
		"pages/index.html":                     "<!--\ntitle: T\n-->\n<img src=\"/up/uploads/used-in-page.png\"><img src='/up/uploads/%E6%97%A5%E6%9C%AC%E8%AA%9E%20%E5%90%8D%E5%89%8D.png'>",
	})
	run := func(mod func(*sitegen.Options)) {
		t.Helper()
		if err := sitegen.Run(opts(dir, func(o *sitegen.Options) {
			o.SkipStatic = false
			o.SWAConfig = true
			o.PruneUploads = "up/uploads"
			mod(o)
		})); err != nil {
			t.Fatal(err)
		}
	}
	var out strings.Builder
	run(func(o *sitegen.Options) { o.Stdout = &out })
	for _, keep := range []string{"used-in-page.png", "used-in-css.png", "日本語 名前.png", "sub/used-in-js.png", "redirected.png"} {
		if !exists(dir + "/dist/up/uploads/" + keep) {
			t.Errorf("使われている %s が消えた", keep)
		}
	}
	for _, gone := range []string{"unused.png", "sub/unused-too.png"} {
		if exists(dir + "/dist/up/uploads/" + gone) {
			t.Errorf("使われていない %s が残っている", gone)
		}
	}
	if !exists(dir + "/static/up/uploads/unused.png") {
		t.Errorf("元の static/ は消さない")
	}
	if !strings.Contains(out.String(), "使われていない WordPress の画像を外した: 0MB") {
		t.Errorf("出力: %q", out.String())
	}

	// SkipStatic のときは何もしない（そもそも写していない）。指定が無いときも消さない
	run(func(o *sitegen.Options) { o.PruneUploads = "" })
	if !exists(dir + "/dist/up/uploads/unused.png") {
		t.Errorf("PruneUploads が空なのに消した")
	}
}

// ---------------------------------------------------------------- 流れ・誤り

func TestFixedPagesThenBuildersInOrder(t *testing.T) {
	dir := newSite(t, "", map[string]string{
		"pages/index.html":   "<!--\ntitle: Home\n-->\n{{PARTS}}",
		"pages/a/b.html":     "<!--\ntitle: B\n-->\nb",
		"pages/a/index.html": "<!--\ntitle: A\n-->\na",
	})
	var trace []string
	err := sitegen.Run(opts(dir, func(o *sitegen.Options) {
		o.Builders = []sitegen.Builder{
			sitegen.BuilderFunc(func(s *sitegen.Site) error {
				trace = append(trace, "1:"+strings.Join(s.PageURLs(), ","))
				s.SetFragment("PARTS", "first")
				return nil
			}),
			sitegen.BuilderFunc(func(s *sitegen.Site) error {
				trace = append(trace, "2")
				s.SetFragment("PARTS", "second") // 部品は書き出す直前に差し込む（後から登録した値が使われる）
				return nil
			}),
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(trace, " "); got != "1:,a,a/b 2" { // 固定ページが先。a/index.html は URL a
		t.Errorf("trace = %s", got)
	}
	if got := readFile(t, dir+"/dist/index.html"); got != "[second]" {
		t.Errorf("index.html = %q", got)
	}
}

func TestFixedPageFilterRunsAfterAllPagesExist(t *testing.T) {
	dir := newSite(t, "", map[string]string{"pages/p.html": "<!--\ntitle: P\n-->\nbody"})
	var filtered []string
	err := sitegen.Run(opts(dir, func(o *sitegen.Options) {
		o.Builders = []sitegen.Builder{addPages(map[string]string{"dyn": "dynamic body"})}
		o.FixedPageFilter = func(s *sitegen.Site, url, main string) string {
			filtered = append(filtered, url)
			return main + "+" + fmt.Sprint(s.HasPage("dyn"))
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(filtered, ",") != "p" { // 固定ページだけ
		t.Errorf("filtered = %v", filtered)
	}
	if readFile(t, dir+"/dist/p.html") != "[body+true]" {
		t.Errorf("フィルターの結果が出ていない")
	}
	if got := readFile(t, dir+"/dist/dyn.html"); got != "[dynamic body]" {
		t.Errorf("動的ページには掛けない: %q", got)
	}
}

func TestErrors(t *testing.T) {
	// URL の重複（固定ページと Builder のページ）
	dir := newSite(t, `{"site": "https://s.example/", "trailing_slash": true}`, map[string]string{"pages/about.html": "<!--\ntitle: A\n-->\n"})
	err := sitegen.Run(opts(dir, func(o *sitegen.Options) { o.Builders = []sitegen.Builder{addPages(map[string]string{"about": "x"})} }))
	if msg := userMsg(t, err); msg != "URL が重複: /about/" {
		t.Errorf("msg = %q", msg)
	}

	// site.json が無い
	empty := t.TempDir()
	writeFiles(t, empty, map[string]string{"layout.html": "x"})
	if msg := userMsg(t, sitegen.Run(opts(empty, nil))); !strings.Contains(msg, "site.json がない") {
		t.Errorf("msg = %q", msg)
	}

	// Builder が error を返す
	boom := errors.New("builder failed")
	err = sitegen.Run(opts(newSite(t, "", nil), func(o *sitegen.Options) {
		o.Builders = []sitegen.Builder{sitegen.BuilderFunc(func(*sitegen.Site) error { return boom })}
	}))
	if !errors.Is(err, boom) {
		t.Errorf("Builder の error はそのまま返る: %v", err)
	}

	// Layout が error を返す
	err = sitegen.Run(opts(newSite(t, "", nil), func(o *sitegen.Options) {
		o.Builders = []sitegen.Builder{addPages(map[string]string{"x": "x"})}
		o.Layout = sitegen.LayoutFunc(func(*sitegen.Site, string, *sitegen.Meta, string) (map[string]string, error) { return nil, boom })
	}))
	if !errors.Is(err, boom) {
		t.Errorf("Layout の error はそのまま返る: %v", err)
	}

	// 想定外の panic は InternalError（元の error を取り出せる）
	inner := errors.New("inner")
	err = sitegen.Run(opts(newSite(t, "", nil), func(o *sitegen.Options) {
		o.Builders = []sitegen.Builder{sitegen.BuilderFunc(func(*sitegen.Site) error { panic(inner) })}
	}))
	var ie *sitegen.InternalError
	if !errors.As(err, &ie) || !errors.Is(err, inner) || len(ie.Stack) == 0 || !strings.HasPrefix(err.Error(), "エラー: ") {
		t.Errorf("InternalError のはず: %T %v", err, err)
	}
	err = sitegen.Run(opts(newSite(t, "", nil), func(o *sitegen.Options) {
		o.Builders = []sitegen.Builder{sitegen.BuilderFunc(func(s *sitegen.Site) error { s.Config().At("no-such-key"); return nil })}
	}))
	if !errors.As(err, &ie) || !strings.Contains(err.Error(), "KeyError") {
		t.Errorf("足りないキーは InternalError: %v", err)
	}

	// 外枠に値の無いトークン
	dir = newSite(t, "", map[string]string{"layout.html": "{{MAIN}}{{UNKNOWN}}"})
	err = sitegen.Run(opts(dir, func(o *sitegen.Options) { o.Builders = []sitegen.Builder{addPages(map[string]string{"x": "x"})} }))
	if msg := userMsg(t, err); msg != "layout.html: {{UNKNOWN}} に値がない" {
		t.Errorf("msg = %q", msg)
	}
}

func TestLayoutMainIsFilledUnlessReturned(t *testing.T) {
	dir := newSite(t, "", map[string]string{"layout.html": "<{{MAIN}}|{{X}}>"})
	err := sitegen.Run(opts(dir, func(o *sitegen.Options) {
		o.Builders = []sitegen.Builder{addPages(map[string]string{"a": "A", "b": "B"})}
		o.Layout = sitegen.LayoutFunc(func(s *sitegen.Site, url string, m *sitegen.Meta, main string) (map[string]string, error) {
			v := map[string]string{"X": url + ":" + m.Title}
			if url == "b" {
				v["MAIN"] = "own " + main
			}
			return v, nil
		})
	}))
	if err != nil {
		t.Fatal(err)
	}
	if readFile(t, dir+"/dist/a.html") != "<A|a:a>" {
		t.Errorf("MAIN は既定で本文になる")
	}
	if readFile(t, dir+"/dist/b.html") != "<own B|b:b>" {
		t.Errorf("Layout が MAIN を返したらそちらを使う")
	}
}

// ---------------------------------------------------------------- SEO の部品

func seoSite(t *testing.T, f func(s *sitegen.Site)) {
	t.Helper()
	dir := newSite(t, `{"site": "https://s.example/", "company": "会社 & 子", "og_image": "/og.png", "logo": "/logo.png", "title_suffix": " | S", "trailing_slash": true}`, nil)
	if err := sitegen.Run(opts(dir, func(o *sitegen.Options) {
		o.Builders = []sitegen.Builder{sitegen.BuilderFunc(func(s *sitegen.Site) error { f(s); return nil })}
	})); err != nil {
		t.Fatal(err)
	}
}

func TestSEOParts(t *testing.T) {
	seoSite(t, func(s *sitegen.Site) {
		m := sitegen.NewMeta("title", "記事 &amp; 題 | S", "description", "説明\"文\"", "image", "/img/a b.png", "og_type", "article")
		if got := s.Canonical("x/y"); got != `<link rel="canonical" href="https://s.example/x/y/" />` {
			t.Errorf("Canonical = %s", got)
		}
		if got := s.OGImage(m); got != "https://s.example/img/a%20b.png" {
			t.Errorf("OGImage = %s", got)
		}
		if got := s.OGImage(sitegen.NewMeta("title", "t")); got != "https://s.example/og.png" {
			t.Errorf("OGImage(既定) = %s", got)
		}
		ogp := strings.Join(s.OGP("x", m), "\n")
		for _, want := range []string{
			`<meta property="og:type" content="article" />`,
			`<meta property="og:title" content="記事 &amp;amp; 題 | S" />`,
			`<meta property="og:url" content="https://s.example/x/" />`,
			`<meta property="og:site_name" content="会社 &amp; 子" />`,
			`<meta property="og:image" content="https://s.example/img/a%20b.png" />`,
			`<meta name="twitter:image" content="https://s.example/img/a%20b.png" />`,
		} {
			if !strings.Contains(ogp, want) {
				t.Errorf("OGP に %s が無い:\n%s", want, ogp)
			}
		}
		if !strings.Contains(strings.Join(s.OGP("x", sitegen.NewMeta("title", "t")), "\n"), `content="website"`) {
			t.Errorf("og:type の既定は website")
		}

		seo := s.SEO("x", m, sitegen.SEOOptions{})
		if !strings.HasPrefix(seo, "<title>記事 &amp; 題 | S</title>\n<meta name=\"description\" content=\"説明&quot;文&quot;\" />\n<link rel=\"canonical\"") || !strings.HasSuffix(seo, "</script>\n") {
			t.Errorf("SEO の並び:\n%s", seo)
		}
		if strings.Contains(s.SEO("x", sitegen.NewMeta("title", "t"), sitegen.SEOOptions{}), `name="description"`) {
			t.Errorf("description が空なら出さない")
		}
	})
}

func TestJSONLD(t *testing.T) {
	seoSite(t, func(s *sitegen.Site) {
		// パンくず・ページ固有のノード・Organization の追加項目
		m := sitegen.NewMeta("title", "記事 | S").WithCrumbs(sitegen.Crumb{Name: "一覧", URL: "/list/"}).
			WithLD(s.ArticleLD("Article", "見出し", "2026-01-02", "2026-01-03", "/img/a.png", "概要"))
		got := s.JSONLD("list/a", m, sitegen.SEOOptions{Org: pyfmt.O("telephone", "000")})
		want := `<script type="application/ld+json">{"@context":"https://schema.org","@graph":[` +
			`{"@type":"Organization","@id":"https://s.example/#organization","name":"会社 & 子","url":"https://s.example/","logo":"https://s.example/logo.png","telephone":"000"},` +
			`{"@type":"WebSite","@id":"https://s.example/#website","url":"https://s.example/","name":"会社 & 子","publisher":{"@id":"https://s.example/#organization"},"inLanguage":"ja"},` +
			`{"@type":"WebPage","@id":"https://s.example/list/a/#webpage","url":"https://s.example/list/a/","name":"記事","isPartOf":{"@id":"https://s.example/#website"},"primaryImageOfPage":{"@type":"ImageObject","url":"https://s.example/og.png"},"inLanguage":"ja","breadcrumb":{"@id":"https://s.example/list/a/#breadcrumb"}},` +
			`{"@type":"BreadcrumbList","@id":"https://s.example/list/a/#breadcrumb","itemListElement":[` +
			`{"@type":"ListItem","position":1,"name":"HOME","item":"https://s.example/"},` +
			`{"@type":"ListItem","position":2,"name":"一覧","item":"https://s.example/list/"},` +
			`{"@type":"ListItem","position":3,"name":"記事","item":"https://s.example/list/a/"}]},` +
			`{"@type":"Article","headline":"見出し","datePublished":"2026-01-02","dateModified":"2026-01-03","author":{"@id":"https://s.example/#organization"},"publisher":{"@id":"https://s.example/#organization"},"image":["https://s.example/img/a.png"],"description":"概要","mainEntityOfPage":{"@id":"https://s.example/list/a/#webpage"}}]}</script>`
		if got != want {
			t.Errorf("JSONLD =\n%s\nwant\n%s", got, want)
		}

		// トップはパンくずを出さない。画像・概要が空の記事はそのキーを出さない。</ はエスケープする
		top := s.JSONLD("", sitegen.NewMeta("title", "ホーム").WithCrumbs(), sitegen.SEOOptions{})
		if strings.Contains(top, "BreadcrumbList") {
			t.Errorf("トップにパンくずは出さない")
		}
		a := s.ArticleLD("Article", strings.Repeat("あ", 200), "d", "d", "", "")
		if h := pyfmt.Str(a.At("headline")); pyfmt.RuneLen(h) != 110 || a.Has("image") || a.Has("description") {
			t.Errorf("ArticleLD: headline %d 文字 / image=%v description=%v", pyfmt.RuneLen(h), a.Has("image"), a.Has("description"))
		}
		esc := s.JSONLD("x", sitegen.NewMeta("title", "</script>"), sitegen.SEOOptions{})
		if strings.Count(esc, "</script>") != 1 {
			t.Errorf("</ をエスケープしていない: %s", esc)
		}
	})
}
