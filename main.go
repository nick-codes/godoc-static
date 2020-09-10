// Package godoc-static generates static Go documentation
package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"go/build"
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
	"golang.org/x/mod/modfile"
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

	goPath string

	godoc         *exec.Cmd
	godocEnv      []string
	godocStartDir string
	outZip        *zip.Writer

	scanIncomplete = []byte(`<span class="alert" style="font-size:120%">Scan is not yet complete.`)
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

func startGodoc(dir string) {
	if dir == godocStartDir {
		return // Already started
	}
	godocStartDir = dir

	if godoc != nil {
		godoc.Process.Kill()
		godoc.Wait()
	}

	godoc = exec.Command("godoc", fmt.Sprintf("-http=%s", listenAddress))
	godoc.Env = godocEnv
	if dir == "" {
		godoc.Dir = os.TempDir()
	} else {
		godoc.Dir = dir
	}
	godoc.Stdin = nil
	godoc.Stdout = nil
	godoc.Stderr = nil
	setDeathSignal(godoc)

	err := godoc.Start()
	if err != nil {
		log.Fatalf("failed to execute godoc: %s\ninstall godoc by running: go get golang.org/x/tools/cmd/godoc\nthen ensure ~/go/bin is in $PATH", err)
	}
}

func run() error {
	var (
		timeStarted = time.Now()

		buf bytes.Buffer
		err error
	)

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

	goPath = os.Getenv("GOPATH")
	if goPath == "" {
		goPath = build.Default.GOPATH
	}

	godocEnv = make([]string, len(os.Environ()))
	copy(godocEnv, os.Environ())
	for i, e := range godocEnv {
		if strings.HasPrefix(e, "GO111MODULE=") {
			godocEnv[i] = ""
		}
	}
	godocEnv = append(godocEnv, "GO111MODULE=auto")

	godocStartDir = "-" // Trigger initial start
	startGodoc("")

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		<-c
		godoc.Process.Kill()
		os.Exit(1)
	}()

	pkgs := flag.Args()

	if len(pkgs) == 0 || (len(pkgs) == 1 && pkgs[0] == "") {
		buf.Reset()

		cmd := exec.Command("go", "list", "...")
		cmd.Env = godocEnv
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
	pkgPaths := make(map[string]string)
	for _, pkg := range pkgs {
		if strings.TrimSpace(pkg) == "" {
			continue
		}

		var suppliedPath bool

		dir := ""
		if _, err := os.Stat(pkg); !os.IsNotExist(err) {
			dir = pkg

			modFileData, err := ioutil.ReadFile(path.Join(dir, "go.mod"))
			if err != nil {
				log.Fatalf("failed to read mod file for %s: %s", pkg, err)
			}

			modFile, err := modfile.Parse(path.Join(dir, "go.mod"), modFileData, nil)
			if err != nil {
				log.Fatalf("failed to parse mod file for %s: %s", pkg, err)
			}

			pkg = modFile.Module.Mod.Path

			suppliedPath = true
		} else {
			srcDir := path.Join(goPath, "src", pkg)
			if _, err := os.Stat(srcDir); !os.IsNotExist(err) {
				dir = srcDir
			}
		}

		newPkgs = append(newPkgs, pkg)

		buf.Reset()

		search := "./..."
		if dir == "" {
			search = pkg
		}

		cmd := exec.Command("go", "list", "-find", "-f", `{{ .ImportPath }} {{ .Dir }}`, search)
		cmd.Env = godocEnv
		if dir == "" {
			cmd.Dir = os.TempDir()
		} else {
			cmd.Dir = dir
		}
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		setDeathSignal(cmd)

		err = cmd.Run()
		if err != nil {
			pkgPaths[pkg] = dir
			continue
		}

		sourceListing := strings.Split(buf.String(), "\n")
		for i := range sourceListing {
			firstSpace := strings.Index(sourceListing[i], " ")
			if firstSpace <= 0 {
				continue
			}

			pkg = sourceListing[i][:firstSpace]
			pkgPath := sourceListing[i][firstSpace+1:]

			newPkgs = append(newPkgs, pkg)

			if dir == "" || strings.HasPrefix(filepath.Base(pkgPath), ".") {
				continue
			}

			if suppliedPath {
				pkgPaths[pkg] = dir
			} else {
				pkgPaths[pkg] = pkgPath
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

	done := make(chan error)
	go func() {
		var (
			res *http.Response
			doc *goquery.Document
			err error
		)
		for _, pkg := range filterPkgs {
			if verbose {
				log.Printf("Copying %s documentation...", pkg)
			}

			startGodoc(pkgPaths[pkg])

			// Rely on timeout to break loop
			for {
				res, err = http.Get(fmt.Sprintf("http://%s/pkg/%s/", listenAddress, pkg))
				if err == nil {
					body, err := ioutil.ReadAll(res.Body)
					if err != nil {
						done <- fmt.Errorf("failed to get page of %s: %s", pkg, err)
						return
					}

					if bytes.Contains(body, scanIncomplete) {
						time.Sleep(25 * time.Millisecond)
						continue
					}

					// Load the HTML document
					doc, err = goquery.NewDocumentFromReader(bytes.NewReader(body))
					if err != nil {
						done <- fmt.Errorf("failed to parse page of %s: %s", pkg, err)
						return
					}

					break
				}
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

	for _, pkg := range filterPkgs {
		if verbose {
			log.Printf("Copying %s sources...", pkg)
		}

		buf.Reset()

		dir := pkgPaths[pkg]
		if dir == "" {
			dir = getTmpDir()
		}

		startGodoc(pkgPaths[pkg])

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
		cmd.Env = godocEnv
		cmd.Dir = dir
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
			var doc *goquery.Document
			for {
				res, err := http.Get(fmt.Sprintf("http://%s/src/%s/%s", listenAddress, pkg, sourceFile))
				if err == nil {
					body, err := ioutil.ReadAll(res.Body)
					if err != nil {
						return fmt.Errorf("failed to get source file page %s of %s: %s", sourceFile, pkg, err)
					}

					if bytes.Contains(body, scanIncomplete) {
						time.Sleep(25 * time.Millisecond)
						continue
					}

					// Load the HTML document
					doc, err = goquery.NewDocumentFromReader(bytes.NewReader(body))
					if err != nil {
						return fmt.Errorf("failed to load document from page for package %s: %s", pkg, err)
					}

					break
				}
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

	for {
		res, err := http.Get(fmt.Sprintf("http://%s/lib/godoc/style.css", listenAddress))
		if err == nil {
			buf.Reset()

			_, err = buf.ReadFrom(res.Body)
			res.Body.Close()
			if err != nil {
				return fmt.Errorf("failed to get style.css: %s", err)
			}
			break
		}
	}

	buf.WriteString("\n" + additionalCSS)

	err = writeFile(&buf, "lib", "style.css")
	if err != nil {
		return fmt.Errorf("failed to write style.css: %s", err)
	}

	// Write index

	if verbose {
		log.Println("Writing index.html...")
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
