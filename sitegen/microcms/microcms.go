// Package microcms は、microCMS から API の中身を取得して JSON ファイルに書き出す（GET だけ）。
//
// 汎用の処理（ページ送りで全件取得・画像を幅 1600 に縮めて保存して URL を置き換え・参照されなくなった画像の削除・
// .env の読み込み・JSON の書き出し・オブジェクト API が未作成（404）のときの扱い）をここで持ち、
// 取る API の一覧・日付の整え方・取得後の整形は Config で差し込む。
//
// 書き出す形: リスト型は {"contents": [...], "totalCount", "offset", "limit"}、オブジェクト型は応答そのまま。
// UTF-8・インデント 1・非 ASCII はそのまま（pyfmt.JSONFileText）。
//
// ネットワークに触る所は Fetcher.get（API）と Fetcher.download（画像）の 2 つだけで、Fetcher.Client を差し替えて試せる。
package microcms

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/EQUATION-CO-LTD/static-site-kit/sitegen"
	"github.com/EQUATION-CO-LTD/static-site-kit/sitegen/pyfmt"
)

// DefaultImgDir は画像の保存先の既定（公開パスは /images/cms/）
const DefaultImgDir = "static/images/cms"

// publicPrefix は保存した画像の公開パス
const publicPrefix = "/images/cms/"

// imgWidth は画像を保存するときの幅の上限（microCMS の画像 API で縮める。拡大はしない）
const imgWidth = 1600

// Result は 1 つの API の取得結果（Name.json に書き出す）
type Result struct {
	Name string
	Data any
}

// Config は取得の設定。サイト固有の部分はここで差し込む
type Config struct {
	// Lists はリスト型 API の名前。全件を取って {"contents", "totalCount", "offset", "limit"} に包む
	Lists []string
	// Objects はオブジェクト型 API の名前。まだ作られていない（404）ものは書き出さない
	Objects []string
	// ImgDir は画像の保存先（既定 DefaultImgDir）。ここにある画像のうち、今回参照されなかった物は消す
	ImgDir string
	// Normalize は、画像を置き換えた後の項目（リスト型は 1 件ごと、オブジェクト型は全体）を整える。
	// 日付を JST にそろえる、独自の形に整える、など。nil なら何もしない。返した物を書き出す
	Normalize func(api string, item *pyfmt.Obj) *pyfmt.Obj
	// Finish は、すべての API が取れた後、書き出す前に結果全体を加工する。結果を足してもよい（画像の対応表など）。nil なら何もしない
	Finish func(results []Result) ([]Result, error)
}

// StatusError は 2xx 以外の応答
type StatusError struct {
	Code int
	URL  string
}

// Error は HTTP のステータスと URL
func (e *StatusError) Error() string { return fmt.Sprintf("HTTP Error %d: %s", e.Code, e.URL) }

// Fetcher は 1 回の取得の状態
type Fetcher struct {
	// Client は通信に使うクライアント（試験では RoundTripper を差し替える）
	Client *http.Client
	// Out は進捗の出力先（既定は標準出力）
	Out io.Writer

	cfg    Config
	domain string
	key    string
	used   map[string]bool // 今回の取得で参照された画像。これ以外は microCMS で消されたものとして削除する
}

// New は取得の準備をする。domain は microCMS のサービスドメイン、key は読み取り専用の API キー
func New(domain, key string, cfg Config) *Fetcher {
	if cfg.ImgDir == "" {
		cfg.ImgDir = DefaultImgDir
	}
	return &Fetcher{Client: &http.Client{Timeout: 30 * time.Second}, Out: os.Stdout, cfg: cfg, domain: domain, key: key, used: map[string]bool{}}
}

const imgURLPattern = `https://images\.microcms-assets\.io/assets/[^/]+/([^/?"` + pyfmt.Space + `]+)/([^/?"` + pyfmt.Space + `]+)`

var (
	imgMatchRe = regexp.MustCompile(`^` + imgURLPattern) // 先頭から
	imgFindRe  = regexp.MustCompile(imgURLPattern)       // 文中のすべて
	// ファイル名に使える文字は Unicode の英数字・_ . - だけ。それ以外は _ に
	badNameRe = regexp.MustCompile(`[^` + pyfmt.Word + `.\-]`)
)

func must(err error) {
	if err != nil {
		panic(err.Error())
	}
}

// get は API の GET。params は limit=100&offset=0 のようにこの順で付ける（urlencode と同じ）
func (f *Fetcher) get(endpoint string, params [][2]string) any {
	u := fmt.Sprintf("https://%s.microcms.io/api/v1/%s", f.domain, endpoint)
	if len(params) > 0 {
		q := make([]string, len(params))
		for i, p := range params {
			q[i] = url.QueryEscape(p[0]) + "=" + url.QueryEscape(p[1])
		}
		u += "?" + strings.Join(q, "&")
	}
	req, err := http.NewRequest("GET", u, nil)
	must(err)
	req.Header.Set("X-MICROCMS-API-KEY", f.key)
	resp, err := f.Client.Do(req)
	must(err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	must(err)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		panic(&StatusError{resp.StatusCode, u})
	}
	v, err := pyfmt.ParseJSON(string(body))
	must(err)
	return v
}

// download は画像を path に保存する。途中で失敗した壊れたファイルを「保存済み」と見なさないよう、呼び出し側が一時ファイルを渡す
func (f *Fetcher) download(src, path string) {
	resp, err := f.Client.Get(src)
	must(err)
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		panic(&StatusError{resp.StatusCode, src})
	}
	out, err := os.Create(path)
	must(err)
	if _, err := io.Copy(out, resp.Body); err != nil {
		out.Close()
		panic(err.Error())
	}
	must(out.Close())
}

// getAll はリスト型 API の全件。100 件ずつ offset を進めて取る
func (f *Fetcher) getAll(endpoint string) []any {
	var items []any
	offset := 0
	for {
		res := pyfmt.AsObj(f.get(endpoint, [][2]string{{"limit", "100"}, {"offset", fmt.Sprint(offset)}}))
		items = append(items, pyfmt.AsList(res.At("contents"))...)
		offset += 100
		if float64(offset) >= pyfmt.AsFloat(res.At("totalCount")) {
			return items
		}
	}
}

// LocalImageName は microCMS の画像 URL → 保存するファイル名（{ID}-{ファイル名}）。空白・記号は URL に出したくないので _ にする
func LocalImageName(imageURL string) string {
	m := imgMatchRe.FindStringSubmatch(imageURL)
	if m == nil {
		panic(fmt.Sprintf("ValueError: 想定外の画像 URL: %s", imageURL))
	}
	return m[1] + "-" + badNameRe.ReplaceAllString(pyfmt.Unquote(m[2]), "_")
}

// localImage は microCMS の画像を画像の保存先に保存して公開パス（/images/cms/…）を返す。保存済みならダウンロードしない
func (f *Fetcher) localImage(imageURL string) string {
	name := LocalImageName(imageURL)
	path := f.cfg.ImgDir + "/" + name
	if !pyfmt.FileExists(path) {
		must(os.MkdirAll(f.cfg.ImgDir, 0o755))
		// 画像 API で幅を抑えて取る（元画像が巨大でも配信サイズを抑える。拡大はしない）
		src, _, _ := strings.Cut(imageURL, "?")
		f.download(fmt.Sprintf("%s?w=%d&fit=max", src, imgWidth), path+".tmp")
		must(os.Rename(path+".tmp", path))
	}
	f.used[path] = true
	return publicPrefix + name
}

// localize は値の中の microCMS 画像を再帰的にローカルのパスへ
func (f *Fetcher) localize(value any) any {
	switch v := value.(type) {
	case *pyfmt.Obj:
		out := pyfmt.NewObj()
		for _, k := range v.Keys() {
			out.Set(k, f.localize(v.Get(k)))
		}
		if s, ok := out.Get("url").(string); ok && imgMatchRe.MatchString(s) {
			out.Set("url", f.localImage(s))
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, e := range v {
			out[i] = f.localize(e)
		}
		return out
	case string:
		if strings.Contains(v, "images.microcms-assets.io") {
			return replaceAllFunc(imgFindRe, v, f.localImage)
		}
	}
	return value
}

func replaceAllFunc(re *regexp.Regexp, s string, fn func(string) string) string {
	var sb strings.Builder
	last := 0
	for _, m := range re.FindAllStringIndex(s, -1) {
		sb.WriteString(s[last:m[0]])
		sb.WriteString(fn(s[m[0]:m[1]]))
		last = m[1]
	}
	sb.WriteString(s[last:])
	return sb.String()
}

// JSTDate は日時 → 日本時間の日付に直した "YYYY-MM-DDT00:00:00.000Z"（microCMS の日付項目をそろえるのに使う）。
// タイムゾーンの無い値は UTC として読む（microCMS は常に Z 付きで返す）
func JSTDate(iso string) string {
	s := strings.Replace(iso, "Z", "+00:00", 1)
	var t time.Time
	var err error
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err = time.ParseInLocation(layout, s, time.UTC); err == nil {
			return t.In(sitegen.JST).Format("2006-01-02") + "T00:00:00.000Z"
		}
	}
	panic(fmt.Sprintf("ValueError: Invalid isoformat string: %s", pyfmt.Repr(iso)))
}

func (f *Fetcher) normalize(api string, item *pyfmt.Obj) *pyfmt.Obj {
	if f.cfg.Normalize == nil {
		return item
	}
	return f.cfg.Normalize(api, item)
}

func writeJSONFile(outDir, name string, data any) {
	must(os.MkdirAll(outDir, 0o755))
	path := outDir + "/" + name + ".json"
	pyfmt.WriteText(path+".tmp", pyfmt.JSONFileText(data))
	must(os.Rename(path+".tmp", path))
}

var envLineRe = regexp.MustCompile(`^[` + pyfmt.Space + `]*([A-Z_]+)[` + pyfmt.Space + `]*=[` + pyfmt.Space + `]*(.*?)[` + pyfmt.Space + `]*$`)

// LoadEnv は接続情報（MICROCMS_SERVICE_DOMAIN と MICROCMS_API_KEY）を環境変数か、rootDir/.env から読む（環境変数が優先）。
// どちらかが無ければ *sitegen.UserError
func LoadEnv(rootDir string) (domain, key string, err error) {
	defer sitegen.Recover(&err)
	if f, err := os.Open(rootDir + "/.env"); err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if m := envLineRe.FindStringSubmatch(sc.Text()); m != nil {
				if _, set := os.LookupEnv(m[1]); !set {
					os.Setenv(m[1], pyfmt.StripChars(m[2], "\"'"))
				}
			}
		}
	}
	if os.Getenv("MICROCMS_SERVICE_DOMAIN") == "" || os.Getenv("MICROCMS_API_KEY") == "" {
		sitegen.Die("MICROCMS_SERVICE_DOMAIN と MICROCMS_API_KEY が要る（環境変数か .env）")
	}
	return os.Getenv("MICROCMS_SERVICE_DOMAIN"), os.Getenv("MICROCMS_API_KEY"), nil
}

// Run は設定の API をすべて取得して outDir/{api}.json に書き出す。
// すべて取れてから書く（途中で失敗して中途半端な組にならないように）。取得後、参照されなくなった画像を消す。
// 通信の失敗は *StatusError などを含む *sitegen.InternalError（errors.As で取り出せる）
func (f *Fetcher) Run(outDir string) (err error) {
	defer sitegen.Recover(&err)
	if f.Out == nil {
		f.Out = os.Stdout
	}
	var results []Result
	for _, api := range f.cfg.Lists {
		var items []any
		for _, c := range f.getAll(api) {
			items = append(items, f.normalize(api, pyfmt.AsObj(f.localize(c))))
		}
		if items == nil {
			items = []any{}
		}
		results = append(results, Result{api, pyfmt.O("contents", items, "totalCount", pyfmt.IntNum(len(items)), "offset", pyfmt.IntNum(0), "limit", pyfmt.IntNum(len(items)))})
		fmt.Fprintf(f.Out, "%s: %d 件\n", api, len(items))
	}
	for _, api := range f.cfg.Objects {
		if data, ok := f.getObject(api); ok {
			results = append(results, Result{api, data})
			fmt.Fprintf(f.Out, "%s: 取得\n", api)
		} else {
			fmt.Fprintf(f.Out, "%s: まだ作られていない（404）。書き出さない\n", api)
		}
	}
	if f.cfg.Finish != nil {
		r, err := f.cfg.Finish(results)
		if err != nil {
			return err
		}
		results = r
	}

	for _, r := range results {
		writeJSONFile(outDir, r.Name, r.Data)
	}

	// microCMS で削除・差し替えられた画像を消す
	if pyfmt.IsDir(f.cfg.ImgDir) {
		for _, name := range pyfmt.ListDirSorted(f.cfg.ImgDir) {
			path := f.cfg.ImgDir + "/" + name
			if !f.used[path] {
				if pyfmt.IsDir(path) {
					panic("IsADirectoryError: " + path)
				}
				must(os.Remove(path))
				fmt.Fprintf(f.Out, "削除: images/cms/%s\n", name)
			}
		}
	}
	return nil
}

// getObject はオブジェクト API。まだ作られていない（404）なら ok=false
func (f *Fetcher) getObject(api string) (data any, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			if e, isStatus := r.(*StatusError); isStatus && e.Code == 404 {
				data, ok = nil, false
				return
			}
			panic(r)
		}
	}()
	return f.normalize(api, pyfmt.AsObj(f.localize(f.get(api, nil)))), true
}
