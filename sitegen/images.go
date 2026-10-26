package sitegen

// 画像の差し替え。microCMS などで写真を替えたとき、「元の画像のパス → 新しい画像のパス」の対応表を、
// すべてのページと static の CSS に当てはめる（元の画像のファイルは static に残す）。

import (
	"strings"

	"github.com/EQUATION-CO-LTD/static-site-kit/sitegen/pyfmt"
)

// ImageSwap は画像 1 枚の差し替え（元のパス → 新しいパス）
type ImageSwap struct{ From, To string }

// ImageSwapsFromObj は {"元のパス": "新しいパス"} の JSON を、書かれた順の差し替えの並びにする。
// 差し替えは書かれた順に当てる（先の結果に後の元のパスが重なれば、後のものがさらに効く）
func ImageSwapsFromObj(o *pyfmt.Obj) []ImageSwap {
	swaps := make([]ImageSwap, 0, o.Len())
	for _, k := range o.Keys() {
		swaps = append(swaps, ImageSwap{k, pyfmt.Str(o.Get(k))})
	}
	return swaps
}

// SwapImages は text の中の元の画像のパスを新しいパスにする。
// 元のパスの直後が " ' ? ) のときだけ置く（/images/a.jpg が /images/a.jpg.bak や /images/a.jpgx を巻き込まないように）
func SwapImages(text string, swaps []ImageSwap) string {
	for _, sw := range swaps {
		if sw.From == "" {
			Die("画像の差し替えの元のパスが空")
		}
		var sb strings.Builder
		i := 0
		for {
			j := strings.Index(text[i:], sw.From)
			if j < 0 {
				break
			}
			j += i
			end := j + len(sw.From)
			if end < len(text) && strings.IndexByte(`"'?)`, text[end]) >= 0 {
				sb.WriteString(text[i:j])
				sb.WriteString(sw.To)
				i = end
			} else {
				sb.WriteString(text[i : j+1])
				i = j + 1
			}
		}
		sb.WriteString(text[i:])
		text = sb.String()
	}
	return text
}
