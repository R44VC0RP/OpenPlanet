package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"html"
	"os"
	"regexp"
	"strings"
)

var inlineCode = regexp.MustCompile("`([^`]+)`")

func main() {
	inPath := flag.String("in", "docs/ggp-cell-v1.md", "markdown input path")
	outPath := flag.String("out", "public/index.html", "html output path")
	flag.Parse()

	input, err := os.Open(*inPath)
	if err != nil {
		fatal(err)
	}
	defer input.Close()

	body, err := renderMarkdown(input)
	if err != nil {
		fatal(err)
	}
	page := renderPage(body)
	if err := os.WriteFile(*outPath, []byte(page), 0o644); err != nil {
		fatal(err)
	}
}

func renderMarkdown(input *os.File) (string, error) {
	scanner := bufio.NewScanner(input)
	var b strings.Builder
	inCode := false
	inTable := false
	inList := false
	codeLang := ""

	closeTable := func() {
		if inTable {
			b.WriteString("</tbody></table>\n")
			inTable = false
		}
	}
	closeList := func() {
		if inList {
			b.WriteString("</ol>\n")
			inList = false
		}
	}
	closeBlocks := func() {
		closeTable()
		closeList()
	}

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if inCode {
				b.WriteString("</code></pre>\n")
				inCode = false
				continue
			}
			closeBlocks()
			codeLang = strings.TrimPrefix(trimmed, "```")
			b.WriteString(`<pre><code class="language-` + html.EscapeString(codeLang) + `">`)
			inCode = true
			continue
		}
		if inCode {
			b.WriteString(html.EscapeString(line))
			b.WriteString("\n")
			continue
		}
		if trimmed == "" {
			closeBlocks()
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			closeBlocks()
			level := 0
			for level < len(trimmed) && trimmed[level] == '#' {
				level++
			}
			text := strings.TrimSpace(trimmed[level:])
			id := slug(text)
			b.WriteString(fmt.Sprintf("<h%d id=\"%s\">%s</h%d>\n", level, id, renderInline(text), level))
			continue
		}
		if strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|") {
			closeList()
			cells := tableCells(trimmed)
			if len(cells) == 0 || isSeparatorRow(cells) {
				continue
			}
			if !inTable {
				b.WriteString("<table><tbody>\n")
				inTable = true
			}
			b.WriteString("<tr>")
			for _, cell := range cells {
				b.WriteString("<td>")
				b.WriteString(renderInline(cell))
				b.WriteString("</td>")
			}
			b.WriteString("</tr>\n")
			continue
		}
		if isNumbered(trimmed) {
			closeTable()
			if !inList {
				b.WriteString("<ol>\n")
				inList = true
			}
			_, rest, _ := strings.Cut(trimmed, ".")
			b.WriteString("<li>")
			b.WriteString(renderInline(strings.TrimSpace(rest)))
			b.WriteString("</li>\n")
			continue
		}
		closeBlocks()
		b.WriteString("<p>")
		b.WriteString(renderInline(trimmed))
		b.WriteString("</p>\n")
	}
	closeBlocks()
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return b.String(), nil
}

func renderInline(value string) string {
	escaped := html.EscapeString(value)
	return inlineCode.ReplaceAllString(escaped, "<code>$1</code>")
}

func tableCells(line string) []string {
	line = strings.Trim(line, "|")
	parts := strings.Split(line, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func isSeparatorRow(cells []string) bool {
	for _, cell := range cells {
		for _, r := range cell {
			if r != '-' && r != ':' && r != ' ' {
				return false
			}
		}
	}
	return true
}

func isNumbered(line string) bool {
	dot := strings.Index(line, ".")
	if dot < 1 || dot > 3 || dot+1 >= len(line) || line[dot+1] != ' ' {
		return false
	}
	for _, r := range line[:dot] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func slug(value string) string {
	value = strings.ToLower(value)
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case !lastDash:
			b.WriteRune('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func renderPage(body string) string {
	var page bytes.Buffer
	page.WriteString(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>GGP Cell v1 Spec</title>
<style>
:root{color-scheme:dark;--bg:#0b1120;--panel:#111827;--text:#dbeafe;--muted:#94a3b8;--line:#263244;--accent:#7dd3fc;--code:#020617}*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--text);font:16px/1.6 ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}main{max-width:980px;margin:0 auto;padding:48px 22px 80px}header{border-bottom:1px solid var(--line);margin-bottom:32px;padding-bottom:24px}h1,h2,h3{line-height:1.2;color:white;margin:34px 0 12px}h1{font-size:42px;margin-top:0}h2{font-size:28px;border-top:1px solid var(--line);padding-top:28px}h3{font-size:22px}p{margin:0 0 14px;color:var(--text)}a{color:var(--accent)}code{background:var(--code);border:1px solid var(--line);border-radius:5px;padding:.1rem .32rem;color:#bae6fd}pre{background:var(--code);border:1px solid var(--line);border-radius:10px;overflow:auto;padding:16px;margin:18px 0}pre code{border:0;padding:0;background:transparent}table{width:100%;border-collapse:collapse;margin:18px 0;background:var(--panel);border:1px solid var(--line);border-radius:10px;overflow:hidden}td{border-bottom:1px solid var(--line);padding:10px 12px;vertical-align:top}tr:first-child td{font-weight:700;color:white;background:#172033}tr:last-child td{border-bottom:0}ol{padding-left:24px}li{margin:6px 0}.eyebrow{color:var(--accent);font-weight:700;letter-spacing:.08em;text-transform:uppercase}.subtitle{color:var(--muted);font-size:18px;max-width:760px}@media(max-width:640px){main{padding:30px 16px 56px}h1{font-size:32px}h2{font-size:24px}table{font-size:14px}}
</style>
</head>
<body>
<main>
<header><div class="eyebrow">Game Gateway Protocol</div><h1>GGP Cell v1</h1><p class="subtitle">Language-neutral WebSocket protocol for terminal-rendered games inside the SSH gateway.</p></header>
`)
	page.WriteString(body)
	page.WriteString("</main></body></html>\n")
	return page.String()
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
