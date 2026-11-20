package audiobook

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type testEPUBDoc struct {
	id       string
	href     string
	navTitle string
	body     string
}

// writeTestEPUB builds a small EPUB 2 book in a temporary directory.
func writeTestEPUB(t *testing.T) string {
	t.Helper()
	return writeEPUB(t, []testEPUBDoc{
		{
			id:       "c1",
			href:     "chapter1.xhtml",
			navTitle: "第一章 开端",
			body:     `<nav><p>导航文字不要出现</p></nav><h1>HTML 标题一</h1><p>第一章正文从这里开始。这是第二句话，用来凑够三十个字符。最后一句结束！</p>`,
		},
		{
			id:   "c2",
			href: "chapter2.xhtml",
			body: `<h2>第二章 相遇</h2><p>第二章正文内容同样需要超过三十个字符，否则会被过滤掉。这里再加一句话保证长度。</p>`,
		},
		{
			id:   "c3",
			href: "chapter3.xhtml",
			body: `<p>太短了。</p>`,
		},
	})
}

func writeEPUB(t *testing.T, docs []testEPUBDoc) string {
	t.Helper()
	dir := t.TempDir()
	epubPath := filepath.Join(dir, "book.epub")
	f, err := os.Create(epubPath)
	if err != nil {
		t.Fatalf("create epub: %v", err)
	}
	zw := zip.NewWriter(f)
	add := func(name, content string) {
		t.Helper()
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := io.WriteString(w, content); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}

	add("mimetype", "application/epub+zip")
	add("META-INF/container.xml", `<?xml version="1.0" encoding="utf-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`)

	var manifest, spine strings.Builder
	for _, d := range docs {
		fmt.Fprintf(&manifest, "\n    <item id=%q href=%q media-type=\"application/xhtml+xml\"/>", d.id, d.href)
		fmt.Fprintf(&spine, "\n    <itemref idref=%q/>", d.id)
		doc := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml">
<head><title>%s</title><style>p { color: red; }</style></head>
<body>%s</body>
</html>`, d.navTitle, d.body)
		add("OEBPS/"+d.href, doc)
	}

	add("OEBPS/content.opf", fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="uid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>测试书</dc:title></metadata>
  <manifest>
    <item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>%s
  </manifest>
  <spine toc="ncx">%s
  </spine>
</package>`, manifest.String(), spine.String()))

	var points strings.Builder
	order := 0
	for _, d := range docs {
		if d.navTitle == "" {
			continue
		}
		order++
		fmt.Fprintf(&points, `<navPoint id="np%d" playOrder="%d"><navLabel><text>%s</text></navLabel><content src="%s"/></navPoint>`,
			order, order, d.navTitle, d.href)
	}
	add("OEBPS/toc.ncx", fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">
  <navMap>%s</navMap>
</ncx>`, points.String()))

	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close epub: %v", err)
	}
	return epubPath
}

func TestParseEPUB(t *testing.T) {
	chapters, err := ParseEPUB(writeTestEPUB(t))
	if err != nil {
		t.Fatalf("ParseEPUB: %v", err)
	}
	if len(chapters) != 2 {
		t.Fatalf("chapters = %d, want 2 (short chapter must be filtered)", len(chapters))
	}
	if chapters[0].Index != 1 || chapters[1].Index != 2 {
		t.Fatalf("indexes = %d,%d, want 1,2", chapters[0].Index, chapters[1].Index)
	}
	if chapters[0].Title != "第一章 开端" {
		t.Errorf("chapter 1 title = %q, want NCX title", chapters[0].Title)
	}
	if chapters[1].Title != "第二章 相遇" {
		t.Errorf("chapter 2 title = %q, want h2 fallback", chapters[1].Title)
	}
	if !strings.Contains(chapters[0].Text, "第一章正文从这里开始") {
		t.Errorf("chapter 1 text missing body: %q", chapters[0].Text)
	}
	if strings.Contains(chapters[0].Text, "导航文字") {
		t.Errorf("chapter 1 text must skip <nav>: %q", chapters[0].Text)
	}
	if strings.Contains(chapters[0].Text, "color: red") {
		t.Errorf("chapter 1 text must skip <style>: %q", chapters[0].Text)
	}
	for _, ch := range chapters {
		if ch.Chars != len([]rune(ch.Text)) {
			t.Errorf("chapter %d chars = %d, want %d", ch.Index, ch.Chars, len([]rune(ch.Text)))
		}
		if ch.Chars < 30 {
			t.Errorf("chapter %d kept with only %d chars", ch.Index, ch.Chars)
		}
	}
}

func TestParseNavTitles(t *testing.T) {
	data := []byte(`<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><body>
  <nav epub:type="toc"><ol>
    <li><a href="chapter1.xhtml">第一章 开端</a></li>
    <li><a href="chapter2.xhtml#part2">第二章 相遇</a></li>
  </ol></nav>
</body></html>`)
	titles := parseNavTitles(data, "OEBPS/nav.xhtml")
	if got := titles["OEBPS/chapter1.xhtml"]; got != "第一章 开端" {
		t.Errorf("chapter1 title = %q", got)
	}
	if got := titles["OEBPS/chapter2.xhtml"]; got != "第二章 相遇" {
		t.Errorf("chapter2 title = %q", got)
	}
}

func TestParseEPUBNumberedTitleFallback(t *testing.T) {
	path := writeEPUB(t, []testEPUBDoc{{
		id:   "c1",
		href: "only.xhtml",
		body: "<p>" + strings.Repeat("春", 40) + "。</p>",
	}})
	chapters, err := ParseEPUB(path)
	if err != nil {
		t.Fatalf("ParseEPUB: %v", err)
	}
	if len(chapters) != 1 {
		t.Fatalf("chapters = %d, want 1", len(chapters))
	}
	if chapters[0].Title != "第1章" {
		t.Errorf("title = %q, want 第1章", chapters[0].Title)
	}
}
