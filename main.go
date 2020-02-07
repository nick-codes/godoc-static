package main

import (
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
	basePath            string
	siteName            string
	siteDescription     string
	siteDescriptionFile string
	linkIndex           bool
	outDir              string
	excludePackages     string
	verbose             bool

	godoc *exec.Cmd
)

func main() {
	log.SetPrefix("")
	log.SetFlags(0)

	flag.StringVar(&listenAddress, "listen-address", "localhost:9001", "address for godoc to listen on while scraping pages")
	flag.StringVar(&basePath, "base-path", "/", "site relative URL path with trailing slash")
	flag.StringVar(&siteName, "site-name", "Documentation", "site name")
	flag.StringVar(&siteDescription, "site-description", "", "site description (markdown-enabled)")
	flag.StringVar(&siteDescriptionFile, "site-description-file", "", "path to markdown file containing site description")
	flag.BoolVar(&linkIndex, "link-index", false, "set link targets to index.html instead of folder")
	flag.StringVar(&outDir, "out", "", "site directory")
	flag.StringVar(&excludePackages, "exclude", "", "list of packages to exclude from index")
	flag.BoolVar(&verbose, "verbose", false, "enable verbose logging")
	flag.Parse()

	err := run()
	if godoc != nil {
		godoc.Process.Kill()
	}
	if err != nil {
		log.Fatal(err)
	}
}

func run() error {
	var buf bytes.Buffer
	timeStarted := time.Now()

	if outDir == "" {
		return errors.New("--out must be set")
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
		return fmt.Errorf("failed to execute godoc: %s", err)
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
	pkgs = uniqueStrings(pkgs)

	sort.Slice(pkgs, func(i, j int) bool {
		return strings.ToLower(pkgs[i]) < strings.ToLower(pkgs[j])
	})

	// Allow godoc to initialize

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

			updatePage(doc, basePath, siteName)

			localPkgPath := path.Join(outDir, pkg)

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
			err = ioutil.WriteFile(path.Join(localPkgPath, "index.html"), buf.Bytes(), 0755)
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

	err = os.MkdirAll(path.Join(outDir, "src"), 0755)
	if err != nil {
		return fmt.Errorf("failed to make directory lib: %s", err)
	}

	for _, pkg := range filterPkgs {
		if verbose {
			log.Printf("Copying %s sources...", pkg)
		}

		tmpDir := os.TempDir()
		// TODO Handle temp directory not existing
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
		cmd.Dir = tmpDir
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

			updatePage(doc, basePath, siteName)

			doc.Find(".layout").First().Find("a").Each(func(_ int, selection *goquery.Selection) {
				href := selection.AttrOr("href", "")
				if !strings.HasSuffix(href, ".") && !strings.HasSuffix(href, "/") && !strings.HasSuffix(href, ".html") {
					selection.SetAttr("href", href+".html")
				}
			})

			pkgSrcPath := path.Join(outDir, "src", pkg)

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
			err = ioutil.WriteFile(path.Join(pkgSrcPath, outFileName), buf.Bytes(), 0755)
			if err != nil {
				return fmt.Errorf("failed to write docs for %s: %s", pkg, err)
			}
		}
	}

	// Write style.css

	if verbose {
		log.Println("Copying style.css...")
	}

	err = os.MkdirAll(path.Join(outDir, "lib"), 0755)
	if err != nil {
		return fmt.Errorf("failed to make directory lib: %s", err)
	}

	res, err := http.Get(fmt.Sprintf("http://%s/lib/godoc/style.css", listenAddress))
	if err != nil {
		return fmt.Errorf("failed to get syle.css: %s", err)
	}

	content, err := ioutil.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		return fmt.Errorf("failed to get style.css: %s", err)
	}

	content = append(content, []byte("\n"+additionalCSS+"\n")...)

	err = ioutil.WriteFile(path.Join(outDir, "lib", "style.css"), content, 0755)
	if err != nil {
		return fmt.Errorf("failed to write index: %s", err)
	}

	// Write index

	if verbose {
		log.Println("Writing index...")
	}

	err = writeIndex(&buf, outDir, basePath, siteName, pkgs, filterPkgs)
	if err != nil {
		return fmt.Errorf("failed to write index: %s", err)
	}

	if verbose {
		log.Printf("Generated documentation in %s.", time.Since(timeStarted).Round(time.Second))
	}
	return nil
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
