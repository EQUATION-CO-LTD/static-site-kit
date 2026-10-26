package pyfmt

// Python の json モジュールと同じ入出力を、標準ライブラリだけで再現する。
// 狙いは「Python 版が書いたファイルとバイト単位で同じ物を書く」こと。
//   - 読み: オブジェクトのキー順を保つ（dict の挿入順）。同じキーが重なったら、位置は最初・値は最後（dict と同じ）
//   - 書き: json.dumps / json.dump の indent・separators・ensure_ascii を引数で選べる
// encoding/json は map の順序が無く、< > & を < に変えるため使わない。

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Obj は JSON のオブジェクト（挿入順つき）。
type Obj struct {
	keys []string
	m    map[string]any
}

// NewObj は空の Obj
func NewObj() *Obj { return &Obj{m: map[string]any{}} }

// O はキーと値を交互に並べて Obj を作る。O("a", 1, "b", 2)
func O(kv ...any) *Obj {
	o := NewObj()
	for i := 0; i+1 < len(kv); i += 2 {
		o.Set(kv[i].(string), kv[i+1])
	}
	return o
}

// Len はキーの数
func (o *Obj) Len() int { return len(o.keys) }

// Keys は挿入順のキー（呼び出し側で書き換えない）
func (o *Obj) Keys() []string { return o.keys }

// Has は k in dict
func (o *Obj) Has(k string) bool { _, ok := o.m[k]; return ok }

// Get は dict.get(k)。無ければ nil
func (o *Obj) Get(k string) any { return o.m[k] }

// At は dict[k]。無ければ KeyError 相当で止まる
func (o *Obj) At(k string) any {
	v, ok := o.m[k]
	if !ok {
		panic(fmt.Sprintf("KeyError: %s", Repr(k)))
	}
	return v
}

// Set は dict[k] = v。既存のキーは位置を変えない
func (o *Obj) Set(k string, v any) {
	if _, ok := o.m[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.m[k] = v
}

// SetDefault は dict.setdefault(k, v)
func (o *Obj) SetDefault(k string, v any) any {
	if cur, ok := o.m[k]; ok {
		return cur
	}
	o.Set(k, v)
	return v
}

// Num は JSON の数。Python の int / float に合わせた書き出し用の文字列を持つ。
type Num struct {
	Text  string
	F     float64
	IsInt bool
}

// IntNum は整数の Num
func IntNum(i int) Num { return Num{Text: strconv.Itoa(i), F: float64(i), IsInt: true} }

// ---------------------------------------------------------------- 読み

type jsonParser struct {
	s string
	i int
}

type parseError string

func (e parseError) Error() string { return string(e) }

// ParseJSON は json.loads。オブジェクトは *Obj、配列は []any、数は Num、文字列は string、真偽は bool、null は nil
func ParseJSON(s string) (v any, err error) {
	defer func() {
		if r := recover(); r != nil {
			if pe, ok := r.(parseError); ok {
				err = pe
				return
			}
			panic(r)
		}
	}()
	p := &jsonParser{s: s}
	p.ws()
	v = p.value()
	p.ws()
	if p.i != len(p.s) {
		p.fail("Extra data")
	}
	return v, nil
}

func (p *jsonParser) fail(msg string) {
	panic(parseError(fmt.Sprintf("JSONDecodeError: %s: char %d", msg, p.i)))
}

func (p *jsonParser) ws() {
	for p.i < len(p.s) {
		switch p.s[p.i] {
		case ' ', '\t', '\n', '\r':
			p.i++
		default:
			return
		}
	}
}

func (p *jsonParser) value() any {
	if p.i >= len(p.s) {
		p.fail("Expecting value")
	}
	rest := p.s[p.i:]
	switch c := p.s[p.i]; {
	case c == '{':
		return p.object()
	case c == '[':
		return p.array()
	case c == '"':
		return p.str()
	case strings.HasPrefix(rest, "true"):
		p.i += 4
		return true
	case strings.HasPrefix(rest, "false"):
		p.i += 5
		return false
	case strings.HasPrefix(rest, "null"):
		p.i += 4
		return nil
	case strings.HasPrefix(rest, "NaN"):
		p.i += 3
		return Num{Text: "NaN", F: math.NaN()}
	case strings.HasPrefix(rest, "Infinity"):
		p.i += 8
		return Num{Text: "Infinity", F: math.Inf(1)}
	case strings.HasPrefix(rest, "-Infinity"):
		p.i += 9
		return Num{Text: "-Infinity", F: math.Inf(-1)}
	default:
		return p.number()
	}
}

func (p *jsonParser) object() any {
	p.i++ // {
	o := NewObj()
	p.ws()
	if p.i < len(p.s) && p.s[p.i] == '}' {
		p.i++
		return o
	}
	for {
		p.ws()
		if p.i >= len(p.s) || p.s[p.i] != '"' {
			p.fail("Expecting property name enclosed in double quotes")
		}
		k := p.str()
		p.ws()
		if p.i >= len(p.s) || p.s[p.i] != ':' {
			p.fail("Expecting ':' delimiter")
		}
		p.i++
		p.ws()
		o.Set(k, p.value())
		p.ws()
		if p.i >= len(p.s) {
			p.fail("Expecting ',' delimiter")
		}
		if p.s[p.i] == ',' {
			p.i++
			continue
		}
		if p.s[p.i] == '}' {
			p.i++
			return o
		}
		p.fail("Expecting ',' delimiter")
	}
}

func (p *jsonParser) array() any {
	p.i++ // [
	arr := []any{}
	p.ws()
	if p.i < len(p.s) && p.s[p.i] == ']' {
		p.i++
		return arr
	}
	for {
		p.ws()
		arr = append(arr, p.value())
		p.ws()
		if p.i >= len(p.s) {
			p.fail("Expecting ',' delimiter")
		}
		if p.s[p.i] == ',' {
			p.i++
			continue
		}
		if p.s[p.i] == ']' {
			p.i++
			return arr
		}
		p.fail("Expecting ',' delimiter")
	}
}

func (p *jsonParser) str() string {
	p.i++ // "
	var sb strings.Builder
	for {
		if p.i >= len(p.s) {
			p.fail("Unterminated string")
		}
		c := p.s[p.i]
		switch {
		case c == '"':
			p.i++
			return sb.String()
		case c == '\\':
			p.i++
			if p.i >= len(p.s) {
				p.fail("Unterminated string")
			}
			e := p.s[p.i]
			p.i++
			switch e {
			case '"', '\\', '/':
				sb.WriteByte(e)
			case 'b':
				sb.WriteByte('\b')
			case 'f':
				sb.WriteByte('\f')
			case 'n':
				sb.WriteByte('\n')
			case 'r':
				sb.WriteByte('\r')
			case 't':
				sb.WriteByte('\t')
			case 'u':
				r := p.hex4()
				if utf16.IsSurrogate(r) && r < 0xDC00 && strings.HasPrefix(p.s[p.i:], `\u`) {
					save := p.i
					p.i += 2
					r2 := p.hex4()
					if d := utf16.DecodeRune(r, r2); d != utf8.RuneError {
						sb.WriteRune(d)
						continue
					}
					p.i = save
				}
				// 対になっていないサロゲートは U+FFFD になる（Python は孤立サロゲートを保つが、内容には現れない前提）
				sb.WriteRune(r)
			default:
				p.fail("Invalid \\escape")
			}
		case c < 0x20:
			p.fail("Invalid control character")
		default:
			sb.WriteByte(c)
			p.i++
		}
	}
}

func (p *jsonParser) hex4() rune {
	if p.i+4 > len(p.s) {
		p.fail("Invalid \\uXXXX escape")
	}
	n, err := strconv.ParseUint(p.s[p.i:p.i+4], 16, 32)
	if err != nil {
		p.fail("Invalid \\uXXXX escape")
	}
	p.i += 4
	return rune(n)
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

func (p *jsonParser) number() any {
	start := p.i
	i := p.i
	if i < len(p.s) && p.s[i] == '-' {
		i++
	}
	if i >= len(p.s) {
		p.fail("Expecting value")
	}
	if p.s[i] == '0' {
		i++
	} else if p.s[i] >= '1' && p.s[i] <= '9' {
		for i < len(p.s) && isDigit(p.s[i]) {
			i++
		}
	} else {
		p.fail("Expecting value")
	}
	isInt := true
	if i+1 < len(p.s) && p.s[i] == '.' && isDigit(p.s[i+1]) {
		isInt = false
		i++
		for i < len(p.s) && isDigit(p.s[i]) {
			i++
		}
	}
	if i < len(p.s) && (p.s[i] == 'e' || p.s[i] == 'E') {
		j := i + 1
		if j < len(p.s) && (p.s[j] == '+' || p.s[j] == '-') {
			j++
		}
		if j < len(p.s) && isDigit(p.s[j]) {
			for j < len(p.s) && isDigit(p.s[j]) {
				j++
			}
			isInt = false
			i = j
		}
	}
	raw := p.s[start:i]
	p.i = i
	if isInt {
		b, _ := new(big.Int).SetString(raw, 10)
		f, _ := strconv.ParseFloat(raw, 64)
		return Num{Text: b.String(), F: f, IsInt: true}
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil && !math.IsInf(f, 0) {
		p.fail("Invalid number")
	}
	return Num{Text: pyFloatRepr(f), F: f}
}

// pyFloatRepr は Python の repr(float)。最短の桁で、指数が -4 未満か 16 以上なら指数表記
func pyFloatRepr(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	}
	if f == 0 {
		if math.Signbit(f) {
			return "-0.0"
		}
		return "0.0"
	}
	e := strconv.FormatFloat(f, 'e', -1, 64) // d.ddde±XX
	neg := ""
	if e[0] == '-' {
		neg = "-"
		e = e[1:]
	}
	mant, expS, _ := strings.Cut(e, "e")
	exp, _ := strconv.Atoi(expS)
	digits := strings.Replace(mant, ".", "", 1)
	if exp < -4 || exp >= 16 {
		m := digits[:1]
		if len(digits) > 1 {
			m += "." + digits[1:]
		}
		sign := "+"
		if exp < 0 {
			sign = "-"
			exp = -exp
		}
		return fmt.Sprintf("%s%se%s%02d", neg, m, sign, exp)
	}
	if exp < 0 {
		return neg + "0." + strings.Repeat("0", -exp-1) + digits
	}
	if len(digits) <= exp+1 {
		return neg + digits + strings.Repeat("0", exp+1-len(digits)) + ".0"
	}
	return neg + digits[:exp+1] + "." + digits[exp+1:]
}

// ---------------------------------------------------------------- 書き

// DumpOpts は json.dumps の indent / separators / ensure_ascii。Indent < 0 は indent=None
type DumpOpts struct {
	Indent  int
	ItemSep string
	KeySep  string
	ASCII   bool
}

var (
	// DumpPretty は json.dump(..., ensure_ascii=False, indent=1)（取得した JSON ファイルの書き出し）
	DumpPretty = DumpOpts{Indent: 1, ItemSep: ",", KeySep: ": ", ASCII: false}
	// DumpPrettyASCII は json.dump(..., ensure_ascii=True, indent=1)（staticwebapp.config.json）
	DumpPrettyASCII = DumpOpts{Indent: 1, ItemSep: ",", KeySep: ": ", ASCII: true}
	// DumpCompact は json.dumps(x, separators=(",", ":"), ensure_ascii=False)（JSON-LD）
	DumpCompact = DumpOpts{Indent: -1, ItemSep: ",", KeySep: ":", ASCII: false}
	// DumpCompactASCII は json.dumps(x, separators=(",", ":"))（ensure_ascii は既定の True）
	DumpCompactASCII = DumpOpts{Indent: -1, ItemSep: ",", KeySep: ":", ASCII: true}
	// DumpDefault は json.dumps(x, ensure_ascii=False)（既定の区切り）
	DumpDefault = DumpOpts{Indent: -1, ItemSep: ", ", KeySep: ": ", ASCII: false}
)

// Dump は json.dumps(v, ...) と同じ文字列を作る。値は nil・bool・string・Num・int・[]any・[]*Obj・*Obj
func Dump(v any, o DumpOpts) string {
	var sb strings.Builder
	dumpValue(&sb, v, o, 0)
	return sb.String()
}

func dumpValue(sb *strings.Builder, v any, o DumpOpts, level int) {
	nl := func(l int) string {
		if o.Indent < 0 {
			return ""
		}
		return "\n" + strings.Repeat(" ", o.Indent*l)
	}
	switch x := v.(type) {
	case nil:
		sb.WriteString("null")
	case bool:
		if x {
			sb.WriteString("true")
		} else {
			sb.WriteString("false")
		}
	case string:
		sb.WriteString(jsonString(x, o.ASCII))
	case Num:
		sb.WriteString(x.Text)
	case int:
		sb.WriteString(strconv.Itoa(x))
	case []any:
		if len(x) == 0 {
			sb.WriteString("[]")
			return
		}
		sb.WriteString("[")
		sb.WriteString(nl(level + 1))
		for i, e := range x {
			if i > 0 {
				sb.WriteString(o.ItemSep)
				sb.WriteString(nl(level + 1))
			}
			dumpValue(sb, e, o, level+1)
		}
		sb.WriteString(nl(level))
		sb.WriteString("]")
	case []*Obj:
		a := make([]any, len(x))
		for i, e := range x {
			a[i] = e
		}
		dumpValue(sb, a, o, level)
	case *Obj:
		if x.Len() == 0 {
			sb.WriteString("{}")
			return
		}
		sb.WriteString("{")
		sb.WriteString(nl(level + 1))
		for i, k := range x.keys {
			if i > 0 {
				sb.WriteString(o.ItemSep)
				sb.WriteString(nl(level + 1))
			}
			sb.WriteString(jsonString(k, o.ASCII))
			sb.WriteString(o.KeySep)
			dumpValue(sb, x.m[k], o, level+1)
		}
		sb.WriteString(nl(level))
		sb.WriteString("}")
	default:
		panic(fmt.Sprintf("TypeError: JSON にできない型 %T", v))
	}
}

// jsonString は Python の py_encode_basestring(_ascii)。
// ascii のときは ' '〜'~' 以外（DEL を含む）をすべて \uXXXX（小文字 16 進、BMP 外はサロゲートペア）にする
func jsonString(s string, ascii bool) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			sb.WriteString(`\\`)
		case '"':
			sb.WriteString(`\"`)
		case '\b':
			sb.WriteString(`\b`)
		case '\f':
			sb.WriteString(`\f`)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		default:
			switch {
			case r < 0x20:
				fmt.Fprintf(&sb, `\u%04x`, r)
			case !ascii:
				sb.WriteRune(r)
			case r >= 0x20 && r <= 0x7e:
				sb.WriteRune(r)
			case r < 0x10000:
				fmt.Fprintf(&sb, `\u%04x`, r)
			default:
				a, b := utf16.EncodeRune(r)
				fmt.Fprintf(&sb, `\u%04x\u%04x`, a, b)
			}
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

// DeepCopy は json.loads(json.dumps(x)) と同じ（キー順を保った複製）
func DeepCopy(v any) any {
	switch x := v.(type) {
	case *Obj:
		c := NewObj()
		for _, k := range x.keys {
			c.Set(k, DeepCopy(x.m[k]))
		}
		return c
	case []any:
		c := make([]any, len(x))
		for i, e := range x {
			c[i] = DeepCopy(e)
		}
		return c
	default:
		return v
	}
}
