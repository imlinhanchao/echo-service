// Package audiobook implements EPUB parsing, Chinese sentence segmentation,
// TTS synthesis orchestration, WAV mastering and SRT subtitle generation.
package audiobook

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

// Chapter is one readable chapter extracted from an EPUB.
type Chapter struct {
	Index int
	Title string
	Text  string
	Chars int
}

type containerFile struct {
	Rootfiles []struct {
		FullPath string `xml:"full-path,attr"`
	} `xml:"rootfiles>rootfile"`
}

type opfItem struct {
	ID         string `xml:"id,attr"`
	Href       string `xml:"href,attr"`
	MediaType  string `xml:"media-type,attr"`
	Properties string `xml:"properties,attr"`
}

type opfItemRef struct {
	IDRef string `xml:"idref,attr"`
}

type opfPackage struct {
	Manifest struct {
		Items []opfItem `xml:"item"`
	} `xml:"manifest"`
	Spine struct {
		Toc   string       `xml:"toc,attr"`
		Items []opfItemRef `xml:"itemref"`
	} `xml:"spine"`
}

type ncxNavPoint struct {
	Label struct {
		Text string `xml:"text"`
	} `xml:"navLabel"`
	Content struct {
		Src string `xml:"src,attr"`
	} `xml:"content"`
	Children []ncxNavPoint `xml:"navPoint"`
}

type ncxDocument struct {
	NavMap struct {
		Points []ncxNavPoint `xml:"navPoint"`
	} `xml:"navMap"`
}

// ParseEPUB reads an EPUB and returns its readable chapters in spine order.
// Fragments shorter than 30 runes are dropped. Titles come from the NCX/EPUB3
// navigation document first, then the first h1-h3 of the document, and finally
// "第N章".
func ParseEPUB(epubPath string) ([]Chapter, error) {
	zr, err := zip.OpenReader(epubPath)
	if err != nil {
		return nil, fmt.Errorf("打开 EPUB: %w", err)
	}
	defer zr.Close()

	files := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		files[cleanZipName(f.Name)] = f
	}

	readFile := func(name string) ([]byte, error) {
		name = cleanZipName(name)
		f, ok := files[name]
		if !ok {
			return nil, fmt.Errorf("EPUB 缺少文件 %s", name)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("读取 EPUB 文件 %s: %w", name, err)
		}
		defer rc.Close()
		data, err := io.ReadAll(rc)
		if err != nil {
			return nil, fmt.Errorf("读取 EPUB 文件 %s: %w", name, err)
		}
		return data, nil
	}

	containerData, err := readFile("META-INF/container.xml")
	if err != nil {
		return nil, err
	}
	var container containerFile
	if err := xml.Unmarshal(containerData, &container); err != nil {
		return nil, fmt.Errorf("解析 container.xml: %w", err)
	}
	if len(container.Rootfiles) == 0 || strings.TrimSpace(container.Rootfiles[0].FullPath) == "" {
		return nil, errors.New("container.xml 中没有 rootfile")
	}
	opfPath := cleanZipName(container.Rootfiles[0].FullPath)

	opfData, err := readFile(opfPath)
	if err != nil {
		return nil, err
	}
	var pkg opfPackage
	if err := xml.Unmarshal(opfData, &pkg); err != nil {
		return nil, fmt.Errorf("解析 OPF: %w", err)
	}

	items := make(map[string]opfItem, len(pkg.Manifest.Items))
	var navItem *opfItem
	var ncxItem *opfItem
	for i := range pkg.Manifest.Items {
		it := pkg.Manifest.Items[i]
		items[it.ID] = it
		if hasToken(it.Properties, "nav") {
			navItem = &pkg.Manifest.Items[i]
		}
		if strings.EqualFold(it.MediaType, "application/x-dtbncx+xml") {
			ncxItem = &pkg.Manifest.Items[i]
		}
	}

	titles := make(map[string]string)
	if pkg.Spine.Toc != "" {
		if it, ok := items[pkg.Spine.Toc]; ok {
			if m := readNCXTitles(readFile, it, opfPath); len(m) > 0 {
				titles = m
			}
		}
	}
	if len(titles) == 0 && ncxItem != nil {
		if m := readNCXTitles(readFile, *ncxItem, opfPath); len(m) > 0 {
			titles = m
		}
	}
	if len(titles) == 0 && navItem != nil {
		navPath := resolveRelative(opfPath, navItem.Href)
		if data, err := readFile(navPath); err == nil {
			titles = parseNavTitles(data, navPath)
		}
	}

	var chapters []Chapter
	for _, ref := range pkg.Spine.Items {
		it, ok := items[ref.IDRef]
		if !ok || !isHTMLItem(it) {
			continue
		}
		docPath := resolveRelative(opfPath, it.Href)
		if docPath == "" {
			continue
		}
		data, err := readFile(docPath)
		if err != nil {
			continue
		}
		text, heading := extractHTMLText(data)
		if len([]rune(text)) < 30 {
			continue
		}
		title := strings.TrimSpace(titles[docPath])
		if title == "" {
			title = heading
		}
		if title == "" {
			title = fmt.Sprintf("第%d章", len(chapters)+1)
		}
		chapters = append(chapters, Chapter{
			Index: len(chapters) + 1,
			Title: title,
			Text:  text,
			Chars: len([]rune(text)),
		})
	}
	if len(chapters) == 0 {
		return nil, errors.New("EPUB 中没有可读章节")
	}
	return chapters, nil
}

func readNCXTitles(readFile func(string) ([]byte, error), it opfItem, opfPath string) map[string]string {
	ncxPath := resolveRelative(opfPath, it.Href)
	if ncxPath == "" {
		return nil
	}
	data, err := readFile(ncxPath)
	if err != nil {
		return nil
	}
	return parseNCXTitles(data, ncxPath)
}

func parseNCXTitles(data []byte, ncxPath string) map[string]string {
	var doc ncxDocument
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil
	}
	out := make(map[string]string)
	var collect func([]ncxNavPoint)
	collect = func(points []ncxNavPoint) {
		for _, p := range points {
			title := strings.TrimSpace(p.Label.Text)
			target := resolveRelative(ncxPath, p.Content.Src)
			if title != "" && target != "" {
				if _, exists := out[target]; !exists {
					out[target] = title
				}
			}
			collect(p.Children)
		}
	}
	collect(doc.NavMap.Points)
	return out
}

func parseNavTitles(data []byte, navPath string) map[string]string {
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	out := make(map[string]string)
	var find func(*html.Node)
	find = func(n *html.Node) {
		if n.Type == html.ElementNode && strings.EqualFold(n.Data, "nav") && isTOCNav(n) {
			collectAnchors(n, navPath, out)
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			find(c)
		}
	}
	find(doc)
	if len(out) == 0 {
		var all func(*html.Node)
		all = func(n *html.Node) {
			if n.Type == html.ElementNode && strings.EqualFold(n.Data, "nav") {
				collectAnchors(n, navPath, out)
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				all(c)
			}
		}
		all(doc)
	}
	return out
}

func isTOCNav(n *html.Node) bool {
	for _, key := range []string{"epub:type", "type", "role"} {
		if strings.Contains(strings.ToLower(getAttr(n, key)), "toc") {
			return true
		}
	}
	return false
}

func collectAnchors(n *html.Node, base string, out map[string]string) {
	if n.Type == html.ElementNode && strings.EqualFold(n.Data, "a") {
		target := resolveRelative(base, getAttr(n, "href"))
		title := strings.TrimSpace(textContent(n))
		if target != "" && title != "" {
			if _, exists := out[target]; !exists {
				out[target] = title
			}
		}
		return
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		collectAnchors(c, base, out)
	}
}

func getAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

func hasToken(list, token string) bool {
	for _, f := range strings.Fields(strings.ToLower(list)) {
		if f == token {
			return true
		}
	}
	return false
}

func isHTMLItem(it opfItem) bool {
	mt := strings.ToLower(strings.TrimSpace(it.MediaType))
	switch mt {
	case "application/xhtml+xml", "text/html":
		return true
	case "":
		ext := strings.ToLower(path.Ext(it.Href))
		return ext == ".xhtml" || ext == ".html" || ext == ".htm"
	default:
		return false
	}
}

// resolveRelative resolves an EPUB href relative to the file that contains it.
func resolveRelative(baseFile, href string) string {
	href = strings.TrimSpace(href)
	if href == "" || strings.Contains(href, "://") {
		return ""
	}
	if i := strings.IndexAny(href, "#?"); i >= 0 {
		href = href[:i]
	}
	if unescaped, err := url.PathUnescape(href); err == nil {
		href = unescaped
	}
	href = strings.TrimPrefix(strings.ReplaceAll(href, "\\", "/"), "/")
	if href == "" {
		return ""
	}
	dir := path.Dir(baseFile)
	if dir == "." || dir == "/" {
		return path.Clean(href)
	}
	return path.Clean(path.Join(dir, href))
}

func cleanZipName(name string) string {
	name = strings.TrimSpace(name)
	if unescaped, err := url.PathUnescape(name); err == nil {
		name = unescaped
	}
	name = strings.TrimPrefix(strings.ReplaceAll(name, "\\", "/"), "/")
	if name == "" {
		return ""
	}
	return path.Clean(name)
}

var skipTextTags = map[string]bool{
	"script":   true,
	"style":    true,
	"head":     true,
	"nav":      true,
	"svg":      true,
	"math":     true,
	"template": true,
	"noscript": true,
}

var blockTags = map[string]bool{
	"p":          true,
	"div":        true,
	"br":         true,
	"li":         true,
	"ul":         true,
	"ol":         true,
	"tr":         true,
	"td":         true,
	"th":         true,
	"table":      true,
	"thead":      true,
	"tbody":      true,
	"h1":         true,
	"h2":         true,
	"h3":         true,
	"h4":         true,
	"h5":         true,
	"h6":         true,
	"blockquote": true,
	"section":    true,
	"article":    true,
	"header":     true,
	"footer":     true,
	"aside":      true,
	"figure":     true,
	"figcaption": true,
	"pre":        true,
	"hr":         true,
	"main":       true,
	"body":       true,
}

type textBuilder struct {
	b    strings.Builder
	last rune
}

func (t *textBuilder) writeRune(r rune) {
	t.b.WriteRune(r)
	t.last = r
}

func (t *textBuilder) space() {
	if t.last != 0 && t.last != ' ' && t.last != '\n' {
		t.b.WriteByte(' ')
		t.last = ' '
	}
}

func (t *textBuilder) newline() {
	if t.last != 0 && t.last != '\n' {
		t.b.WriteByte('\n')
		t.last = '\n'
	}
}

func (t *textBuilder) text(s string) {
	for _, r := range s {
		if unicode.IsSpace(r) {
			t.space()
		} else {
			t.writeRune(r)
		}
	}
}

func extractHTMLText(data []byte) (string, string) {
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return "", ""
	}
	var tb textBuilder
	heading := ""
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.ElementNode:
			tag := strings.ToLower(n.Data)
			if skipTextTags[tag] {
				return
			}
			if heading == "" && (tag == "h1" || tag == "h2" || tag == "h3") {
				if h := strings.TrimSpace(textContent(n)); h != "" {
					heading = h
				}
			}
			block := blockTags[tag]
			if block {
				tb.newline()
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
			if block {
				tb.newline()
			}
		case html.TextNode:
			tb.text(n.Data)
		default:
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
	}
	walk(doc)
	return cleanExtractedText(tb.b.String()), heading
}

func cleanExtractedText(raw string) string {
	lines := strings.Split(raw, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

func textContent(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			return
		}
		if n.Type == html.ElementNode && skipTextTags[strings.ToLower(n.Data)] {
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}
