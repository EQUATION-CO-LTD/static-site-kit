package pyfmt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestObjDuplicateKeys(t *testing.T) {
	v, _ := ParseJSON(`{"a":1,"b":2,"a":3}`)
	if got := Dump(v, DumpCompact); got != `{"a":3,"b":2}` { // 位置は最初、値は最後（dict と同じ）
		t.Errorf("dup = %s", got)
	}
}

func TestParseJSONErrors(t *testing.T) {
	for _, in := range []string{``, `{`, `[1,]`, `{"a" 1}`, `"\x"`, `01`, `[1] x`, "\"a\nb\""} {
		if _, err := ParseJSON(in); err == nil {
			t.Errorf("ParseJSON(%q) は失敗するはず", in)
		}
	}
}

func TestSlicesByCodePoint(t *testing.T) {
	if Slice("あいう😀えお", 2, 5) != "う😀え" || Head("あいう", 10) != "あいう" || Tail("あいう", 1) != "いう" || RuneLen("😀a") != 2 {
		t.Errorf("コードポイント単位の切り出し")
	}
	if Strip("\x1c　 a b \n") != "a b" || StripChars("\n\nx\n", "\n") != "x" {
		t.Errorf("strip")
	}
	if got := SplitLines("a\r\nb\rc\x0bd\n\ne\n"); strings.Join(got, "|") != "a|b|c|d||e" {
		t.Errorf("splitlines = %q", got)
	}
	if SplitExt("about.html") != "about" || SplitExt(".hidden") != ".hidden" || SplitExt("a.b.html") != "a.b" || SplitExt("index") != "index" {
		t.Errorf("splitext")
	}
	if Repr("abc1") != "'abc1'" || Repr("it's") != `"it's"` || Repr("a\nb") != `'a\nb'` {
		t.Errorf("repr")
	}
}

func TestTruthyAndOr(t *testing.T) {
	for _, v := range []any{nil, "", false, IntNum(0), []any{}, NewObj()} {
		if Truthy(v) {
			t.Errorf("Truthy(%v) は偽のはず", v)
		}
	}
	for _, v := range []any{"a", true, IntNum(1), []any{1}, O("k", 1)} {
		if !Truthy(v) {
			t.Errorf("Truthy(%v) は真のはず", v)
		}
	}
	if Or("", "b") != "b" || Or("a", "b") != "a" {
		t.Errorf("Or")
	}
	o := O("a", "", "b", "x")
	if OrKey(o, "a", "b") != "x" || OrKey(O("a", "y"), "a", "missing") != "y" {
		t.Errorf("OrKey は a が偽のときだけ b を引く")
	}
}

func TestAsHelpersPanicOnTypeMismatch(t *testing.T) {
	mustPanic := func(name string, f func()) {
		defer func() {
			if recover() == nil {
				t.Errorf("%s は型が違えば止まるはず", name)
			}
		}()
		f()
	}
	mustPanic("AsObj", func() { AsObj("x") })
	mustPanic("AsList", func() { AsList("x") })
	mustPanic("AsFloat", func() { AsFloat("x") })
	mustPanic("At", func() { NewObj().At("missing") })
	mustPanic("Atoi", func() { Atoi("1x") })
	if AsList(nil) != nil || Atoi("-12") != -12 {
		t.Errorf("AsList(nil) は空、Atoi は int()")
	}
}

func TestSortedStableKeepsOrderOfEquals(t *testing.T) {
	type kv struct {
		k string
		n int
	}
	in := []kv{{"a", 2}, {"b", 1}, {"c", 2}, {"d", 1}}
	got := SortedStable(in, func(x, y kv) bool { return x.n < y.n })
	var ids []string
	for _, e := range got {
		ids = append(ids, e.k)
	}
	if strings.Join(ids, "") != "bdac" || in[0].k != "a" {
		t.Errorf("SortedStable = %v（元は変えない）", ids)
	}
}

func TestDeepCopyIsIndependent(t *testing.T) {
	src, _ := ParseJSON(`{"a":[1,{"b":2}],"c":"x"}`)
	cp := DeepCopy(src).(*Obj)
	cp.Set("c", "changed")
	AsList(cp.At("a"))[0] = IntNum(9)
	if got := Dump(src, DumpCompact); got != `{"a":[1,{"b":2}],"c":"x"}` {
		t.Errorf("複製を書き換えると元も変わった: %s", got)
	}
}

func TestJSONFileText(t *testing.T) {
	v := O("a", []any{IntNum(1), "日本語"}, "b", NewObj())
	want := "{\n \"a\": [\n  1,\n  \"日本語\"\n ],\n \"b\": {}\n}\n"
	if got := JSONFileText(v); got != want {
		t.Errorf("JSONFileText = %q", got)
	}
}

func TestFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("b.txt", "1\r\n2\r3\n")
	write("a/z.txt", "")
	write("a/y/k.txt", "")
	write("c.json", `{"k": [1]}`)
	if got := ReadText(dir + "/b.txt"); got != "1\n2\n3\n" {
		t.Errorf("ユニバーサル改行: %q", got)
	}
	var walked []string
	WalkFiles(dir, func(rel string) { walked = append(walked, rel) })
	// ディレクトリごとに「そこのファイル → 下のディレクトリ」の順、名前順
	if got := strings.Join(walked, ","); got != "b.txt,c.json,a/z.txt,a/y/k.txt" {
		t.Errorf("WalkFiles = %s", got)
	}
	if got := strings.Join(ListDirSorted(dir), ","); got != "a,b.txt,c.json" {
		t.Errorf("ListDirSorted = %s", got)
	}
	if !IsDir(dir+"/a") || IsFile(dir+"/a") || !IsFile(dir+"/b.txt") || FileExists(dir+"/none") {
		t.Errorf("IsDir/IsFile/FileExists")
	}
	if v, ok := LoadJSONFile(dir + "/c.json"); !ok || Dump(v, DumpCompact) != `{"k":[1]}` {
		t.Errorf("LoadJSONFile")
	}
	if _, ok := LoadJSONFile(dir + "/none.json"); ok {
		t.Errorf("無いファイルは ok=false")
	}
	if Join("a", "b") != "a/b" || Join("a/", "b") != "a/b" || Join("", "b") != "b" || Join("a", "/b") != "/b" {
		t.Errorf("Join")
	}
	if got := DecodeReplace([]byte("a\xffb\xe3\x81")); got != "a�b�" {
		t.Errorf("DecodeReplace = %q", got)
	}
}
