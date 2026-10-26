// Package check は、ビルド結果（出力ディレクトリ）のサイト内リンクと画像・CSS の参照がすべて解決するかを確かめる。
// 参照はすべてルート絶対パス（/company など）で書く約束なので、相対パスが残っていれば書き間違い。
// 壊れた参照が 1 つでもあれば公開しない（終了コードを 1 にする）ために使う。
package check

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/EQUATION-CO-LTD/static-site-kit/sitegen"
	"github.com/EQUATION-CO-LTD/static-site-kit/sitegen/pyfmt"
)

// Options は Check の設定
type Options struct {
	// Dist は検査する出力ディレクトリ（既定 "dist"）
	Dist string
	// Dir は site.json のあるサイトのディレクトリ（既定 "."）。link_check_skip と link_check_known を読む
	Dir string
	// Static が空でなければ、出力に無いファイルをここ（static/ のパス）でも探す。static を写さずに作ったときに使う
	Static string
}

var (
	commentRe  = regexp.MustCompile(`(?s)<!--.*?-->`)
	refAttrRe  = regexp.MustCompile(`(?:href|src)=(?:"([^"]+)"|'([^']+)')`)
	externalRe = regexp.MustCompile(`^(https?:|//|mailto:|tel:|javascript:|#)`)
)

// Check は出力の HTML の href と src を調べ、壊れた参照の一覧（「ページ: 理由 参照」）を返す。すべて解決すれば空。
// site.json の link_check_skip（検査しない配下の前方一致）と link_check_known（既知のリンク切れ。増えたら止まる）を読む。
// 転送元（staticwebapp.config.json の redirect を持つ route）と /.auth/ は解決済みとする。
// 一覧の順は出力ディレクトリの名前順
func Check(o Options) (broken []string, err error) {
	defer sitegen.Recover(&err)
	dist, dir := o.Dist, o.Dir
	if dist == "" {
		dist = "dist"
	}
	if dir == "" {
		dir = "."
	}
	confV, ok := pyfmt.LoadJSONFile(dir + "/site.json")
	if !ok {
		sitegen.Die("site.json がない（サイトのディレクトリ %s を確かめる）", dir)
	}
	conf := pyfmt.AsObj(confV)
	var skip []string // 検査しない配下（別サイトの複製など）
	for _, s := range pyfmt.AsList(pyfmt.Getd(conf, "link_check_skip", []any{})) {
		skip = append(skip, pyfmt.Str(s))
	}
	known := map[string]bool{} // 前のサイトから引き継いだ既知のリンク切れ。増えたら止まる
	for _, s := range pyfmt.AsList(pyfmt.Getd(conf, "link_check_known", []any{})) {
		known[pyfmt.Str(s)] = true
	}
	redirects := map[string]bool{}
	cfg, ok := pyfmt.LoadJSONFile(dist + "/staticwebapp.config.json")
	if !ok {
		panic("FileNotFoundError: staticwebapp.config.json")
	}
	for _, r := range pyfmt.AsObjs(pyfmt.AsObj(cfg).At("routes")) {
		if r.Has("redirect") {
			redirects[pyfmt.Str(r.At("route"))] = true
		}
	}

	resolves := func(u string) bool {
		path, _, _ := strings.Cut(u, "#")
		path, _, _ = strings.Cut(path, "?")
		path = pyfmt.Unquote(path)
		if path == "" || path == "/" || redirects[path] || strings.HasPrefix(path, "/.auth/") { // /.auth/ は SWA が応答する（ログイン・ログアウト）
			return true
		}
		p := strings.TrimLeft(path, "/")
		// SWA は /x に x.html か x/index.html を返す
		if o.Static != "" && pyfmt.IsFile(pyfmt.Join(o.Static, p)) {
			return true
		}
		return pyfmt.IsFile(pyfmt.Join(dist, p)) || pyfmt.IsFile(pyfmt.Join(dist, p+".html")) || pyfmt.IsFile(pyfmt.Join(dist, pyfmt.Join(p, "index.html")))
	}

	pyfmt.WalkFiles(dist, func(page string) {
		if !strings.HasSuffix(page, ".html") {
			return
		}
		for _, s := range skip {
			if strings.HasPrefix(page, s) {
				return
			}
		}
		raw, err := os.ReadFile(dist + "/" + page)
		if err != nil {
			panic(err.Error())
		}
		if !utf8.Valid(raw) {
			panic(fmt.Sprintf("UnicodeDecodeError: %s は UTF-8 として読めない", page))
		}
		text := commentRe.ReplaceAllString(pyfmt.UniversalNewlines(string(raw)), "") // コメントアウトされた部分は見ない
		for _, m := range refAttrRe.FindAllStringSubmatch(text, -1) {
			u := m[1]
			if u == "" {
				u = m[2]
			}
			if known[u] || externalRe.MatchString(u) {
				continue
			}
			if !strings.HasPrefix(u, "/") {
				broken = append(broken, fmt.Sprintf("%s: 相対パスのまま %s", page, u))
			} else if !resolves(u) {
				broken = append(broken, fmt.Sprintf("%s: リンク切れ %s", page, u))
			}
		}
	})
	return broken, nil
}

// maxShown は Report が標準出力に出す壊れた参照の上限
const maxShown = 50

// Report は Check の結果を出して終了コードを返す。壊れた参照があれば先頭の 50 件を stdout、件数を stderr に出して 1。
// なければ「すべて解決」を stdout に出して 0
func Report(broken []string, stdout, stderr io.Writer) int {
	if len(broken) > 0 {
		fmt.Fprintln(stdout, strings.Join(broken[:min(maxShown, len(broken))], "\n"))
		fmt.Fprintf(stderr, "壊れた参照が %d 件あります\n", len(broken))
		return 1
	}
	fmt.Fprintln(stdout, "リンク・参照: すべて解決")
	return 0
}
