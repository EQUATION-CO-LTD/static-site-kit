package sitegen

// 書き出し: static の写し・ページ・Azure Static Web Apps の設定・sitemap.xml・使われていない取り込み画像の除外。

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/EQUATION-CO-LTD/static-site-kit/sitegen/pyfmt"
)

// swaConfigLimit は Azure Static Web Apps の設定ファイルの上限（20KB）
const swaConfigLimit = 20 * 1024

func mustOK(err error) {
	if err != nil {
		panic(err.Error())
	}
}

// pagePath はページの出力先（出力ディレクトリからの相対）。
//   - トップ（URL が空）は index.html、404 は 404.html
//   - trailing_slash が真なら全ページ {url}/index.html
//   - 偽なら、子を持つ URL（parents に入っている物）だけ {url}/index.html、他は {url}.html
func pagePath(url string, slash bool, parents map[string]bool) string {
	switch {
	case url == "":
		return "index.html"
	case url == "404":
		return "404.html"
	case slash || parents[url]:
		return url + "/index.html"
	}
	return url + ".html"
}

// parentURLs は子ページを持つ URL（a/b があれば a）の集まり
func parentURLs(pages map[string]*page) map[string]bool {
	parents := map[string]bool{}
	for u := range pages {
		for i := 0; i < len(u); i++ {
			if u[i] == '/' {
				parents[u[:i]] = true
			}
		}
	}
	return parents
}

func copyTree(src, dst string) {
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
		if st, err := d.Info(); err == nil {
			_ = os.Chtimes(target, st.ModTime(), st.ModTime()) // shutil.copytree（copy2）と同じく更新日時を残す
		}
		return nil
	})
	if err != nil {
		panic(err.Error())
	}
}

// emit は組み立て済みの HTML（URL → 本文）を出力先に書く
func (s *Site) emit(html map[string]string) {
	out := s.out
	_ = os.RemoveAll(out)
	if s.opts.SkipStatic { // 検証だけ。大きな静的ファイルを毎回写さない
		mustOK(os.MkdirAll(out, 0o755))
	} else {
		copyTree(s.path("static"), out)
		s.swapCSS()
	}
	parents := parentURLs(s.pages)
	for url, text := range html {
		p := out + "/" + pagePath(url, s.slash != "", parents)
		mustOK(os.MkdirAll(filepath.Dir(p), 0o755))
		pyfmt.WriteText(p, text)
	}

	var cfg *pyfmt.Obj
	if s.opts.SWAConfig {
		cfg = s.swaConfig()
		cfgText := pyfmt.Dump(cfg, pyfmt.DumpPrettyASCII) // json.dump は末尾に改行を足さない
		pyfmt.WriteText(out+"/staticwebapp.config.json", cfgText)
		if size := len(cfgText); size > swaConfigLimit {
			Die("staticwebapp.config.json が %d バイトで SWA の上限（20KB）を超えた", size)
		}
	}

	var sm strings.Builder
	sm.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<urlset xmlns=\"http://www.sitemaps.org/schemas/sitemap/0.9\">\n")
	urls := make([]string, 0, len(s.pages))
	for u := range s.pages {
		urls = append(urls, u)
	}
	sort.Strings(urls)
	for _, u := range urls {
		if u != "404" {
			fmt.Fprintf(&sm, "  <url><loc>%s</loc></url>\n", pyfmt.XMLEscape(s.base+pyfmt.Quote(s.Href(u)[1:], "/")))
		}
	}
	sm.WriteString("</urlset>\n")
	pyfmt.WriteText(out+"/sitemap.xml", sm.String())
	if s.opts.PruneUploads != "" && !s.opts.SkipStatic {
		swa := ""
		if cfg != nil {
			swa = pyfmt.Dump(cfg, pyfmt.DumpDefault)
		}
		s.pruneUploads(swa)
	}
	if s.opts.Summary != nil {
		fmt.Fprint(s.stdout, s.opts.Summary(len(s.pages), out))
		return
	}
	fmt.Fprintf(s.stdout, "ページ %d → %s/\n", len(s.pages), out)
}

// swaConfig は staticwebapp.config.json の中身。redirects.json（と Builder が足した転送）を 301 にし、
// site.json の swa をそのまま重ねる。staging のときは全ページを role tester に限定して検索に載せない
func (s *Site) swaConfig() *pyfmt.Obj {
	srcs := make([]string, 0, len(s.redirects))
	for k := range s.redirects {
		srcs = append(srcs, k)
	}
	sort.Strings(srcs)
	routes := make([]any, 0, len(srcs)+1)
	for _, a := range srcs {
		routes = append(routes, pyfmt.O("route", a, "redirect", s.redirects[a], "statusCode", pyfmt.IntNum(301)))
	}
	swa := pyfmt.AsObj(pyfmt.DeepCopy(pyfmt.Getd(s.conf, "swa", pyfmt.NewObj())))
	if s.staging {
		// 検証環境: 全ページを role tester（SWA の招待で付ける）に限定し、検索に載せない
		routes = append(routes, pyfmt.O("route", "/*", "allowedRoles", []any{"tester"}))
		pyfmt.AsObj(swa.SetDefault("responseOverrides", pyfmt.NewObj())).Set("401", pyfmt.O("statusCode", pyfmt.IntNum(302), "redirect", "/.auth/login/aad"))
		pyfmt.AsObj(swa.SetDefault("globalHeaders", pyfmt.NewObj())).Set("X-Robots-Tag", "noindex")
	}
	cfg := pyfmt.O("routes", routes)
	for _, k := range swa.Keys() {
		cfg.Set(k, swa.Get(k))
	}
	return cfg
}

// pruneUploads: 前のサイト（WordPress）から引き継いだ画像のうち、どのページ・CSS・JS・転送先からも使われていない物を
// 出力から外す。WordPress が自動で作ったサイズ違いが大半で、容量を食うため。元の static/ には残す。
// PruneUploads の最後の要素（uploads など）を目印に、本文中の参照（uploads/…）を集める
func (s *Site) pruneUploads(swaConfig string) {
	marker := filepath.Base(filepath.FromSlash(s.opts.PruneUploads))
	refRe := regexp.MustCompile(regexp.QuoteMeta(marker) + `/([^"'()` + pyfmt.Space + `?#,<>]+)`)
	used := map[string]bool{}
	scan := func(t string) {
		for _, m := range refRe.FindAllStringSubmatch(t, -1) {
			used[pyfmt.Unquote(pyfmt.HTMLUnescape(m[1]))] = true
		}
	}
	scan(swaConfig)
	pyfmt.WalkFiles(s.out, func(rel string) {
		for _, ext := range []string{".html", ".css", ".js", ".xml"} {
			if strings.HasSuffix(rel, ext) {
				raw, err := os.ReadFile(s.out + "/" + rel)
				mustOK(err)
				scan(pyfmt.DecodeReplace(raw)) // 外部ライブラリの JS は UTF-8 とは限らない
				return
			}
		}
	})
	removed := int64(0)
	base := s.out + "/" + s.opts.PruneUploads
	if pyfmt.IsDir(base) {
		pyfmt.WalkFiles(base, func(rel string) {
			if !used[rel] {
				p := base + "/" + rel
				st, err := os.Stat(p)
				mustOK(err)
				removed += st.Size()
				mustOK(os.Remove(p))
			}
		})
	}
	fmt.Fprintf(s.stdout, "使われていない WordPress の画像を外した: %dMB\n", removed/1048576)
}

// swapCSS は出力に写した CSS（*.css）の背景画像なども、画像の差し替えで置き換える。
// 差し替えが無ければ何もしない（CSS の改行も変えない）
func (s *Site) swapCSS() {
	if len(s.opts.ImageSwaps) == 0 {
		return
	}
	pyfmt.WalkFiles(s.out, func(rel string) {
		if strings.HasSuffix(rel, ".css") {
			p := s.out + "/" + rel
			pyfmt.WriteText(p, SwapImages(pyfmt.ReadText(p), s.opts.ImageSwaps)) // 読み切ってから書く
		}
	})
}
