package sitegen_test

// 見本サイト（testdata/sample）を作った結果を、保存済みの正解（testdata/sample_expected）と 1 バイトずつ比べる基準テスト。
// 正解を作り直すのは `go test ./sitegen -run Sample -update`（差分を目で見てから取り込む）。

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/EQUATION-CO-LTD/static-site-kit/sitegen"
	"github.com/EQUATION-CO-LTD/static-site-kit/sitegen/pyfmt"
)

var update = flag.Bool("update", false, "見本サイトの正解（testdata/sample_expected）を作り直す")

const (
	sampleDir      = "testdata/sample"
	sampleExpected = "testdata/sample_expected"
)

// sampleNews は見本サイトのお知らせ。一覧（2 件ずつにページ送り）・個別ページ・トップの最新 3 件・404 を足す Builder
type sampleNews struct{}

func (sampleNews) Build(s *sitegen.Site) error {
	data, ok := s.Content("news")
	if !ok {
		sitegen.Die("news がない")
	}
	posts := pyfmt.SortedStable(pyfmt.AsObjs(data), func(x, y *pyfmt.Obj) bool {
		return pyfmt.Str(x.At("date")) > pyfmt.Str(y.At("date")) // 新しい順
	})
	item := func(p *pyfmt.Obj) string {
		return sitegen.Fill(s.Tpl("news-item"), map[string]string{
			"ID": pyfmt.Str(p.At("id")), "DATE": sitegen.JDate(pyfmt.Str(p.At("date")), "."), "TITLE": pyfmt.Esc(p.At("title")),
		}, "tpl/news-item.html")
	}
	var latest strings.Builder
	for i, p := range posts {
		if i >= 3 {
			break
		}
		latest.WriteString(item(p))
	}
	s.SetFragment("LATEST", latest.String())
	s.SetFragment("CONTACT", `<p class="contact"><a href="/about/">お問い合わせ</a></p>`)

	for _, p := range posts {
		main := sitegen.Fill(s.Tpl("news-post"), map[string]string{
			"TITLE": pyfmt.Esc(p.At("title")), "DATE": sitegen.JDate(pyfmt.Str(p.At("date")), "."), "BODY": pyfmt.Str(p.At("body")),
		}, "tpl/news-post.html")
		m := sitegen.NewMeta("title", pyfmt.Esc(p.At("title"))+s.TitleSuffix(), "og_type", "article",
			"description", sitegen.Plain(p.At("body"), 110), "body_id", "post", "nav", "/news/").
			WithCrumbs(sitegen.Crumb{Name: "お知らせ", URL: "/news/"}).
			WithLD(s.ArticleLD("BlogPosting", pyfmt.Str(p.At("title")), sitegen.JDate(pyfmt.Str(p.At("date")), "-"), pyfmt.Str(p.At("date")), "", ""))
		s.AddPage("news/"+pyfmt.Str(p.At("id")), m, main)
	}

	pages := sitegen.Paginate("news", posts, pyfmt.AsInt(s.Config().At("per_page")))
	urls := make([]string, len(pages))
	for i, c := range pages {
		urls[i] = c.URL
	}
	for n, c := range pages {
		var items strings.Builder
		for _, p := range c.Items {
			items.WriteString(item(p))
		}
		part := ""
		if n > 0 {
			part = fmt.Sprintf("（%d ページ目）", n+1)
		}
		main := sitegen.Fill(s.Tpl("news-list"), map[string]string{"PART": part, "ITEMS": items.String(), "PAGER": samplePager(s, urls, n)}, "tpl/news-list.html")
		s.AddPage(c.URL, sitegen.NewMeta("title", "お知らせ"+part+s.TitleSuffix(), "body_id", "list", "nav", "/news/"), main)
	}
	s.AddPage("404", sitegen.NewMeta("title", "ページが見つかりません"+s.TitleSuffix()), "<h1>ページが見つかりません</h1>")
	return nil
}

func samplePager(s *sitegen.Site, urls []string, current int) string {
	if len(urls) < 2 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(`<nav class="pager">`)
	for i, u := range urls {
		if i == current {
			fmt.Fprintf(&sb, `<span>%d</span>`, i+1)
		} else {
			fmt.Fprintf(&sb, `<a href="%s">%d</a>`, s.Href(u), i+1)
		}
	}
	sb.WriteString(`</nav>`)
	return sb.String()
}

// sampleLayout は見本サイトの外枠の値。SEO はキットの全部入りを使い、ナビと会社名を足す
type sampleLayout struct{}

func (sampleLayout) Values(s *sitegen.Site, url string, m *sitegen.Meta, main string) (map[string]string, error) {
	var nav strings.Builder
	nav.WriteString("<ul>")
	for _, it := range pyfmt.AsObjs(s.Config().At("nav")) {
		cur := ""
		if m.Get("nav") == pyfmt.Str(it.At("url")) || (url == "" && it.At("url") == "/") {
			cur = ` class="current"`
		}
		fmt.Fprintf(&nav, `<li%s><a href="%s">%s</a></li>`, cur, pyfmt.Str(it.At("url")), pyfmt.Esc(it.At("label")))
	}
	nav.WriteString("</ul>")
	attrs := ""
	if id := m.Get("body_id"); id != "" {
		attrs = fmt.Sprintf(` id="%s"`, id)
	}
	seo := s.SEO(url, m, sitegen.SEOOptions{Org: pyfmt.O("telephone", "000-0000-0000")})
	if url == "404" {
		seo = fmt.Sprintf("<title>%s</title>\n", m.Title)
	}
	return map[string]string{
		"SEO": seo, "BODY_ATTRS": attrs, "NAV": nav.String(), "LOGO": pyfmt.Str(s.Config().At("logo")), "SITE_NAME": pyfmt.Esc(s.Config().At("company")),
	}, nil
}

// sampleOptions は見本サイトを作る設定
func sampleOptions(out string) sitegen.Options {
	return sitegen.Options{
		Dir: sampleDir, Out: out, Today: "2026-06-01", Stdout: io.Discard,
		Builders: []sitegen.Builder{sampleNews{}}, Layout: sampleLayout{}, SWAConfig: true,
	}
}

// readTree は dir 以下のファイルを 相対パス → 中身 で返す
func readTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		out[filepath.ToSlash(rel)] = string(bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSampleSiteMatchesSavedOutput(t *testing.T) {
	out := t.TempDir() + "/dist"
	if err := sitegen.Run(sampleOptions(out)); err != nil {
		t.Fatal(err)
	}
	if *update {
		if err := os.RemoveAll(sampleExpected); err != nil {
			t.Fatal(err)
		}
		for rel, body := range readTree(t, out) {
			p := filepath.Join(sampleExpected, filepath.FromSlash(rel))
			os.MkdirAll(filepath.Dir(p), 0o755)
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		t.Skip("正解を作り直した")
	}
	got, want := readTree(t, out), readTree(t, sampleExpected)
	var names []string
	for n := range got {
		names = append(names, n)
	}
	for n := range want {
		if _, ok := got[n]; !ok {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		g, gok := got[n]
		w, wok := want[n]
		switch {
		case !wok:
			t.Errorf("正解に無いファイルが出来た: %s", n)
		case !gok:
			t.Errorf("正解にあるファイルが出来ていない: %s", n)
		case g != w:
			t.Errorf("%s が正解と違う\n--- got\n%s\n--- want\n%s", n, g, w)
		}
	}
}

// 同じ入力からは、いつ・何度作っても同じバイト列になる
func TestSampleSiteIsDeterministic(t *testing.T) {
	a, b := t.TempDir()+"/a", t.TempDir()+"/b"
	for _, out := range []string{a, b} {
		if err := sitegen.Run(sampleOptions(out)); err != nil {
			t.Fatal(err)
		}
	}
	ta, tb := readTree(t, a), readTree(t, b)
	if len(ta) == 0 || len(ta) != len(tb) {
		t.Fatalf("ファイル数が違う: %d と %d", len(ta), len(tb))
	}
	for n, body := range ta {
		if tb[n] != body {
			t.Errorf("%s が 2 回の作成で違う", n)
		}
	}
}
