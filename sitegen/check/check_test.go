package check

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EQUATION-CO-LTD/static-site-kit/sitegen"
)

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

const swaConfig = `{"routes":[{"route":"/old/","redirect":"/new/","statusCode":301},{"route":"/*","allowedRoles":["tester"]}]}`

// site は site.json を置いたサイトのディレクトリ
func site(t *testing.T, conf string) string {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"site.json": conf})
	return dir
}

func TestResolvedLinksPass(t *testing.T) {
	dist := t.TempDir()
	writeFiles(t, dist, map[string]string{
		"staticwebapp.config.json": swaConfig,
		"index.html": `<a href="/about/">a</a><a href="/x">x</a><a href="/old/">r</a><a href="/.auth/login/aad">l</a><a href="/img/%E6%97%A5.png">i</a>` +
			`<a href="https://example.com/z">e</a><a href="mailto:a@b">m</a><a href='#top'>t</a><a href="/about/?x=1#h">q</a><!-- <a href="/commented-out/">c</a> -->` +
			`<a href="?#">known</a><a href="/legacy/">known</a>`,
		"about/index.html": `<img src="/img/日.png">`,
		"x.html":           ``,
		"img/日.png":        `png`,
	})
	broken, err := Check(Options{Dist: dist, Dir: site(t, `{"link_check_known": ["?#", "/legacy/"]}`)})
	if err != nil || len(broken) != 0 {
		t.Errorf("正しいリンクだけなのに壊れた参照 %v %v", broken, err)
	}
}

func TestBrokenLinksAreReported(t *testing.T) {
	dist := t.TempDir()
	writeFiles(t, dist, map[string]string{
		"staticwebapp.config.json": swaConfig,
		"index.html":               `<a href="/missing/">m</a><a href="relative.html">r</a><a href="/about/">ok</a><img src='/img/none.png'>`,
		"about/index.html":         ``,
	})
	broken, err := Check(Options{Dist: dist, Dir: site(t, `{}`)})
	if err != nil {
		t.Fatal(err)
	}
	want := "index.html: リンク切れ /missing/|index.html: 相対パスのまま relative.html|index.html: リンク切れ /img/none.png"
	if got := strings.Join(broken, "|"); got != want {
		t.Errorf("broken = %s\nwant %s", got, want)
	}
}

func TestSkipPrefixAndKnownList(t *testing.T) {
	dist := t.TempDir()
	writeFiles(t, dist, map[string]string{
		"staticwebapp.config.json": swaConfig,
		"index.html":               `<a href="/known-broken/">k</a>`,
		"mirror/a.html":            `<a href="/anything/">x</a>`,
	})
	broken, err := Check(Options{Dist: dist, Dir: site(t, `{"link_check_skip": ["mirror/"], "link_check_known": ["/known-broken/"]}`)})
	if err != nil || len(broken) != 0 {
		t.Errorf("skip と known は数えない: %v %v", broken, err)
	}
	broken, _ = Check(Options{Dist: dist, Dir: site(t, `{}`)})
	if len(broken) != 2 {
		t.Errorf("設定が無ければ 2 件: %v", broken)
	}
}

// static を写していないときは、Static のディレクトリでも探す
func TestStaticDirIsSearchedWhenGiven(t *testing.T) {
	dist, static := t.TempDir(), t.TempDir()
	writeFiles(t, dist, map[string]string{"staticwebapp.config.json": swaConfig, "index.html": `<img src="/img/a.png"><link href="/css/a.css">`})
	writeFiles(t, static, map[string]string{"img/a.png": "x"})
	d := site(t, `{}`)
	broken, _ := Check(Options{Dist: dist, Dir: d, Static: static})
	if len(broken) != 1 || !strings.Contains(broken[0], "/css/a.css") {
		t.Errorf("static にある物は解決する: %v", broken)
	}
	broken, _ = Check(Options{Dist: dist, Dir: d})
	if len(broken) != 2 {
		t.Errorf("Static を指定しなければ 2 件: %v", broken)
	}
}

func TestErrors(t *testing.T) {
	var ue *sitegen.UserError
	if _, err := Check(Options{Dist: t.TempDir(), Dir: t.TempDir()}); !errors.As(err, &ue) || !strings.Contains(ue.Msg, "site.json がない") {
		t.Errorf("site.json が無い: %v", err)
	}
	var ie *sitegen.InternalError
	if _, err := Check(Options{Dist: t.TempDir(), Dir: site(t, `{}`)}); !errors.As(err, &ie) || !strings.Contains(err.Error(), "staticwebapp.config.json") {
		t.Errorf("SWA の設定が無い: %v", err)
	}
	dist := t.TempDir()
	writeFiles(t, dist, map[string]string{"staticwebapp.config.json": swaConfig})
	os.WriteFile(dist+"/bad.html", []byte("\xff\xfe"), 0o644)
	if _, err := Check(Options{Dist: dist, Dir: site(t, `{}`)}); !errors.As(err, &ie) || !strings.Contains(err.Error(), "UTF-8") {
		t.Errorf("UTF-8 でない HTML: %v", err)
	}
}

func TestReport(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Report(nil, &out, &errOut); code != 0 || out.String() != "リンク・参照: すべて解決\n" || errOut.Len() != 0 {
		t.Errorf("成功: %d %q %q", code, out.String(), errOut.String())
	}
	out.Reset()
	var many []string
	for i := 0; i < 60; i++ {
		many = append(many, fmt.Sprintf("p%02d.html: リンク切れ /x", i))
	}
	if code := Report(many, &out, &errOut); code != 1 || errOut.String() != "壊れた参照が 60 件あります\n" {
		t.Errorf("失敗: %d %q", code, errOut.String())
	}
	if lines := strings.Split(strings.TrimSpace(out.String()), "\n"); len(lines) != 50 || lines[0] != many[0] || lines[49] != many[49] {
		t.Errorf("標準出力は先頭の 50 件: %d 行", len(lines))
	}
}
