package main

import (
	"bytes"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	gmhtml "github.com/yuin/goldmark/renderer/html"
	"golang.org/x/net/html"
)

const additionalCSS = `
details { margin-top: 20px; }
summary { margin-left: 20px; cursor: pointer; }
`

var (
	listenAddress       string
	basePath            string
	siteName            string
	siteDescription     string
	siteDescriptionFile string
	linkIndex           bool
	outDir              string
	verbose             bool
)

func main() {
	flag.StringVar(&listenAddress, "listen-address", "localhost:9001", "address for godoc to listen on while scraping pages")
	flag.StringVar(&basePath, "base-path", "/", "site relative URL path with trailing slash")
	flag.StringVar(&siteName, "site-name", "Documentation", "site name")
	flag.StringVar(&siteDescription, "site-description", "", "site description (markdown-enabled)")
	flag.StringVar(&siteDescriptionFile, "site-description-file", "", "path to markdown file containing site description")
	flag.BoolVar(&linkIndex, "link-index", false, "set link targets to index.html instead of folder")
	flag.StringVar(&outDir, "out", "", "site directory")
	flag.BoolVar(&verbose, "verbose", false, "enable verbose logging")
	flag.Parse()

	var buf bytes.Buffer
	timeStarted := time.Now()

	if outDir == "" {
		log.Fatal("--out must be set")
	}

	if siteDescriptionFile != "" {
		siteDescriptionBytes, err := ioutil.ReadFile(siteDescriptionFile)
		if err != nil {
			log.Fatalf("failed to read site description file %s: %s", siteDescriptionFile, err)
		}
		siteDescription = string(siteDescriptionBytes)
	}

	if siteDescription != "" {
		markdown := goldmark.New(
			goldmark.WithRendererOptions(
				gmhtml.WithUnsafe(),
			),
			goldmark.WithExtensions(
				extension.NewLinkify(),
			),
		)

		buf.Reset()
		err := markdown.Convert([]byte(siteDescription), &buf)
		if err != nil {
			log.Fatalf("failed to render site description markdown: %s", err)
		}
		siteDescription = buf.String()
	}

	if verbose {
		log.Println("Starting godoc...")
	}

	cmd := exec.Command("godoc", fmt.Sprintf("-http=%s", listenAddress))
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGKILL,
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err := cmd.Start()
	if err != nil {
		log.Fatalf("failed to execute godoc: %s", err)
	}

	// Allow godoc to initialize

	time.Sleep(3 * time.Second)

	done := make(chan struct{})
	timeout := time.After(15 * time.Second)

	pkgs := flag.Args()

	var newPkgs []string

	for _, pkg := range pkgs {
		if strings.TrimSpace(pkg) == "" {
			continue
		}

		buf.Reset()

		newPkgs = append(newPkgs, pkg)

		listCmd := exec.Command("go", "list", "-find", "-f", `{{ .Dir }}`, pkg)
		listCmd.Dir = os.TempDir()
		listCmd.SysProcAttr = &syscall.SysProcAttr{
			Pdeathsig: syscall.SIGKILL,
		}
		listCmd.Stdout = &buf

		err = listCmd.Run()
		if err != nil {
			log.Fatalf("failed to list source directory of package %s: %s", pkg, err)
		}

		pkgPath := strings.TrimSpace(buf.String())
		if pkgPath != "" {
			err := filepath.Walk(pkgPath, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				} else if !info.IsDir() {
					return nil
				} else if strings.HasPrefix(filepath.Base(path), ".") {
					return filepath.SkipDir
				}

				if len(path) > len(pkgPath) && strings.HasPrefix(path, pkgPath) {
					newPkgs = append(newPkgs, pkg+path[len(pkgPath):])
				}
				return nil
			})
			if err != nil {
				log.Fatalf("failed to walk source directory of package %s: %s", pkg, err)
			}
		}
		buf.Reset()
	}
	pkgs = uniqueStrings(newPkgs)

	if len(pkgs) == 0 {
		log.Fatal("failed to generate docs: provide the name of at least one package to generate documentation for")
	}

	filterPkgs := pkgs

	for _, pkg := range pkgs {
		subPkgs := strings.Split(pkg, "/")
		for i := range subPkgs {
			pkgs = append(pkgs, strings.Join(subPkgs[0:i+1], "/"))
		}
	}
	pkgs = uniqueStrings(pkgs)

	sort.Slice(pkgs, func(i, j int) bool {
		return strings.ToLower(pkgs[i]) < strings.ToLower(pkgs[j])
	})

	if verbose {
		log.Println("Copying docs...")
	}

	go func() {
		var (
			res *http.Response
			err error
		)
		for _, pkg := range pkgs {
			// Rely on timeout to break loop
			for {
				res, err = http.Get(fmt.Sprintf("http://%s/pkg/%s/", listenAddress, pkg))
				if err == nil {
					break
				}
			}

			// Load the HTML document
			doc, err := goquery.NewDocumentFromReader(res.Body)
			if err != nil {
				log.Fatalf("failed to get page of %s: %s", pkg, err)
			}

			doc.Find("title").First().SetHtml(fmt.Sprintf("%s - %s", path.Base(pkg), siteName))

			updatePage(doc, basePath, siteName)

			localPkgPath := path.Join(outDir, pkg)

			err = os.MkdirAll(localPkgPath, 0755)
			if err != nil {
				log.Fatalf("failed to make directory %s: %s", localPkgPath, err)
			}

			buf.Reset()
			err = html.Render(&buf, doc.Nodes[0])
			if err != nil {
				return
			}
			err = ioutil.WriteFile(path.Join(localPkgPath, "index.html"), buf.Bytes(), 0755)
			if err != nil {
				log.Fatalf("failed to write docs for %s: %s", pkg, err)
			}
		}
		done <- struct{}{}
	}()

	select {
	case <-timeout:
		log.Fatal("godoc failed to start in time")
	case <-done:
	}

	// Write source files

	if verbose {
		log.Println("Copying sources...")
	}

	err = os.MkdirAll(path.Join(outDir, "src"), 0755)
	if err != nil {
		log.Fatalf("failed to make directory lib: %s", err)
	}

	for _, pkg := range filterPkgs {
		tmpDir := os.TempDir()
		// TODO Handle temp directory not existing
		buf.Reset()

		listCmd := exec.Command("go", "list", "-find", "-f", `{{ join .GoFiles "\n" }}`, pkg)
		listCmd.Dir = tmpDir
		listCmd.SysProcAttr = &syscall.SysProcAttr{
			Pdeathsig: syscall.SIGKILL,
		}
		listCmd.Stdout = &buf

		err = listCmd.Run()
		if err != nil {
			//log.Fatalf("failed to list source files of package %s: %s", pkg, err)
			continue // This is expected for packages without source files
		}

		sourceFiles := strings.Split(buf.String(), "\n")
		for _, sourceFile := range sourceFiles {
			sourceFile = strings.TrimSpace(sourceFile)
			if sourceFile == "" {
				continue
			}

			// Rely on timeout to break loop
			res, err := http.Get(fmt.Sprintf("http://%s/src/%s/%s", listenAddress, pkg, sourceFile))
			if err != nil {
				log.Fatalf("failed to get source file page %s for package %s: %s", sourceFile, pkg, err)
			}

			// Load the HTML document
			doc, err := goquery.NewDocumentFromReader(res.Body)
			if err != nil {
				log.Fatalf("failed to load document from page for package %s: %s", pkg, err)
			}

			doc.Find("title").First().SetHtml(fmt.Sprintf("%s - %s", path.Base(pkg), siteName))

			updatePage(doc, basePath, siteName)

			pkgSrcPath := path.Join(outDir, "src", pkg)

			err = os.MkdirAll(pkgSrcPath, 0755)
			if err != nil {
				log.Fatalf("failed to make directory %s: %s", pkgSrcPath, err)
			}

			buf.Reset()
			err = html.Render(&buf, doc.Nodes[0])
			if err != nil {
				return
			}
			err = ioutil.WriteFile(path.Join(pkgSrcPath, sourceFile+".html"), buf.Bytes(), 0755)
			if err != nil {
				log.Fatalf("failed to write docs for %s: %s", pkg, err)
			}
		}
	}

	// Write style.css

	if verbose {
		log.Println("Copying style.css...")
	}

	err = os.MkdirAll(path.Join(outDir, "lib"), 0755)
	if err != nil {
		log.Fatalf("failed to make directory lib: %s", err)
	}

	res, err := http.Get(fmt.Sprintf("http://%s/lib/godoc/style.css", listenAddress))
	if err != nil {
		log.Fatalf("failed to get syle.css: %s", err)
	}

	content, err := ioutil.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		log.Fatalf("failed to get style.css: %s", err)
	}

	content = append(content, []byte("\n"+additionalCSS+"\n")...)

	err = ioutil.WriteFile(path.Join(outDir, "lib", "style.css"), content, 0755)
	if err != nil {
		log.Fatalf("failed to write index: %s", err)
	}

	// Write index

	if verbose {
		log.Println("Writing index...")
	}

	writeIndex(&buf, outDir, basePath, siteName, pkgs, filterPkgs)

	if verbose {
		log.Printf("Generated documentation in %s", time.Since(timeStarted).Round(time.Second))
	}
}

func uniqueStrings(strSlice []string) []string {
	keys := make(map[string]bool)
	var unique []string
	for _, entry := range strSlice {
		if _, value := keys[entry]; !value {
			keys[entry] = true
			unique = append(unique, entry)
		}
	}
	return unique
}
