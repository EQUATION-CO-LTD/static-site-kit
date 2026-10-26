package pyfmt

// JSON から読んだ値（Python の動的な型）の取り出し方。型が合わなければ TypeError 相当で止まる（panic）。
// panic は sitegen.Run などの入口が受けて error にする。

import (
	"fmt"
	"sort"
)

// AsObj は値を *Obj として取り出す
func AsObj(v any) *Obj {
	if o, ok := v.(*Obj); ok {
		return o
	}
	panic(fmt.Sprintf("TypeError: オブジェクトが要る: %s", ReprAny(v)))
}

// AsList は配列。None（無い）は空として扱う
func AsList(v any) []any {
	switch x := v.(type) {
	case nil:
		return nil
	case []any:
		return x
	}
	panic(fmt.Sprintf("TypeError: 配列が要る: %s", ReprAny(v)))
}

// AsObjs はオブジェクトの配列
func AsObjs(v any) []*Obj {
	l := AsList(v)
	out := make([]*Obj, len(l))
	for i, e := range l {
		out[i] = AsObj(e)
	}
	return out
}

// AsFloat は数を float64 として取り出す
func AsFloat(v any) float64 {
	if n, ok := v.(Num); ok {
		return n.F
	}
	panic(fmt.Sprintf("TypeError: 数が要る: %s", ReprAny(v)))
}

// AsInt は数を int として取り出す（小数は切り捨て）
func AsInt(v any) int {
	if n, ok := v.(Num); ok {
		return int(n.F)
	}
	panic(fmt.Sprintf("TypeError: 数が要る: %s", ReprAny(v)))
}

// Getd は dict.get(k, default)
func Getd(o *Obj, k string, def any) any {
	if o.Has(k) {
		return o.Get(k)
	}
	return def
}

// Or は Python の `a or b`
func Or(a, b any) any {
	if Truthy(a) {
		return a
	}
	return b
}

// OrKey は Python の `o.get(a) or o[b]`。a が偽のときだけ b を引く（b が無くても a があれば止まらない）
func OrKey(o *Obj, a, b string) any {
	if v := o.Get(a); Truthy(v) {
		return v
	}
	return o.At(b)
}

// StrEq は `v == s`（v は任意の型）
func StrEq(v any, s string) bool {
	x, ok := v.(string)
	return ok && x == s
}

// SortedStable は items を less で安定に並べた複製（元は変えない）。Python の sorted と同じく、同じ値は元の並びのまま
func SortedStable[T any](items []T, less func(a, b T) bool) []T {
	c := append([]T(nil), items...)
	sort.SliceStable(c, func(i, j int) bool { return less(c[i], c[j]) })
	return c
}

// Atoi は int(s)。数字でなければ ValueError 相当で止まる
func Atoi(s string) int {
	n := 0
	neg := false
	for i, r := range s {
		if i == 0 && r == '-' {
			neg = true
			continue
		}
		if r < '0' || r > '9' {
			panic(fmt.Sprintf("ValueError: invalid literal for int(): %s", Repr(s)))
		}
		n = n*10 + int(r-'0')
	}
	if neg {
		n = -n
	}
	return n
}

// JSONFileText は json.dump(data, f, ensure_ascii=False, indent=1) + '\n'（取得した JSON ファイルの形）
func JSONFileText(data any) string { return Dump(data, DumpPretty) + "\n" }

// LoadJSONFile は JSON ファイルを読む。無ければ ok=false。中身が壊れていれば path つきで止まる
func LoadJSONFile(path string) (v any, ok bool) {
	if !FileExists(path) {
		return nil, false
	}
	v, err := ParseJSON(ReadText(path))
	if err != nil {
		panic(path + ": " + err.Error())
	}
	return v, true
}
