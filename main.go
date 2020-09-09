// Package godoc-static generates static Go documentation
package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	gmhtml "github.com/yuin/goldmark/renderer/html"
	"golang.org/x/net/html"
)

var (
	listenAddress       string
	siteName            string
	siteDescription     string
	siteDescriptionFile string
	siteFooter          string
	siteFooterFile      string
	siteDestination     string
	siteZip             string
	linkIndex           bool
	excludePackages     string
	verbose             bool

	godoc  *exec.Cmd
	outZip *zip.Writer
)

func main() {
	log.SetPrefix("")
	log.SetFlags(0)

	flag.StringVar(&listenAddress, "listen-address", "localhost:9001", "address for godoc to listen on while scraping pages")
	flag.StringVar(&siteName, "site-name", "Documentation", "site name")
	flag.StringVar(&siteDescription, "site-description", "", "site description (markdown-enabled)")
	flag.StringVar(&siteDescriptionFile, "site-description-file", "", "path to markdown file containing site description")
	flag.StringVar(&siteFooter, "site-footer", "", "site footer (markdown-enabled)")
	flag.StringVar(&siteFooterFile, "site-footer-file", "", "path to markdown file containing site footer")
	flag.StringVar(&siteDestination, "destination", "", "path to write site HTML")
	flag.StringVar(&siteZip, "zip", "docs.zip", "name of site ZIP file (blank to disable)")
	flag.BoolVar(&linkIndex, "link-index", false, "set link targets to index.html instead of folder")
	flag.StringVar(&excludePackages, "exclude", "", "list of packages to exclude from index")
	flag.BoolVar(&verbose, "verbose", false, "enable verbose logging")
	flag.Parse()

	err := run()
	if godoc != nil && godoc.Process != nil {
		godoc.Process.Kill()
	}
	if err != nil {
		log.Fatal(err)
	}
}

func filterPkgsWithExcludes(pkgs []string) []string {
	excludePackagesSplit := strings.Split(excludePackages, " ")
	var tmpPkgs []string
PACKAGEINDEX:
	for _, pkg := range pkgs {
		for _, excludePackage := range excludePackagesSplit {
			if strings.Contains(pkg, "\\") || strings.Contains(pkg, "testdata") || strings.Contains(pkg, "internal") || pkg == "cmd" || pkg == excludePackage || strings.HasPrefix(pkg, excludePackage+"/") {
				continue PACKAGEINDEX
			}
		}
		tmpPkgs = append(tmpPkgs, pkg)
	}
	return tmpPkgs
}

func getTmpDir() string {
	tmpDir := os.TempDir()
	if _, err := os.Stat(tmpDir); os.IsNotExist(err) {
		mkDirErr := os.MkdirAll(tmpDir, 0755)
		if _, err = os.Stat(tmpDir); os.IsNotExist(err) {
			log.Fatalf("failed to create missing temporary directory %s: %s", tmpDir, mkDirErr)
		}
	}
	return tmpDir
}

func writeFile(buf *bytes.Buffer, fileDir string, fileName string) error {
	if outZip != nil {
		fn := fileDir
		if fn != "" {
			fn += "/"
		}
		fn += fileName

		outZipFile, err := outZip.Create(fn)
		if err != nil {
			return fmt.Errorf("failed to create zip file %s: %s", fn, err)
		}

		_, err = outZipFile.Write(buf.Bytes())
		if err != nil {
			return fmt.Errorf("failed to write zip file %s: %s", fn, err)
		}
	}

	return ioutil.WriteFile(path.Join(siteDestination, fileDir, fileName), buf.Bytes(), 0755)
}

func run() error {
	var buf bytes.Buffer
	timeStarted := time.Now()

	if siteDestination == "" {
		return errors.New("--destination must be set")
	}

	if siteDescriptionFile != "" {
		siteDescriptionBytes, err := ioutil.ReadFile(siteDescriptionFile)
		if err != nil {
			return fmt.Errorf("failed to read site description file %s: %s", siteDescriptionFile, err)
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
			return fmt.Errorf("failed to render site description markdown: %s", err)
		}
		siteDescription = buf.String()
	}

	if siteFooterFile != "" {
		siteFooterBytes, err := ioutil.ReadFile(siteFooterFile)
		if err != nil {
			return fmt.Errorf("failed to read site footer file %s: %s", siteFooterFile, err)
		}
		siteFooter = string(siteFooterBytes)
	}

	if siteFooter != "" {
		markdown := goldmark.New(
			goldmark.WithRendererOptions(
				gmhtml.WithUnsafe(),
			),
			goldmark.WithExtensions(
				extension.NewLinkify(),
			),
		)

		buf.Reset()
		err := markdown.Convert([]byte(siteFooter), &buf)
		if err != nil {
			return fmt.Errorf("failed to render site footer markdown: %s", err)
		}
		siteFooter = buf.String()
	}

	if siteZip != "" {
		outZipFile, err := os.Create(filepath.Join(siteDestination, siteZip))
		if err != nil {
			return fmt.Errorf("failed to create zip file %s: %s", filepath.Join(siteDestination, siteZip), err)
		}
		defer outZipFile.Close()

		outZip = zip.NewWriter(outZipFile)
		defer outZip.Close()
	}

	if verbose {
		log.Println("Starting godoc...")
	}

	godoc = exec.Command("godoc", fmt.Sprintf("-http=%s", listenAddress))
	godoc.Stdin = os.Stdin
	godoc.Stdout = os.Stdout
	godoc.Stderr = os.Stderr
	setDeathSignal(godoc)

	err := godoc.Start()
	if err != nil {
		return fmt.Errorf("failed to execute godoc: %s\ninstall godoc by running: go get golang.org/x/tools/cmd/godoc\nthen ensure ~/go/bin is in $PATH", err)
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		<-c
		godoc.Process.Kill()
		os.Exit(1)
	}()

	godocStarted := time.Now()

	pkgs := flag.Args()

	if len(pkgs) == 0 || (len(pkgs) == 1 && pkgs[0] == "") {
		buf.Reset()

		cmd := exec.Command("go", "list", "...")
		cmd.Dir = os.TempDir()
		cmd.Stdout = &buf
		setDeathSignal(cmd)

		err = cmd.Run()
		if err != nil {
			return fmt.Errorf("failed to list system packages: %s", err)
		}

		pkgs = strings.Split(strings.TrimSpace(buf.String()), "\n")
	}

	var newPkgs []string
	for _, pkg := range pkgs {
		if strings.TrimSpace(pkg) == "" {
			continue
		}

		buf.Reset()

		newPkgs = append(newPkgs, pkg)

		cmd := exec.Command("go", "list", "-find", "-f", `{{ .Dir }}`, pkg)
		cmd.Dir = os.TempDir()
		cmd.Stdout = &buf
		setDeathSignal(cmd)

		err = cmd.Run()
		if err != nil {
			return fmt.Errorf("failed to list source directory of package %s: %s", pkg, err)
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
				return fmt.Errorf("failed to walk source directory of package %s: %s", pkg, err)
			}
		}
		buf.Reset()
	}
	pkgs = uniqueStrings(newPkgs)

	if len(pkgs) == 0 {
		return errors.New("failed to generate docs: provide the name of at least one package to generate documentation for")
	}

	filterPkgs := pkgs

	for _, pkg := range pkgs {
		subPkgs := strings.Split(pkg, "/")
		for i := range subPkgs {
			pkgs = append(pkgs, strings.Join(subPkgs[0:i+1], "/"))
		}
	}
	pkgs = filterPkgsWithExcludes(uniqueStrings(pkgs))

	sort.Slice(pkgs, func(i, j int) bool {
		return strings.ToLower(pkgs[i]) < strings.ToLower(pkgs[j])
	})

	// Allow some time for godoc to initialize

	if time.Since(godocStarted) < 3*time.Second {
		time.Sleep((3 * time.Second) - time.Since(godocStarted))
	}

	done := make(chan error)
	timeout := time.After(15 * time.Second)

	go func() {
		var (
			res *http.Response
			err error
		)
		for _, pkg := range pkgs {
			if verbose {
				log.Printf("Copying %s docs...", pkg)
			}

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
				done <- fmt.Errorf("failed to get page of %s: %s", pkg, err)
				return
			}

			doc.Find("title").First().SetHtml(fmt.Sprintf("%s - %s", path.Base(pkg), siteName))

			updatePage(doc, relativeBasePath(pkg), siteName)

			localPkgPath := path.Join(siteDestination, pkg)

			err = os.MkdirAll(localPkgPath, 0755)
			if err != nil {
				done <- fmt.Errorf("failed to make directory %s: %s", localPkgPath, err)
				return
			}

			buf.Reset()
			err = html.Render(&buf, doc.Nodes[0])
			if err != nil {
				done <- fmt.Errorf("failed to render HTML: %s", err)
				return
			}
			err = writeFile(&buf, pkg, "index.html")
			if err != nil {
				done <- fmt.Errorf("failed to write docs for %s: %s", pkg, err)
				return
			}
		}
		done <- nil
	}()

	select {
	case <-timeout:
		return errors.New("godoc failed to start in time")
	case err = <-done:
		if err != nil {
			return fmt.Errorf("failed to copy docs: %s", err)
		}
	}

	// Write source files

	err = os.MkdirAll(path.Join(siteDestination, "src"), 0755)
	if err != nil {
		return fmt.Errorf("failed to make directory lib: %s", err)
	}

	filterPkgs = filterPkgsWithExcludes(filterPkgs)

	for _, pkg := range filterPkgs {
		if verbose {
			log.Printf("Copying %s sources...", pkg)
		}

		buf.Reset()

		cmd := exec.Command("go", "list", "-find", "-f",
			`{{ join .GoFiles "\n" }}`+"\n"+
				`{{ join .CgoFiles "\n" }}`+"\n"+
				`{{ join .CFiles "\n" }}`+"\n"+
				`{{ join .CXXFiles "\n" }}`+"\n"+
				`{{ join .MFiles "\n" }}`+"\n"+
				`{{ join .HFiles "\n" }}`+"\n"+
				`{{ join .FFiles "\n" }}`+"\n"+
				`{{ join .SFiles "\n" }}`+"\n"+
				`{{ join .SwigFiles "\n" }}`+"\n"+
				`{{ join .SwigCXXFiles "\n" }}`+"\n"+
				`{{ join .TestGoFiles "\n" }}`+"\n"+
				`{{ join .XTestGoFiles "\n" }}`,
			pkg)
		cmd.Dir = getTmpDir()
		cmd.Stdout = &buf
		setDeathSignal(cmd)

		err = cmd.Run()
		if err != nil {
			//return fmt.Errorf("failed to list source files of package %s: %s", pkg, err)
			continue // This is expected for packages without source files
		}

		sourceFiles := append(strings.Split(buf.String(), "\n"), "index.html")
		for _, sourceFile := range sourceFiles {
			sourceFile = strings.TrimSpace(sourceFile)
			if sourceFile == "" {
				continue
			}

			// Rely on timeout to break loop
			res, err := http.Get(fmt.Sprintf("http://%s/src/%s/%s", listenAddress, pkg, sourceFile))
			if err != nil {
				return fmt.Errorf("failed to get source file page %s for package %s: %s", sourceFile, pkg, err)
			}

			// Load the HTML document
			doc, err := goquery.NewDocumentFromReader(res.Body)
			if err != nil {
				return fmt.Errorf("failed to load document from page for package %s: %s", pkg, err)
			}

			doc.Find("title").First().SetHtml(fmt.Sprintf("%s - %s", path.Base(pkg), siteName))

			updatePage(doc, relativeBasePath("src/"+pkg), siteName)

			doc.Find(".layout").First().Find("a").Each(func(_ int, selection *goquery.Selection) {
				href := selection.AttrOr("href", "")
				if !strings.HasSuffix(href, ".") && !strings.HasSuffix(href, "/") && !strings.HasSuffix(href, ".html") {
					selection.SetAttr("href", href+".html")
				}
			})

			pkgSrcPath := path.Join(siteDestination, "src", pkg)

			err = os.MkdirAll(pkgSrcPath, 0755)
			if err != nil {
				return fmt.Errorf("failed to make directory %s: %s", pkgSrcPath, err)
			}

			buf.Reset()
			err = html.Render(&buf, doc.Nodes[0])
			if err != nil {
				return fmt.Errorf("failed to render HTML: %s", err)
			}

			outFileName := sourceFile
			if !strings.HasSuffix(outFileName, ".html") {
				outFileName += ".html"
			}
			err = writeFile(&buf, "src/"+pkg, outFileName)
			if err != nil {
				return fmt.Errorf("failed to write docs for %s: %s", pkg, err)
			}
		}
	}

	// Write style.css

	if verbose {
		log.Println("Copying style.css...")
	}

	err = os.MkdirAll(path.Join(siteDestination, "lib"), 0755)
	if err != nil {
		return fmt.Errorf("failed to make directory lib: %s", err)
	}

	res, err := http.Get(fmt.Sprintf("http://%s/lib/godoc/style.css", listenAddress))
	if err != nil {
		return fmt.Errorf("failed to get syle.css: %s", err)
	}

	buf.Reset()

	_, err = buf.ReadFrom(res.Body)
	res.Body.Close()
	if err != nil {
		return fmt.Errorf("failed to get style.css: %s", err)
	}

	buf.WriteString("\n" + additionalCSS)

	err = writeFile(&buf, "lib", "style.css")
	if err != nil {
		return fmt.Errorf("failed to write style.css: %s", err)
	}

	// Write index

	if verbose {
		log.Println("Writing index...")
	}

	err = writeIndex(&buf, pkgs, filterPkgs)
	if err != nil {
		return fmt.Errorf("failed to write index: %s", err)
	}

	if verbose {
		log.Printf("Generated documentation in %s.", time.Since(timeStarted).Round(time.Second))
	}
	return nil
}

func relativeBasePath(p string) string {
	var r string
	if p != "" {
		r += "../"
	}
	p = filepath.ToSlash(p)
	for i := strings.Count(p, "/"); i > 0; i-- {
		r += "../"
	}
	return r
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
