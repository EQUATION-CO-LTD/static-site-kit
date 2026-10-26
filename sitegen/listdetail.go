package sitegen

// 記事（お知らせ・ブログなど）の定番の Builder。一覧（ページ送りつき）・カテゴリ別の一覧・個別ページを、
// site.json の設定とテンプレートだけで作る。

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/EQUATION-CO-LTD/static-site-kit/sitegen/pyfmt"
)

// Cat は記事のカテゴリ（URL に入るスラッグと表示名）
type Cat struct{ Slug, Name string }

// ListDetail は 1 種類の記事の一覧・カテゴリ別の一覧・個別ページを足す Builder。
//
// 使うファイルの約束:
//
//	tpl/{Kind}-list.html  一覧。{{H1}} {{CAT_LIST}} {{ITEMS}} {{PAGINATION}}
//	tpl/{Kind}-item.html  一覧の 1 件。{{URL}} {{TITLE}} {{TITLE_ATTR}} {{DATE}} {{CATS}} {{EYECATCH}} {{DESCRIPTION}}
//	tpl/{Kind}-post.html  個別ページ。{{CATS}} {{DATE}} {{TITLE}} {{TITLE_ATTR}} {{EYECATCH}} {{BODY}}
//	content/{PastDir}/{Kind}.json, content/{CMSDir}/{Kind}.json  記事の配列。各記事は id・date（YYYY-MM-DD）・title・category・body、
//	                      あれば eyecatch（サイト内のパス）・description。title と description は HTML として安全な形（エスケープ済み）で持つ
//
// 記事は過去分に取得分（microCMS）を重ねる。同じ id は取得分を優先し（id の型も同じ物だけを同じとみなす）、
// 日付の新しい順に並べる。同じ日付は取得分・元の並びを優先する。
type ListDetail struct {
	// Kind はテンプレートとデータのファイル名の頭（news なら tpl/news-list.html と news.json）。必須
	Kind string
	// ListURL は一覧の URL（先頭の / なし）。空なら Kind
	ListURL string
	// Label は一覧の見出しとタイトル
	Label string
	// Cats はカテゴリの一覧（表示の順）。記事のカテゴリがここに無ければ止める
	Cats []Cat
	// PostURL は個別ページの URL の形。{id} と {yyyymmdd}（日付の - を除いたもの）が使える
	PostURL string
	// CatURL はカテゴリ別の一覧の URL の形。{cat}（スラッグ）が使える。カテゴリ別の一覧の 2 ページ目以降は {CatURL}/page/N
	CatURL string
	// CatTitle・CatH1 はカテゴリ別の一覧のタイトルと見出し。{name}（表示名）が使える
	CatTitle, CatH1 string
	// Description は一覧のページの description。空なら site.json の description
	Description string
	// Excerpt は一覧に抜粋（description）を出す種類か。ページの組み立てでは使わない。取得（fetch）側が読む
	Excerpt bool
	// Nav はこの種類のページでナビの現在地にする項目（Meta.Extra の "nav"）。空なら付けない
	Nav string
	// Indent はカテゴリの一覧とページ送りの HTML のインデント（タブの数）。テンプレート内の置かれた深さに合わせる
	Indent int
	// PerPage は 1 ページの件数。0 なら site.json の per_page、それも無ければ 10
	PerPage int
	// TitleSuffix はページの title の末尾。空なら site.json の title_suffix
	TitleSuffix string
	// AllLabel はカテゴリの一覧の先頭（全件）のリンクの文字。空なら「全表示」
	AllLabel string
	// PartTitle は 2 ページ目以降の title に足す文字の形（ページ番号は %d）。空なら " - Page %d"
	PartTitle string
	// PastDir・CMSDir は過去分と取得分を置いた、中身のディレクトリの下の場所（"." は中身のディレクトリ自身）。
	// 既定は seed と cms
	PastDir, CMSDir string
	// Pagination はページ送りの HTML を返す。current は今のページ（0 から）、urls は全ページの URL。
	// nil なら PostPagination
	Pagination func(s *Site, indent, current int, urls []string) string
}

// ListDetailFromConfig は site.json の 1 種類分の設定（posts.{kind} など）から ListDetail を作る。読むキー:
// label・cats（[スラッグ, 表示名] の配列）・post_url・cat_url・cat_title・cat_h1・description・excerpt・nav・indent
func ListDetailFromConfig(kind string, o *pyfmt.Obj) *ListDetail {
	d := &ListDetail{
		Kind:        kind,
		Label:       pyfmt.Str(o.At("label")),
		PostURL:     pyfmt.Str(o.At("post_url")),
		CatURL:      pyfmt.Str(o.At("cat_url")),
		CatTitle:    pyfmt.Str(o.At("cat_title")),
		CatH1:       pyfmt.Str(o.At("cat_h1")),
		Description: pyfmt.Str(pyfmt.Or(o.Get("description"), "")),
		Excerpt:     pyfmt.Truthy(o.Get("excerpt")),
		Nav:         pyfmt.Str(pyfmt.Or(o.Get("nav"), "")),
	}
	if o.Has("indent") {
		d.Indent = pyfmt.AsInt(o.Get("indent"))
	}
	for _, c := range pyfmt.AsList(o.At("cats")) {
		pair := pyfmt.AsList(c)
		d.Cats = append(d.Cats, Cat{pyfmt.Str(pair[0]), pyfmt.Str(pair[1])})
	}
	return d
}

// ListDetailsFrom は site.json の key（"posts" など。種類 → 設定のオブジェクト）に書かれたすべての種類を、
// 書かれた順に ListDetail で作る Builder。adjust が非 nil なら、各 ListDetail を Build の前に調整できる
func ListDetailsFrom(key string, adjust func(s *Site, d *ListDetail)) Builder {
	return BuilderFunc(func(s *Site) error {
		if !s.Config().Has(key) {
			return nil
		}
		conf := pyfmt.AsObj(s.Config().At(key))
		for _, kind := range conf.Keys() {
			d := ListDetailFromConfig(kind, pyfmt.AsObj(conf.Get(kind)))
			if adjust != nil {
				adjust(s, d)
			}
			if err := d.Build(s); err != nil {
				return err
			}
		}
		return nil
	})
}

// PostPagination は既定のページ送りの HTML。ページが 1 つなら空。
// 前へ（&laquo;）・各ページの番号（今のページは <span>）・次へ（&raquo;）を indent 個のタブで字下げして並べる
func PostPagination(s *Site, indent, current int, urls []string) string {
	if len(urls) < 2 {
		return ""
	}
	t := strings.Repeat("\t", indent)
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s<center><ul class=\"post-pagination\">\n", t)
	if current > 0 {
		fmt.Fprintf(&sb, "%s\t<li class=\"prev\"><a href=\"%s\">&laquo;</a></li>\n", t, s.Href(urls[current-1]))
	}
	for i, u := range urls {
		if i == current {
			fmt.Fprintf(&sb, "%s\t<li class=\"active\"><span>%d</span></li>\n", t, i+1)
		} else {
			fmt.Fprintf(&sb, "%s\t<li><a href=\"%s\">%d</a></li>\n", t, s.Href(u), i+1)
		}
	}
	if current < len(urls)-1 {
		fmt.Fprintf(&sb, "%s\t<li class=\"next\"><a href=\"%s\">&raquo;</a></li>\n", t, s.Href(urls[current+1]))
	}
	fmt.Fprintf(&sb, "%s</ul></center>\n", t)
	return sb.String()
}

// ldPost は組み立て中の記事
type ldPost struct {
	obj                   *pyfmt.Obj
	id, date, title, body string
	category              string
	url                   string // 個別ページの URL（先頭の / なし）
	cats                  string // 記事に付けるカテゴリの HTML
}

// idKey は id が同じかを決める鍵。型も合わせる（文字列の "1" と数の 1 は別の物）
func idKey(v any) string {
	switch x := v.(type) {
	case string:
		return "s:" + x
	case pyfmt.Num:
		return "n:" + strconv.FormatFloat(x.F, 'g', -1, 64)
	}
	return "o:" + pyfmt.Str(v)
}

// placeholders は形の中の {…} を values で置き、使えない {…} が残っていれば止める（field は誤りに出す設定の名前）
func (d *ListDetail) placeholders(field, form string, values map[string]string) string {
	out := form
	for k, v := range values {
		out = strings.ReplaceAll(out, "{"+k+"}", v)
	}
	if strings.ContainsAny(out, "{}") {
		allowed := make([]string, 0, len(values))
		for k := range values {
			allowed = append(allowed, "{"+k+"}")
		}
		sort.Strings(allowed)
		Die("%s の %s「%s」に使えない { } がある（使えるのは %s）", d.Kind, field, form, strings.Join(allowed, " "))
	}
	return out
}

// load は過去分に取得分を重ね、日付の新しい順に並べた記事と、うち取得分の件数を返す
func (d *ListDetail) load(s *Site) (posts []*ldPost, nCMS int) {
	pastDir, cmsDir := d.PastDir, d.CMSDir
	if pastDir == "" {
		pastDir = "seed"
	}
	if cmsDir == "" {
		cmsDir = "cms"
	}
	read := func(dir string) []*pyfmt.Obj {
		data, ok := s.ContentIn(dir, d.Kind)
		if !ok {
			return nil
		}
		return pyfmt.AsObjs(data)
	}
	cms := read(cmsDir)
	taken := map[string]bool{}
	for _, o := range cms {
		taken[idKey(o.At("id"))] = true
	}
	all := append([]*pyfmt.Obj(nil), cms...)
	for _, o := range read(pastDir) {
		if !taken[idKey(o.At("id"))] {
			all = append(all, o)
		}
	}
	for _, o := range all {
		posts = append(posts, &ldPost{obj: o, id: pyfmt.Str(o.At("id")), date: pyfmt.Str(o.At("date")), title: pyfmt.Str(o.At("title")),
			category: pyfmt.Str(o.At("category")), body: pyfmt.Str(o.At("body"))})
	}
	sort.SliceStable(posts, func(i, j int) bool { return posts[i].date > posts[j].date })
	return posts, len(cms)
}

// Build は一覧・カテゴリ別の一覧・個別ページを足す
func (d *ListDetail) Build(s *Site) error {
	if d.Kind == "" {
		Die("ListDetail の Kind が空")
	}
	tplList, tplItem, tplPost := d.Kind+"-list", d.Kind+"-item", d.Kind+"-post"
	list, item, post := s.Tpl(tplList), s.Tpl(tplItem), s.Tpl(tplPost)

	perPage := d.PerPage
	if perPage == 0 {
		perPage = 10
		if s.conf.Has("per_page") {
			perPage = pyfmt.AsInt(s.conf.Get("per_page"))
		}
	}
	if perPage < 1 {
		Die("%s の 1 ページの件数が 1 未満", d.Kind)
	}
	suffix := d.TitleSuffix
	if suffix == "" {
		suffix = s.TitleSuffix()
	}
	partTitle, allLabel, listURL := d.PartTitle, d.AllLabel, d.ListURL
	if partTitle == "" {
		partTitle = " - Page %d"
	}
	if allLabel == "" {
		allLabel = "全表示"
	}
	if listURL == "" {
		listURL = d.Kind
	}
	pagination := d.Pagination
	if pagination == nil {
		pagination = PostPagination
	}
	siteDesc := pyfmt.Str(pyfmt.Getd(s.conf, "description", ""))
	typeDesc := d.Description
	if typeDesc == "" {
		typeDesc = siteDesc
	}
	defaultEyecatch := "" // アイキャッチが空の記事に出す画像（サイトの og_image）
	if og := pyfmt.Str(pyfmt.Getd(s.conf, "og_image", "")); og != "" {
		defaultEyecatch = "/" + strings.TrimLeft(strings.TrimPrefix(og, s.base), "/")
	}

	catName := map[string]string{}
	for _, c := range d.Cats {
		catName[c.Slug] = c.Name
	}
	catURL := func(slug string) string {
		return d.placeholders("cat_url", d.CatURL, map[string]string{"cat": slug})
	}
	posts, nCMS := d.load(s)
	for _, p := range posts {
		name, ok := catName[p.category]
		if !ok {
			Die("%s %s: 未知のカテゴリ %s（カテゴリの一覧 cats に足すか、記事のカテゴリを直す）", d.Kind, p.id, p.category)
		}
		p.url = d.placeholders("post_url", d.PostURL, map[string]string{"id": p.id, "yyyymmdd": strings.ReplaceAll(p.date, "-", "")})
		p.cats = fmt.Sprintf(`<ul class="post-cat-list clearfix"><li><a href="%s">%s</a></li></ul>`, s.Href(catURL(p.category)), name)
	}

	count := map[string]int{}
	for _, p := range posts {
		count[p.category]++
	}
	li := strings.Repeat("\t", d.Indent+1)
	var catList strings.Builder
	fmt.Fprintf(&catList, "%s<li><a href=\"%s\">%s</a></li>\n", li, s.Href(d.Kind), allLabel)
	for _, c := range d.Cats {
		if count[c.Slug] > 0 {
			fmt.Fprintf(&catList, "%s<li><a href=\"%s\">%s(%d)</a></li>\n", li, s.Href(catURL(c.Slug)), c.Name, count[c.Slug])
		}
	}

	eyecatch := func(p *ldPost) string { return pyfmt.Str(pyfmt.Or(p.obj.Get("eyecatch"), defaultEyecatch)) }
	lists := func(base, title, h1 string, items []*ldPost) {
		chunks := Paginate(base, items, perPage)
		urls := make([]string, len(chunks))
		for i, c := range chunks {
			urls[i] = c.URL
		}
		for i, c := range chunks {
			var rows strings.Builder
			for _, p := range c.Items {
				rows.WriteString(Fill(item, map[string]string{
					"URL": s.Href(p.url), "TITLE": p.title, "TITLE_ATTR": p.title, "DATE": strings.ReplaceAll(p.date, "-", "/"),
					"CATS": p.cats, "EYECATCH": eyecatch(p), "DESCRIPTION": pyfmt.Str(pyfmt.Getd(p.obj, "description", "")),
				}, "tpl/"+tplItem+".html"))
			}
			t := title + suffix
			if i > 0 {
				t += fmt.Sprintf(partTitle, i+1)
			}
			m := NewMeta("title", t, "description", typeDesc)
			if d.Nav != "" {
				m.Set("nav", d.Nav)
			}
			s.AddPage(urls[i], m, Fill(list, map[string]string{
				"H1": h1, "CAT_LIST": catList.String(), "ITEMS": rows.String(), "PAGINATION": pagination(s, d.Indent, i, urls),
			}, "tpl/"+tplList+".html"))
		}
	}

	lists(listURL, d.Label, d.Label, posts)
	for _, c := range d.Cats {
		var inCat []*ldPost
		for _, p := range posts {
			if p.category == c.Slug {
				inCat = append(inCat, p)
			}
		}
		if len(inCat) > 0 {
			lists(catURL(c.Slug), d.placeholders("cat_title", d.CatTitle, map[string]string{"name": c.Name}),
				d.placeholders("cat_h1", d.CatH1, map[string]string{"name": c.Name}), inCat)
		}
	}

	for _, p := range posts {
		m := NewMeta("title", p.title+suffix, "og_type", "article",
			"description", pyfmt.Str(pyfmt.Or(p.obj.Get("description"), siteDesc)))
		if d.Nav != "" {
			m.Set("nav", d.Nav)
		}
		if pyfmt.Truthy(p.obj.Get("eyecatch")) {
			m.Set(KeyImage, s.base+strings.TrimLeft(pyfmt.Str(p.obj.Get("eyecatch")), "/"))
		}
		s.AddPage(p.url, m, Fill(post, map[string]string{
			"CATS": p.cats, "DATE": strings.ReplaceAll(p.date, "-", "/"), "TITLE": p.title, "TITLE_ATTR": p.title,
			"EYECATCH": eyecatch(p), "BODY": p.body,
		}, "tpl/"+tplPost+".html"))
	}
	fmt.Fprintf(s.stdout, "%s: 記事 %d 件（うち microCMS %d 件）\n", d.Kind, len(posts), nCMS)
	return nil
}
