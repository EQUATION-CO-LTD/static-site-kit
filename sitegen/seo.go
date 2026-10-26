package sitegen

// SEO の部品。canonical・OGP（og:image を含む）・構造化データ（JSON-LD）を作る関数で、
// どれを外枠に入れるかは Layout が決める（SEO は全部入りの一括版）。日本語のサイトを前提にしている（ja_JP・ja）。

import (
	"fmt"
	"strings"

	"github.com/EQUATION-CO-LTD/static-site-kit/sitegen/pyfmt"
)

// SEOOptions は SEO の部品への追加の指定
type SEOOptions struct {
	// Org は構造化データの Organization に足す項目（telephone など）。nil なら足さない
	Org *pyfmt.Obj
}

// OrgID は構造化データの Organization の @id
func (s *Site) OrgID() string { return s.base + "#organization" }

// Canonical は canonical の <link>
func (s *Site) Canonical(url string) string {
	return fmt.Sprintf(`<link rel="canonical" href="%s" />`, s.PageURL(url))
}

// OGImage は og:image の絶対 URL。ページの Image、無ければ site.json の og_image
func (s *Site) OGImage(m *Meta) string {
	src := m.Image
	if src == "" {
		src = pyfmt.Str(s.conf.At("og_image"))
	}
	return s.AbsURL(src)
}

// OGP は og:* と twitter:* の <meta>（og:locale から twitter:image まで）。
// og:title は m.Title をもう一度エスケープして入れる（Title が HTML として安全な形になっていても二重になる。
// 先行実装の出力とバイト単位で同じにするため、この形を保つ）
func (s *Site) OGP(url string, m *Meta) []string {
	full := s.PageURL(url)
	image := s.OGImage(m)
	ogType := "website"
	if m.OGType != "" {
		ogType = m.OGType
	}
	return []string{
		`<meta property="og:locale" content="ja_JP" />`,
		fmt.Sprintf(`<meta property="og:type" content="%s" />`, ogType),
		fmt.Sprintf(`<meta property="og:title" content="%s" />`, pyfmt.Esc(m.Title)),
		fmt.Sprintf(`<meta property="og:url" content="%s" />`, full),
		fmt.Sprintf(`<meta property="og:site_name" content="%s" />`, pyfmt.Esc(s.conf.At("company"))),
		fmt.Sprintf(`<meta property="og:image" content="%s" />`, pyfmt.Esc(image)),
		`<meta name="twitter:card" content="summary_large_image" />`,
		fmt.Sprintf(`<meta name="twitter:image" content="%s" />`, pyfmt.Esc(image)),
	}
}

// SEO は <head> に入れる SEO 情報の全部入り。title・description・canonical・OGP・JSON-LD を 1 行ずつ並べ、末尾に改行を付ける。
// m.Title は HTML としてそのまま入る
func (s *Site) SEO(url string, m *Meta, o SEOOptions) string {
	seo := []string{fmt.Sprintf("<title>%s</title>", m.Title)}
	if m.Description != "" {
		seo = append(seo, fmt.Sprintf(`<meta name="description" content="%s" />`, pyfmt.Esc(m.Description)))
	}
	seo = append(seo, s.Canonical(url))
	seo = append(seo, s.OGP(url, m)...)
	seo = append(seo, s.JSONLD(url, m, o))
	return strings.Join(seo, "\n") + "\n"
}

// JSONLD は構造化データ（JSON-LD）の <script>。全ページに Organization・WebSite・WebPage、
// m.HasCrumbs ならパンくず（トップ以外）、m.LD にページ固有の物（記事など）を入れる。中身からビルドのたびに作り直す
func (s *Site) JSONLD(url string, m *Meta, o SEOOptions) string {
	full := s.PageURL(url)
	image := s.OGImage(m)
	name := pyfmt.Strip(strings.ReplaceAll(pyfmt.HTMLUnescape(m.Title), s.suffix, ""))
	pageID := full + "#webpage"
	webpage := pyfmt.O("@type", "WebPage", "@id", pageID, "url", full, "name", name, "isPartOf", pyfmt.O("@id", s.base+"#website"),
		"primaryImageOfPage", pyfmt.O("@type", "ImageObject", "url", image), "inLanguage", "ja")
	org := pyfmt.O("@type", "Organization", "@id", s.OrgID(), "name", s.conf.At("company"), "url", s.base,
		"logo", s.AbsURL(pyfmt.Str(s.conf.At("logo"))))
	if o.Org != nil {
		for _, k := range o.Org.Keys() {
			org.Set(k, o.Org.Get(k))
		}
	}
	graph := []any{
		org,
		pyfmt.O("@type", "WebSite", "@id", s.base+"#website", "url", s.base, "name", s.conf.At("company"), "publisher", pyfmt.O("@id", s.OrgID()), "inLanguage", "ja"),
		webpage,
	}
	if url != "" && m.HasCrumbs {
		trail := []Crumb{{"HOME", s.base}}
		for _, c := range m.Crumbs {
			trail = append(trail, Crumb{c.Name, s.AbsURL(c.URL)})
		}
		trail = append(trail, Crumb{name, full})
		webpage.Set("breadcrumb", pyfmt.O("@id", full+"#breadcrumb"))
		items := make([]any, len(trail))
		for i, t := range trail {
			items[i] = pyfmt.O("@type", "ListItem", "position", pyfmt.IntNum(i+1), "name", t.Name, "item", t.URL)
		}
		graph = append(graph, pyfmt.O("@type", "BreadcrumbList", "@id", full+"#breadcrumb", "itemListElement", items))
	}
	for _, node := range m.LD {
		n := pyfmt.NewObj()
		for _, k := range node.Keys() {
			n.Set(k, node.Get(k))
		}
		n.Set("mainEntityOfPage", pyfmt.O("@id", pageID))
		graph = append(graph, n)
	}
	data := strings.ReplaceAll(pyfmt.Dump(pyfmt.O("@context", "https://schema.org", "@graph", graph), pyfmt.DumpCompact), "</", `<\/`)
	return `<script type="application/ld+json">` + data + `</script>`
}

// ArticleLD は記事の構造化データの 1 件（m.LD に入れる）。書き手・発行元は Organization。
// kind は BlogPosting・Article など、published と modified は日付、image はサイト内のパス（空なら入れない）
func (s *Site) ArticleLD(kind, headline string, published, modified any, image, description string) *pyfmt.Obj {
	node := pyfmt.O("@type", kind, "headline", pyfmt.Head(headline, 110), "datePublished", published, "dateModified", modified,
		"author", pyfmt.O("@id", s.OrgID()), "publisher", pyfmt.O("@id", s.OrgID()))
	if image != "" {
		node.Set("image", []any{s.AbsURL(image)})
	}
	if description != "" {
		node.Set("description", description)
	}
	return node
}
