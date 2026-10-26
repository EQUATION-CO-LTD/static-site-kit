package microcms

// 通信は差し替えた RoundTripper が受ける（ネットワークに触れない）。データはすべて架空。

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/EQUATION-CO-LTD/static-site-kit/sitegen"
	"github.com/EQUATION-CO-LTD/static-site-kit/sitegen/pyfmt"
)

type stubTransport func(*http.Request) (*http.Response, error)

func (s stubTransport) RoundTrip(r *http.Request) (*http.Response, error) { return s(r) }

func reply(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Status: fmt.Sprint(code), Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}
}

// stub は架空の microCMS。API 名 → 応答の JSON、404 にする API、受けた問い合わせを覚える
type stub struct {
	mu       sync.Mutex
	raw      map[string]string
	notFound map[string]bool
	imgReqs  []string
	apiReqs  []string
	apiKey   string
}

func (s *stub) client() *http.Client {
	return &http.Client{Transport: stubTransport(func(r *http.Request) (*http.Response, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Host, ".microcms.io"):
			ep := strings.TrimPrefix(r.URL.Path, "/api/v1/")
			s.apiReqs = append(s.apiReqs, ep+"?"+r.URL.RawQuery)
			s.apiKey = r.Header.Get("X-MICROCMS-API-KEY")
			if s.notFound[ep] {
				return reply(404, `{"message":"not found"}`), nil
			}
			return reply(200, s.raw[ep]), nil
		case r.URL.Host == "images.microcms-assets.io":
			s.imgReqs = append(s.imgReqs, r.URL.String())
			if strings.Contains(r.URL.Path, "broken") {
				return reply(500, "error"), nil
			}
			return reply(200, "IMG:"+r.URL.Path), nil
		}
		return nil, fmt.Errorf("想定外の宛先: %s", r.URL)
	})}
}

const imgBase = "https://images.microcms-assets.io/assets/svc/"

func newStub() *stub {
	img1, img2 := imgBase+"AAA/%E5%86%99%E7%9C%9F%201.png", imgBase+"BBB/photo.JPG"
	return &stub{notFound: map[string]bool{}, raw: map[string]string{
		"articles": `{"contents":[
			{"id":"a1","publishedAt":"2026-09-24T15:00:00.000Z","title":"記事 & 1","cover":{"url":"` + img1 + `","width":100},"body":"<p><img src=\"` + img2 + `\"> と <img src=\"` + img1 + `\"></p>"},
			{"id":"a2","publishedAt":"2026-10-01T00:00:00.000Z","date":"2026-09-30T16:00:00.000Z","n":1.5,"flag":true,"none":null}
		],"totalCount":2,"offset":0,"limit":100}`,
		"notices":  `{"contents":[],"totalCount":0,"offset":0,"limit":100}`,
		"settings": `{"name":"架空の設定","logo":{"url":"` + img2 + `"},"events":[{"date":"2026-11-05T15:00:00.000Z"},{"label":"随時"}]}`,
	}}
}

// config は試験用の設定（日付の整形つき）。画像の保存先は dir/img
func config(dir string) Config {
	return Config{
		Lists:   []string{"articles", "notices"},
		Objects: []string{"settings"},
		ImgDir:  dir + "/img",
		Normalize: func(api string, item *pyfmt.Obj) *pyfmt.Obj {
			switch api {
			case "articles":
				item.Set("date", JSTDate(pyfmt.Str(pyfmt.OrKey(item, "date", "publishedAt"))))
			case "settings":
				for _, e := range pyfmt.AsObjs(item.Get("events")) {
					if pyfmt.Truthy(e.Get("date")) {
						e.Set("date", JSTDate(pyfmt.Str(e.At("date"))))
					}
				}
			}
			return item
		},
	}
}

func newFetcher(s *stub, cfg Config) (*Fetcher, *strings.Builder) {
	f := New("example", "KEY", cfg)
	f.Client = s.client()
	var out strings.Builder
	f.Out = &out
	return f, &out
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRunWritesJSONAndSavesImages(t *testing.T) {
	dir := t.TempDir()
	s := newStub()
	f, out := newFetcher(s, config(dir))
	// 前回の取得で残った画像（今回は参照されない）は消える
	os.MkdirAll(dir+"/img", 0o755)
	os.WriteFile(dir+"/img/stale.png", []byte("x"), 0o644)
	if err := f.Run(dir + "/cms"); err != nil {
		t.Fatal(err)
	}

	wantArticles := `{
 "contents": [
  {
   "id": "a1",
   "publishedAt": "2026-09-24T15:00:00.000Z",
   "title": "記事 & 1",
   "cover": {
    "url": "/images/cms/AAA-写真_1.png",
    "width": 100
   },
   "body": "<p><img src=\"/images/cms/BBB-photo.JPG\"> と <img src=\"/images/cms/AAA-写真_1.png\"></p>",
   "date": "2026-09-25T00:00:00.000Z"
  },
  {
   "id": "a2",
   "publishedAt": "2026-10-01T00:00:00.000Z",
   "date": "2026-10-01T00:00:00.000Z",
   "n": 1.5,
   "flag": true,
   "none": null
  }
 ],
 "totalCount": 2,
 "offset": 0,
 "limit": 2
}
`
	if got := read(t, dir+"/cms/articles.json"); got != wantArticles {
		t.Errorf("articles.json =\n%s\nwant\n%s", got, wantArticles)
	}
	if got := read(t, dir+"/cms/notices.json"); got != "{\n \"contents\": [],\n \"totalCount\": 0,\n \"offset\": 0,\n \"limit\": 0\n}\n" {
		t.Errorf("空のリスト型も包んで書く: %s", got)
	}
	wantSettings := "{\n \"name\": \"架空の設定\",\n \"logo\": {\n  \"url\": \"/images/cms/BBB-photo.JPG\"\n },\n \"events\": [\n  {\n   \"date\": \"2026-11-06T00:00:00.000Z\"\n  },\n  {\n   \"label\": \"随時\"\n  }\n ]\n}\n"
	if got := read(t, dir+"/cms/settings.json"); got != wantSettings {
		t.Errorf("settings.json =\n%s\nwant\n%s", got, wantSettings)
	}

	ents, _ := os.ReadDir(dir + "/img")
	var imgs []string
	for _, e := range ents {
		imgs = append(imgs, e.Name())
	}
	sort.Strings(imgs)
	if strings.Join(imgs, "|") != "AAA-写真_1.png|BBB-photo.JPG" {
		t.Errorf("保存した画像 %q（stale.png は消える）", imgs)
	}
	if got := read(t, dir+"/img/AAA-写真_1.png"); got != "IMG:/assets/svc/AAA/写真 1.png" {
		t.Errorf("画像の中身 %q", got)
	}
	// 幅 1600 で取る。同じ画像は 1 度だけ
	if len(s.imgReqs) != 2 {
		t.Errorf("画像の取得 %d 回: %q", len(s.imgReqs), s.imgReqs)
	}
	for _, u := range s.imgReqs {
		if !strings.HasSuffix(u, "?w=1600&fit=max") {
			t.Errorf("画像の取得 URL に幅の指定が無い: %s", u)
		}
	}
	if s.apiKey != "KEY" {
		t.Errorf("API キーのヘッダが付いていない: %q", s.apiKey)
	}
	if want := "articles: 2 件\nnotices: 0 件\nsettings: 取得\n削除: images/cms/stale.png\n"; out.String() != want {
		t.Errorf("進捗の出力 %q", out.String())
	}
	if ents, _ := os.ReadDir(dir + "/cms"); len(ents) != 3 {
		t.Errorf("書いたファイルの数 %d（.tmp を残さない）", len(ents))
	}

	// 2 回目は、保存済みの画像を取り直さない
	s.imgReqs = nil
	f, _ = newFetcher(s, config(dir))
	if err := f.Run(dir + "/cms"); err != nil || len(s.imgReqs) != 0 {
		t.Errorf("保存済みの画像は取り直さない: %v %q", err, s.imgReqs)
	}
}

// 100 件を超える API は offset を進めて取り直す
func TestPagination(t *testing.T) {
	var reqs []string
	f := New("example", "KEY", Config{Lists: []string{"articles"}})
	f.Client = &http.Client{Transport: stubTransport(func(r *http.Request) (*http.Response, error) {
		reqs = append(reqs, r.URL.RawQuery)
		offset := 0
		fmt.Sscanf(r.URL.Query().Get("offset"), "%d", &offset)
		n := min(100, 250-offset)
		items := make([]string, n)
		for i := range items {
			items[i] = fmt.Sprintf(`{"id":"i%d"}`, offset+i)
		}
		return reply(200, fmt.Sprintf(`{"contents":[%s],"totalCount":250,"offset":%d,"limit":100}`, strings.Join(items, ","), offset)), nil
	})}
	got := f.getAll("articles")
	if len(got) != 250 || strings.Join(reqs, " ") != "limit=100&offset=0 limit=100&offset=100 limit=100&offset=200" {
		t.Errorf("取得 %d 件、問い合わせ %q", len(got), reqs)
	}
	if id := pyfmt.Str(pyfmt.AsObj(got[249]).At("id")); id != "i249" {
		t.Errorf("最後の項目 %s", id)
	}
}

// オブジェクト型がまだ作られていない（404）ときは何も書かない。リスト型の 404 は止まる
func TestObjectAPI404IsSkippedButListAPI404Stops(t *testing.T) {
	dir := t.TempDir()
	s := newStub()
	s.notFound["settings"] = true
	f, out := newFetcher(s, config(dir))
	if err := f.Run(dir + "/cms"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir + "/cms/settings.json"); err == nil {
		t.Errorf("404 の settings.json は書かない")
	}
	if _, err := os.Stat(dir + "/cms/articles.json"); err != nil {
		t.Errorf("他の API は書く")
	}
	if !strings.Contains(out.String(), "settings: まだ作られていない（404）。書き出さない") {
		t.Errorf("出力 %q", out.String())
	}

	s = newStub()
	s.notFound["articles"] = true
	f, _ = newFetcher(s, config(dir))
	err := f.Run(dir + "/cms2")
	var se *StatusError
	if !errors.As(err, &se) || se.Code != 404 {
		t.Errorf("articles の 404 は止まるはず: %v", err)
	}
	if _, err := os.Stat(dir + "/cms2"); err == nil {
		t.Errorf("途中で失敗したら、何も書かない")
	}
}

func TestImageDownloadFailureStopsWithoutSavingBrokenFile(t *testing.T) {
	dir := t.TempDir()
	s := newStub()
	s.raw["articles"] = `{"contents":[{"id":"a","cover":{"url":"` + imgBase + `CCC/broken.png"}}],"totalCount":1}`
	f, _ := newFetcher(s, config(dir))
	err := f.Run(dir + "/cms")
	var se *StatusError
	if !errors.As(err, &se) || se.Code != 500 {
		t.Fatalf("画像の取得失敗で止まるはず: %v", err)
	}
	if _, err := os.Stat(dir + "/img/CCC-broken.png"); err == nil {
		t.Errorf("壊れた画像を保存済みとして残した")
	}
}

func TestFinishCanAddResults(t *testing.T) {
	dir := t.TempDir()
	s := newStub()
	cfg := config(dir)
	cfg.Finish = func(res []Result) ([]Result, error) {
		var names []string
		for _, r := range res {
			names = append(names, r.Name)
		}
		return append(res, Result{"summary", pyfmt.O("names", strings.Join(names, ","))}), nil
	}
	f, _ := newFetcher(s, cfg)
	if err := f.Run(dir + "/cms"); err != nil {
		t.Fatal(err)
	}
	if got := read(t, dir+"/cms/summary.json"); got != "{\n \"names\": \"articles,notices,settings\"\n}\n" {
		t.Errorf("summary.json = %s", got)
	}

	// Finish の誤りは、何も書かずに返る
	dir2 := t.TempDir()
	boom := errors.New("finish failed")
	cfg = config(dir2)
	cfg.Finish = func([]Result) ([]Result, error) { return nil, boom }
	f, _ = newFetcher(newStub(), cfg)
	if err := f.Run(dir2 + "/cms"); !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
	if _, err := os.Stat(dir2 + "/cms"); err == nil {
		t.Errorf("Finish が失敗したら、何も書かない")
	}
}

func TestWithoutNormalizeItemsAreWrittenAsReceived(t *testing.T) {
	dir := t.TempDir()
	s := newStub()
	f, _ := newFetcher(s, Config{Lists: []string{"articles"}, ImgDir: dir + "/img"})
	if err := f.Run(dir + "/cms"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(read(t, dir+"/cms/articles.json"), `"date": "2026-09-25`) {
		t.Errorf("Normalize が無いのに日付を整えた")
	}
}

func TestLoadEnv(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(dir+"/.env", []byte("# comment\nMICROCMS_SERVICE_DOMAIN = \"abc\"\r\nMICROCMS_API_KEY='k-1'  \nOTHER=x\n"), 0o644)
	t.Setenv("MICROCMS_SERVICE_DOMAIN", "")
	os.Unsetenv("MICROCMS_SERVICE_DOMAIN")
	os.Unsetenv("MICROCMS_API_KEY")
	d, k, err := LoadEnv(dir)
	if err != nil || d != "abc" || k != "k-1" {
		t.Errorf("LoadEnv = %q, %q, %v", d, k, err)
	}
	t.Setenv("MICROCMS_API_KEY", "from-env") // 環境変数が優先
	if _, k, _ := LoadEnv(dir); k != "from-env" {
		t.Errorf("環境変数が優先されない: %q", k)
	}

	// どちらも無ければ、担当者向けの誤り
	empty := t.TempDir()
	os.Unsetenv("MICROCMS_SERVICE_DOMAIN")
	os.Unsetenv("MICROCMS_API_KEY")
	_, _, err = LoadEnv(empty)
	var ue *sitegen.UserError
	if !errors.As(err, &ue) || !strings.Contains(ue.Msg, "MICROCMS_SERVICE_DOMAIN と MICROCMS_API_KEY が要る") {
		t.Errorf("err = %v", err)
	}
}

func TestJSTDate(t *testing.T) {
	for in, want := range map[string]string{
		"2026-09-25T00:00:00.000Z":      "2026-09-25T00:00:00.000Z",
		"2026-09-24T15:00:00.000Z":      "2026-09-25T00:00:00.000Z", // UTC の 15:00 は日本では翌日
		"2026-09-24T14:59:59.999Z":      "2026-09-24T00:00:00.000Z",
		"2026-12-31T23:59:59.999Z":      "2027-01-01T00:00:00.000Z",
		"2027-01-01T00:00:00Z":          "2027-01-01T00:00:00.000Z",
		"2026-09-25T00:00:00+00:00":     "2026-09-25T00:00:00.000Z",
		"2026-09-25T20:00:00.000+09:00": "2026-09-25T00:00:00.000Z",
		"2026-09-24 15:00:00":           "2026-09-25T00:00:00.000Z", // タイムゾーンの無い値は UTC
		"2026-09-25":                    "2026-09-25T00:00:00.000Z",
	} {
		if got := JSTDate(in); got != want {
			t.Errorf("JSTDate(%q) = %q, want %q", in, got, want)
		}
	}
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("読めない値は止まるはず")
		}
	}()
	JSTDate("昨日")
}

func TestLocalImageName(t *testing.T) {
	for in, want := range map[string]string{
		imgBase + "AAA/photo.jpg":                        "AAA-photo.jpg",
		imgBase + "AAA/%E5%86%99%E7%9C%9F%201.png?w=100": "AAA-写真_1.png",
		imgBase + "AAA/a b.png":                          "AAA-a",
		imgBase + "AAA/weird%20name%28x%29%21.JPG":       "AAA-weird_name_x__.JPG",
		imgBase + "AAA/a%2Fb.png":                        "AAA-a_b.png",
		imgBase + "AAA/%E3%83%AD%E3%82%B4_ロゴ.svg":        "AAA-ロゴ_ロゴ.svg",
		imgBase + "AAA/ＡＢＣ１２３.png":                       "AAA-ＡＢＣ１２３.png",
	} {
		if got := LocalImageName(in); got != want {
			t.Errorf("LocalImageName(%q) = %q, want %q", in, got, want)
		}
	}
}

// ImgParams で、API ごとに画像の取得のパラメータ（幅など）を変えられる
func TestImgParamsPerAPI(t *testing.T) {
	dir := t.TempDir()
	s := newStub()
	cfg := config(dir)
	cfg.ImgParams = func(api string) string {
		if api == "settings" {
			return "w=2400"
		}
		return "w=1000"
	}
	f, _ := newFetcher(s, cfg)
	if err := f.Run(dir + "/cms"); err != nil {
		t.Fatal(err)
	}
	// 記事で使う 2 枚は 1000、設定の画像は記事で保存済みなので取り直さない
	if len(s.imgReqs) != 2 {
		t.Fatalf("画像の取得 %q", s.imgReqs)
	}
	for _, u := range s.imgReqs {
		if !strings.HasSuffix(u, "?w=1000") {
			t.Errorf("画像の取得 URL: %s", u)
		}
	}

	// 設定だけが使う画像は 2400
	s2 := newStub()
	s2.raw["articles"] = `{"contents":[],"totalCount":0,"offset":0,"limit":100}`
	dir2 := t.TempDir()
	cfg2 := config(dir2)
	cfg2.ImgParams = cfg.ImgParams
	f, _ = newFetcher(s2, cfg2)
	if err := f.Run(dir2 + "/cms"); err != nil {
		t.Fatal(err)
	}
	if len(s2.imgReqs) != 1 || !strings.HasSuffix(s2.imgReqs[0], "?w=2400") {
		t.Errorf("設定の画像の取得 %q", s2.imgReqs)
	}
}
