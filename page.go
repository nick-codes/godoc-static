package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/PuerkitoBio/goquery"
)

func topBar(basePath string, siteName string) string {
	var index string
	if linkIndex {
		index = "index.html"
	}

	return `<div class="container">
<div class="top-heading" id="heading-wide"><a href="` + basePath + index + `">` + siteName + `</a></div>
<div class="top-heading" id="heading-narrow"><a href="` + basePath + index + `">` + siteName + `</a></div>
<!--<a href="#" id="menu-button"><span id="menu-button-arrow">&#9661;</span></a>-->
<div id="menu">
<a href="` + basePath + index + `" style="margin-right: 10px;">Packages</a>
</div>
</div>`
}

func updatePage(doc *goquery.Document, basePath string, siteName string) {
	doc.Find("link").Remove()
	doc.Find("script").Remove()

	linkTag := &html.Node{
		Type:     html.ElementNode,
		DataAtom: atom.Link,
		Data:     "link",
		Attr: []html.Attribute{
			{Key: "type", Val: "text/css"},
			{Key: "rel", Val: "stylesheet"},
			{Key: "href", Val: basePath + "lib/style.css"},
		},
	}

	doc.Find("head").AppendNodes(linkTag)

	doc.Find("#topbar").First().SetHtml(topBar(basePath, siteName))

	doc.Find("a").Each(func(_ int, selection *goquery.Selection) {
		href := selection.AttrOr("href", "")
		if strings.HasPrefix(href, "/src/") || strings.HasPrefix(href, "/pkg/") {
			if strings.ContainsRune(path.Base(href), '.') {
				queryPos := strings.IndexRune(href, '?')
				if queryPos >= 0 {
					href = href[0:queryPos] + ".html" + href[queryPos:]
				} else {
					hashPos := strings.IndexRune(href, '#')
					if hashPos >= 0 {
						href = href[0:hashPos] + ".html" + href[hashPos:]
					} else {
						href += ".html"
					}
				}
			} else if linkIndex {
				queryPos := strings.IndexRune(href, '?')
				if queryPos >= 0 {
					href = href[0:queryPos] + "/index.html" + href[queryPos:]
				} else {
					hashPos := strings.IndexRune(href, '#')
					if hashPos >= 0 {
						href = href[0:hashPos] + "/index.html" + href[hashPos:]
					} else {
						href += "/index.html"
					}
				}
			}

			if strings.HasPrefix(href, "/pkg/") {
				href = href[4:]
			}

			selection.SetAttr("href", basePath+href[1:])
		}
	})

	doc.Find("div").Each(func(_ int, selection *goquery.Selection) {
		if selection.HasClass("toggle") {
			var summary string
			var err error
			selection.Find("div").Each(func(_ int, subSelection *goquery.Selection) {
				if subSelection.HasClass("collapsed") {
					summary, err = subSelection.Find("span.text").First().Html()
					if err != nil {
						summary = "Summary not available"
					}

					subSelection.Remove()
				}
			})

			selection.Find(".toggleButton").Remove()

			selection.PrependHtml(fmt.Sprintf("<summary>%s</summary>", summary))

			selection.RemoveClass("toggle")

			selection.Nodes[0].Data = "details"
			selection.Nodes[0].DataAtom = atom.Details
		}
	})

	scriptTag := &html.Node{
		Type:     html.ElementNode,
		DataAtom: atom.Script,
		Data:     "script",
		Attr: []html.Attribute{
			{Key: "type", Val: "text/javascript"},
			{Key: "src", Val: basePath + "lib/godoc-static.js"},
		},
	}

	doc.Find("body").AppendNodes(scriptTag)

	doc.Find("#footer").Last().Remove()
}

func writeIndex(buf *bytes.Buffer, outDir string, basePath string, siteName string, pkgs []string, filterPkgs []string) {
	var index string
	if linkIndex {
		index = "/index.html"
	}

	var b bytes.Buffer

	b.WriteString(`<!DOCTYPE html>
<html>
<head>
<meta http-equiv="Content-Type" content="text/html; charset=utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="theme-color" content="#375EAB">
<title>` + siteName + `</title>
<link type="text/css" rel="stylesheet" href="` + basePath + `lib/style.css">
</head>
<body>

<div id="lowframe" style="position: fixed; bottom: 0; left: 0; height: 0; width: 100%; border-top: thin solid grey; background-color: white; overflow: auto;">
...
</div><!-- #lowframe -->

<div id="topbar" class="wide">` + topBar(basePath, siteName) + `</div>
<div id="page" class="wide">
<div class="container">
`)

	if siteDescription != "" {
		b.WriteString(siteDescription)
	}

	b.WriteString(`
<h1>
	Packages
</h1>
<div class="pkg-dir">
	<table>
		<tr>
			<th class="pkg-name">Name</th>
			<th class="pkg-synopsis">Synopsis</th>
		</tr>
`)

	var padding int
	var lastPkg string
	for _, pkg := range pkgs {
		buf.Reset()
		listCmd := exec.Command("go", "list", "-find", "-f", `{{ .Doc }}`, pkg)
		listCmd.Dir = os.TempDir()
		listCmd.SysProcAttr = &syscall.SysProcAttr{
			Pdeathsig: syscall.SIGKILL,
		}
		listCmd.Stdout = buf

		_ = listCmd.Run() // Ignore error

		pkgLabel := pkg
		if lastPkg != "" {
			lastPkgSplit := strings.Split(lastPkg, "/")
			pkgSplit := strings.Split(pkg, "/")
			shared := 0
			for i := range pkgSplit {
				if i < len(lastPkgSplit) && strings.ToLower(lastPkgSplit[i]) == strings.ToLower(pkgSplit[i]) {
					shared++
				}
			}

			padding = shared * 20
			pkgLabel = strings.Join(pkgSplit[shared:], "/")
		}
		lastPkg = pkg

		var linkPackage bool
		for _, filterPkg := range filterPkgs {
			if pkg == filterPkg {
				linkPackage = true
				break
			}
		}
		b.WriteString(`
		<tr>
			<td class="pkg-name" style="padding-left: ` + strconv.Itoa(padding) + `px;">`)
		if !linkPackage {
			b.WriteString(pkgLabel)
		} else {
			b.WriteString(`<a href="` + pkg + index + `">` + pkgLabel + `</a>`)
		}
		b.WriteString(`</td>
			<td class="pkg-synopsis">
				` + buf.String() + `
			</td>
		</tr>
`)
	}
	b.WriteString(`
	</table>
</div>
</div>
</div>
<script type="text/javascript" src="` + basePath + `lib/godoc-static.js"></script>
</body>
</html>
`)

	err := ioutil.WriteFile(path.Join(outDir, "index.html"), b.Bytes(), 0755)
	if err != nil {
		log.Fatalf("failed to write index: %s", err)
	}
}
