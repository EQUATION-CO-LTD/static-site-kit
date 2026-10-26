package pyfmt

// Python 3 の文字列まわりの振る舞いを、Go の標準ライブラリで再現する道具。
// Go の既定と違う所（html.EscapeString の ' や、正規表現の \s \w が ASCII だけなど）を、
// Python で書いた先行実装とバイト単位で同じ出力にするためにここへ集める。

import (
	"fmt"
	"html"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ---------------------------------------------------------------- 正規表現（Python の \s \w）

// Space は Python の正規表現の \s（Unicode の空白。str.isspace() が真の文字）。文字クラス [ ] の中に埋めて使う。Go の \s は ASCII だけ
const Space = `\t\n\x0b\x0c\r\x1c-\x1f \x{85}\x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}`

// Word は Python の正規表現の \w（Unicode の英数字とアンダースコア）。文字クラス [ ] の中に埋めて使う
const Word = `\p{L}\p{N}_`

// IsSpace は Space に含まれる文字か（str.isspace() と同じ）
func IsSpace(r rune) bool {
	switch {
	case r >= 0x09 && r <= 0x0d, r >= 0x1c && r <= 0x20, r == 0x85, r == 0xa0, r == 0x1680,
		r >= 0x2000 && r <= 0x200a, r == 0x2028, r == 0x2029, r == 0x202f, r == 0x205f, r == 0x3000:
		return true
	}
	return false
}

// ---------------------------------------------------------------- str のメソッド

// Strip は str.strip()（引数なし。Unicode の空白を両端から落とす）
func Strip(s string) string { return strings.TrimFunc(s, IsSpace) }

// StripChars は str.strip(chars)
func StripChars(s, chars string) string {
	return strings.Trim(s, chars)
}

// RuneLen は len(str)（コードポイント数）
func RuneLen(s string) int { return utf8.RuneCountInString(s) }

// Slice は s[a:b]（コードポイント単位。範囲外は切り詰める。負の値は使わない）
func Slice(s string, a, b int) string {
	r := []rune(s)
	if a > len(r) {
		a = len(r)
	}
	if b > len(r) {
		b = len(r)
	}
	if a >= b {
		return ""
	}
	return string(r[a:b])
}

// Head は s[:n]（コードポイント単位）
func Head(s string, n int) string { return Slice(s, 0, n) }

// Tail は s[a:]（コードポイント単位）
func Tail(s string, a int) string { return Slice(s, a, 1<<30) }

// SplitLines は str.splitlines()
func SplitLines(s string) []string {
	var out []string
	rs := []rune(s)
	i := 0
	var cur strings.Builder
	for i < len(rs) {
		r := rs[i]
		switch {
		case r == '\r' && i+1 < len(rs) && rs[i+1] == '\n':
			out = append(out, cur.String())
			cur.Reset()
			i += 2
		case r == '\n', r == '\r', r == 0x0b, r == 0x0c, r == 0x1c, r == 0x1d, r == 0x1e, r == 0x85, r == 0x2028, r == 0x2029:
			out = append(out, cur.String())
			cur.Reset()
			i++
		default:
			cur.WriteRune(r)
			i++
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// SplitExt は os.path.splitext(p)[0]。p は区切り文字を含まない名前
func SplitExt(p string) string {
	i := strings.LastIndex(p, ".")
	if i < 0 {
		return p
	}
	// 先頭のドットだけの名前（.hidden）は拡張子なし
	j := 0
	for j < len(p) && p[j] == '.' {
		j++
	}
	if i < j {
		return p
	}
	return p[:i]
}

// ---------------------------------------------------------------- 値の文字列化・真偽

// Truthy は Python の真偽（None・空文字・0・空リスト・空 dict は偽）
func Truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case Num:
		return x.F != 0
	case int:
		return x != 0
	case []any:
		return len(x) > 0
	case []*Obj:
		return len(x) > 0
	case *Obj:
		return x.Len() > 0
	}
	return true
}

// Str は str(v)
func Str(v any) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case bool:
		if x {
			return "True"
		}
		return "False"
	case string:
		return x
	case Num:
		return x.Text
	case int:
		return strconv.Itoa(x)
	}
	return ReprAny(v)
}

// ReprAny は repr(v)。文字列・配列・Obj は Python の表記で、他は Str と同じ
func ReprAny(v any) string {
	switch x := v.(type) {
	case string:
		return Repr(x)
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = ReprAny(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *Obj:
		parts := make([]string, 0, x.Len())
		for _, k := range x.keys {
			parts = append(parts, Repr(k)+": "+ReprAny(x.m[k]))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return Str(v)
}

// Repr は repr(str)
func Repr(s string) string {
	q := byte('\'')
	if strings.Contains(s, "'") && !strings.Contains(s, `"`) {
		q = '"'
	}
	var sb strings.Builder
	sb.WriteByte(q)
	for _, r := range s {
		switch {
		case r == rune(q) || r == '\\':
			sb.WriteByte('\\')
			sb.WriteRune(r)
		case r == '\n':
			sb.WriteString(`\n`)
		case r == '\r':
			sb.WriteString(`\r`)
		case r == '\t':
			sb.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&sb, `\x%02x`, r)
		case r < 0x7f || unicode.IsPrint(r):
			sb.WriteRune(r)
		case r < 0x100:
			fmt.Fprintf(&sb, `\x%02x`, r)
		case r < 0x10000:
			fmt.Fprintf(&sb, `\u%04x`, r)
		default:
			fmt.Fprintf(&sb, `\U%08x`, r)
		}
	}
	sb.WriteByte(q)
	return sb.String()
}

// ---------------------------------------------------------------- HTML

// Esc は html.escape(str(s), quote=True)。None は空。
// Go の html.EscapeString は ' を &#39; にするので使わない（Python は &#x27;）
func Esc(v any) string {
	if v == nil {
		return ""
	}
	return HTMLEscape(Str(v))
}

var htmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#x27;")

// HTMLEscape は html.escape(s, quote=True)
func HTMLEscape(s string) string { return htmlEscaper.Replace(s) }

var xmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// XMLEscape は xml.sax.saxutils.escape（& < > だけ）
func XMLEscape(s string) string { return xmlEscaper.Replace(s) }

var numRefRe = regexp.MustCompile(`^&#(?:[0-9]+|[xX][0-9a-fA-F]+);?`)

// Python の html._invalid_charrefs（Windows-1252 の取り違え）。数値参照がこの番号なら、対応する文字にする
var invalidCharrefs = map[uint64]rune{
	0x00: 0xfffd, 0x0d: 0x0d, 0x80: 0x20ac, 0x81: 0x81, 0x82: 0x201a, 0x83: 0x0192, 0x84: 0x201e, 0x85: 0x2026,
	0x86: 0x2020, 0x87: 0x2021, 0x88: 0x02c6, 0x89: 0x2030, 0x8a: 0x0160, 0x8b: 0x2039, 0x8c: 0x0152, 0x8d: 0x8d,
	0x8e: 0x017d, 0x8f: 0x8f, 0x90: 0x90, 0x91: 0x2018, 0x92: 0x2019, 0x93: 0x201c, 0x94: 0x201d, 0x95: 0x2022,
	0x96: 0x2013, 0x97: 0x2014, 0x98: 0x02dc, 0x99: 0x2122, 0x9a: 0x0161, 0x9b: 0x203a, 0x9c: 0x0153, 0x9d: 0x9d,
	0x9e: 0x017e, 0x9f: 0x0178,
}

// pyInvalidCodepoint は html._invalid_codepoints（文字参照で出ても捨てる制御文字・非文字）
func pyInvalidCodepoint(n uint64) bool {
	switch {
	case n >= 0x1 && n <= 0x8, n == 0xb, n >= 0xe && n <= 0x1f, n >= 0x7f && n <= 0x9f, n >= 0xfdd0 && n <= 0xfdef:
		return true
	}
	return n <= 0x10ffff && (n&0xffff == 0xfffe || n&0xffff == 0xffff)
}

func numericRef(ref string) string {
	body := strings.TrimSuffix(ref[2:], ";")
	base := 10
	if body[0] == 'x' || body[0] == 'X' {
		base, body = 16, body[1:]
	}
	n, err := strconv.ParseUint(body, base, 64)
	if err != nil { // 桁あふれ（0x10ffff より大きい）
		return string(utf8.RuneError)
	}
	if r, ok := invalidCharrefs[n]; ok {
		return string(r)
	}
	if (n >= 0xd800 && n <= 0xdfff) || n > 0x10ffff {
		return string(utf8.RuneError)
	}
	if pyInvalidCodepoint(n) {
		return ""
	}
	return string(rune(n))
}

// HTMLUnescape は html.unescape。名前つきの文字参照（セミコロン無しの古い形を含む）は Go の html パッケージが同じ HTML5 の表で扱う。
// 数値参照は Python と規則が違う（0x80〜0x9f・制御文字・桁あふれ・「&#x;」）ので、ここで Python の規則どおりに先に処理する
func HTMLUnescape(s string) string {
	if !strings.Contains(s, "&") {
		return s
	}
	var sb strings.Builder
	for {
		i := strings.Index(s, "&#")
		if i < 0 {
			sb.WriteString(html.UnescapeString(s))
			return sb.String()
		}
		sb.WriteString(html.UnescapeString(s[:i]))
		if m := numRefRe.FindString(s[i:]); m != "" {
			sb.WriteString(numericRef(m))
			s = s[i+len(m):]
		} else {
			sb.WriteString("&#") // 数字が続かない「&#」は参照ではない
			s = s[i+2:]
		}
	}
}

// ---------------------------------------------------------------- URL

const alwaysSafe = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_.-~"

// Quote は urllib.parse.quote(s, safe=safe)。UTF-8 でパーセントエンコードし、16 進は大文字
func Quote(s, safe string) string {
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if strings.IndexByte(alwaysSafe, c) >= 0 || strings.IndexByte(safe, c) >= 0 {
			sb.WriteByte(c)
		} else {
			fmt.Fprintf(&sb, "%%%02X", c)
		}
	}
	return sb.String()
}

func isHex(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

func unhex(b byte) byte {
	switch {
	case b >= '0' && b <= '9':
		return b - '0'
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10
	}
	return b - 'A' + 10
}

// Unquote は urllib.parse.unquote(s)（UTF-8・errors='replace'）。
// ASCII の連なりごとに %XX をバイトへ戻して UTF-8 として読む。ASCII 以外の文字はそのまま
func Unquote(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	var out strings.Builder
	var run []byte
	flush := func() {
		if len(run) > 0 {
			out.WriteString(DecodeReplace(unquoteBytes(run)))
			run = run[:0]
		}
	}
	for _, r := range s {
		if r < 0x80 {
			run = append(run, byte(r))
		} else {
			flush()
			out.WriteRune(r)
		}
	}
	flush()
	return out.String()
}

func unquoteBytes(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if b[i] == '%' && i+2 < len(b) && isHex(b[i+1]) && isHex(b[i+2]) {
			out = append(out, unhex(b[i+1])<<4|unhex(b[i+2]))
			i += 2
		} else {
			out = append(out, b[i])
		}
	}
	return out
}

// DecodeReplace は bytes.decode('utf-8', 'replace')。壊れた並びは「最大の正しい先頭部分」ごとに U+FFFD 1 つ
func DecodeReplace(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var sb strings.Builder
	for i := 0; i < len(b); {
		r, n := utf8.DecodeRune(b[i:])
		if r != utf8.RuneError || n != 1 {
			sb.WriteRune(r)
			i += n
			continue
		}
		sb.WriteRune(utf8.RuneError)
		i += maximalSubpart(b[i:])
	}
	return sb.String()
}

// maximalSubpart は、壊れた並びの先頭から、正しい UTF-8 の途中までに当たる長さ（Unicode の「最大の部分列」）
func maximalSubpart(b []byte) int {
	b0 := b[0]
	var lo, hi byte = 0x80, 0xBF
	need := 0
	switch {
	case b0 >= 0xC2 && b0 <= 0xDF:
		need = 1
	case b0 >= 0xE0 && b0 <= 0xEF:
		need = 2
		if b0 == 0xE0 {
			lo = 0xA0
		} else if b0 == 0xED {
			hi = 0x9F
		}
	case b0 >= 0xF0 && b0 <= 0xF4:
		need = 3
		if b0 == 0xF0 {
			lo = 0x90
		} else if b0 == 0xF4 {
			hi = 0x8F
		}
	default:
		return 1
	}
	n := 1
	for k := 0; k < need && n < len(b); k++ {
		c := b[n]
		if k == 0 {
			if c < lo || c > hi {
				return n
			}
		} else if c < 0x80 || c > 0xBF {
			return n
		}
		n++
	}
	return n
}

// ---------------------------------------------------------------- ファイル

// ReadText は open(path, encoding='utf-8').read()。改行は \n にそろう（ユニバーサル改行）
func ReadText(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		panic(err.Error())
	}
	return UniversalNewlines(string(b))
}

// UniversalNewlines は CRLF と CR を LF にそろえる（open() の既定の読み方と同じ）
func UniversalNewlines(s string) string {
	if !strings.Contains(s, "\r") {
		return s
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// WriteText は文字列をそのまま（UTF-8・改行を変えずに）書く。失敗したら止まる
func WriteText(path, s string) {
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		panic(err.Error())
	}
}

// FileExists はファイルかディレクトリがあるか
func FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// IsFile は通常のファイルがあるか
func IsFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}

// IsDir はディレクトリがあるか
func IsDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

// Join は os.path.join(a, b)（Cleanしない。b が絶対なら b、a が空なら b）
func Join(a, b string) string {
	switch {
	case strings.HasPrefix(b, "/"):
		return b
	case a == "":
		return b
	case strings.HasSuffix(a, "/"):
		return a + b
	}
	return a + "/" + b
}

// ListDirSorted は os.listdir の代わり（Python は順不同。Go 版は名前順にして結果を安定させる）
func ListDirSorted(dir string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		panic(err.Error())
	}
	names := make([]string, len(ents))
	for i, e := range ents {
		names[i] = e.Name()
	}
	sort.Strings(names)
	return names
}

// WalkFiles は os.walk の代わり。ディレクトリごとに「そこにあるファイル → 下のディレクトリ」の順（名前順）で
// 相対パス（/ 区切り）を返す。top 自身を含まない（"./a/b.html" ではなく "a/b.html"）
func WalkFiles(top string, visit func(rel string)) {
	var rec func(dir, rel string)
	rec = func(dir, rel string) {
		ents, err := os.ReadDir(dir)
		if err != nil {
			panic(err.Error())
		}
		var dirs []string
		for _, e := range ents {
			if e.Type()&os.ModeSymlink != 0 {
				// os.walk（followlinks=False）は、ディレクトリへのリンクを files には入れず、降りもしない
				if IsDir(dir + "/" + e.Name()) {
					continue
				}
				visit(rel + e.Name())
				continue
			}
			if e.IsDir() {
				dirs = append(dirs, e.Name())
			} else {
				visit(rel + e.Name())
			}
		}
		for _, d := range dirs {
			rec(dir+"/"+d, rel+d+"/")
		}
	}
	rec(top, "")
}
