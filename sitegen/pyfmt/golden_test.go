package pyfmt

// Python の標準ライブラリの出力を期待値にした表形式のテスト。
// 期待値は testdata/py_golden.json（testdata/gen_golden.py が html・urllib.parse・json・re から作る）。

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"unicode"
)

var golden = func() *Obj {
	b, err := os.ReadFile("testdata/py_golden.json")
	if err != nil {
		panic(err)
	}
	v, err := ParseJSON(string(b))
	if err != nil {
		panic(err)
	}
	return AsObj(v)
}()

func cases(name string) []*Obj { return AsObjs(golden.At(name)) }

func str(o *Obj, k string) string { return Str(o.At(k)) }

func TestEsc(t *testing.T) {
	for _, c := range cases("esc") {
		if got, want := Esc(str(c, "in")), str(c, "out"); got != want {
			t.Errorf("Esc(%q) = %q, want %q", str(c, "in"), got, want)
		}
	}
	if Esc(nil) != "" {
		t.Errorf("Esc(None) は空")
	}
	if Esc(IntNum(12)) != "12" || Esc(true) != "True" {
		t.Errorf("Esc は str() してから escape する")
	}
}

func TestXMLEscape(t *testing.T) {
	for _, c := range cases("xml_escape") {
		if got, want := XMLEscape(str(c, "in")), str(c, "out"); got != want {
			t.Errorf("XMLEscape(%q) = %q, want %q", str(c, "in"), got, want)
		}
	}
}

func TestQuote(t *testing.T) {
	for _, c := range cases("quote") {
		if got, want := Quote(str(c, "in"), str(c, "safe")), str(c, "out"); got != want {
			t.Errorf("Quote(%q, safe=%q) = %q, want %q", str(c, "in"), str(c, "safe"), got, want)
		}
	}
}

func TestUnquote(t *testing.T) {
	for _, c := range cases("unquote") {
		if got, want := Unquote(str(c, "in")), str(c, "out"); got != want {
			t.Errorf("Unquote(%q) = %q, want %q", str(c, "in"), got, want)
		}
	}
}

func TestHTMLUnescape(t *testing.T) {
	for _, c := range cases("unescape") {
		if got, want := HTMLUnescape(str(c, "in")), str(c, "out"); got != want {
			t.Errorf("HTMLUnescape(%q) = %q, want %q", str(c, "in"), got, want)
		}
	}
}

func TestFloatRepr(t *testing.T) {
	for _, c := range cases("float_repr") {
		v, err := ParseJSON(str(c, "in"))
		if err != nil {
			t.Fatal(err)
		}
		if got, want := Dump(v, DumpDefault), str(c, "out"); got != want {
			t.Errorf("dumps(%s) = %q, want %q", str(c, "in"), got, want)
		}
	}
}

func TestJSONDump(t *testing.T) {
	for _, c := range cases("json") {
		v, err := ParseJSON(str(c, "in"))
		if err != nil {
			t.Fatal(err)
		}
		check := func(mode string, opts DumpOpts, post func(string) string) {
			got := post(Dump(v, opts))
			if want := str(c, mode); got != want {
				t.Errorf("%s: %s\n got  %q\n want %q", str(c, "in"), mode, got, want)
			}
		}
		same := func(s string) string { return s }
		check("pretty", DumpPretty, same)
		check("pretty_ascii", DumpPrettyASCII, same)
		check("compact", DumpCompact, func(s string) string { return strings.ReplaceAll(s, "</", `<\/`) })
		check("compact_ascii", DumpCompactASCII, same)
		check("default", DumpDefault, same)
	}
}

// rangesContain は [[lo, hi], ...] に cp が入るか
func rangesContain(ranges []any, cp rune) bool {
	for _, r := range ranges {
		p := AsList(r)
		if cp >= rune(AsInt(p[0])) && cp <= rune(AsInt(p[1])) {
			return true
		}
	}
	return false
}

// Python の \s は Space と全コードポイントで一致する
func TestSpaceClassMatchesPython(t *testing.T) {
	re := regexp.MustCompile(`^[` + Space + `]$`)
	py := AsList(golden.At("space_ranges"))
	for cp := rune(0); cp <= 0x10FFFF; cp++ {
		if cp >= 0xD800 && cp <= 0xDFFF {
			continue
		}
		want := rangesContain(py, cp)
		if got := re.MatchString(string(cp)); got != want {
			t.Errorf("Space U+%04X: got %v, Python %v", cp, got, want)
		}
		if got := IsSpace(cp); got != want {
			t.Errorf("IsSpace U+%04X: got %v, Python %v", cp, got, want)
		}
	}
}

// goAssigned は、Go の Unicode の表（unicode.Version）にその文字があるか（どれかの一般カテゴリに入っているか）
func goAssigned(cp rune) bool {
	for name, tab := range unicode.Categories {
		if len(name) == 2 && name != "Cn" && unicode.Is(tab, cp) { // Lu Ll Nd Mn … の 2 文字のカテゴリ（Cn は未割り当て）
			return true
		}
	}
	return false
}

// Python の \w と Word（Unicode の文字・数字・_）が、Go の Unicode の表にある文字で食い違わないこと。
// 食い違うのは、Python の Unicode の版が Go（unicode.Version）より新しく、Go が未割り当てとしている文字だけ
func TestWordClassMatchesPython(t *testing.T) {
	re := regexp.MustCompile(`^[` + Word + `]$`)
	py := AsList(golden.At("word_ranges"))
	versionGap := 0
	for cp := rune(0); cp <= 0x10FFFF; cp++ {
		if cp >= 0xD800 && cp <= 0xDFFF {
			continue
		}
		want := rangesContain(py, cp)
		if got := re.MatchString(string(cp)); got != want {
			if !goAssigned(cp) && want {
				versionGap++
				continue
			}
			t.Errorf("Word U+%04X: got %v, Python %v", cp, got, want)
		}
	}
	t.Logf("Go の Unicode %s で未割り当ての新しい文字（Python 側の \\w は真）: %d 文字", unicode.Version, versionGap)
}
