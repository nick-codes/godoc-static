package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const additionalCSS = `
details { margin-top: 20px; }
summary { margin-left: 20px; cursor: pointer; }
`

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

	importPathDisplay := doc.Find("#short-nav").First().Find("code").First()
	if importPathDisplay.Length() > 0 {
		importPathDisplayText := importPathDisplay.Text()
		if strings.ContainsRune(importPathDisplayText, '.') && strings.HasPrefix(importPathDisplayText, `import "`) && strings.HasSuffix(importPathDisplayText, `"`) {
			importPath := importPathDisplayText[8 : len(importPathDisplayText)-1]

			browseImportPath := importPath
			var browseInsert string
			if strings.HasPrefix(importPath, "gitlab.com/") {
				browseInsert = "/-/tree/master"
			} else if strings.HasPrefix(importPath, "github.com/") || strings.HasPrefix(importPath, "git.sr.ht/") {
				browseInsert = "/tree/master"
			} else if strings.HasPrefix(importPath, "bitbucket.org/") {
				browseInsert = "/src/master"
			}
			if browseInsert != "" {
				var insertPos int
				var found int
				for i, c := range importPath {
					if c == '/' {
						found++
						if found == 3 {
							insertPos = i
							break
						}
					}
				}
				if insertPos > 0 {
					browseImportPath = importPath[0:insertPos] + browseInsert + importPath[insertPos:]
				}
			}

			importPathDisplay.SetHtml(fmt.Sprintf(`import "<a href="https://` + browseImportPath + `" target="_blank">` + importPath + `</a>"`))
		}
	}

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

	doc.Find("#footer").Last().Remove()
}

func writeIndex(buf *bytes.Buffer, outDir string, basePath string, siteName string, pkgs []string, filterPkgs []string) error {
	var index string
	if linkIndex {
		index = "/index.html"
	}

	buf.Reset()
	buf.WriteString(`<!DOCTYPE html>
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
		buf.WriteString(siteDescription)
	}

	buf.WriteString(`
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
	var pkgBuf bytes.Buffer
	excludePackagesSplit := strings.Split(excludePackages, " ")
PACKAGEINDEX:
	for _, pkg := range pkgs {
		for _, excludePackage := range excludePackagesSplit {
			if pkg == excludePackage || strings.HasPrefix(pkg, excludePackage+"/") {
				continue PACKAGEINDEX
			}
		}

		pkgBuf.Reset()
		cmd := exec.Command("go", "list", "-find", "-f", `{{ .Doc }}`, pkg)
		cmd.Dir = os.TempDir()
		cmd.Stdout = &pkgBuf
		setDeathSignal(cmd)

		cmd.Run() // Ignore error

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
		buf.WriteString(`
		<tr>
			<td class="pkg-name" style="padding-left: ` + strconv.Itoa(padding) + `px;">`)
		if !linkPackage {
			buf.WriteString(pkgLabel)
		} else {
			buf.WriteString(`<a href="` + pkg + index + `">` + pkgLabel + `</a>`)
		}
		buf.WriteString(`</td>
			<td class="pkg-synopsis">
				` + pkgBuf.String() + `
			</td>
		</tr>
`)
	}
	buf.WriteString(`
	</table>
</div>
</div>
</div>
</body>
</html>
`)

	return ioutil.WriteFile(path.Join(outDir, "index.html"), buf.Bytes(), 0755)
}
