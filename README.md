# godoc-static
[![CI status](https://code.rocketnine.space/tslocum/godoc-static/raw/branch/master/badge.svg)](https://code.rocketnine.space/tslocum/godoc-static/commits/master)
[![Donate](https://img.shields.io/liberapay/receives/rocketnine.space.svg?logo=liberapay)](https://liberapay.com/rocketnine.space)

Generate static Go documentation

## Demo

[Rocket Nine Labs Documentation](https://docs.rocketnine.space)

## Installation

Install `godoc-static`:

```bash
go get code.rocketnine.space/tslocum/godoc-static
```

Also install `godoc`:

```bash
go get golang.org/x/tools/cmd/godoc
``` 

## Documentation

To generate documentation for specific packages, execute `godoc-static`
supplying at least one package import path and/or absolute path:

```bash
godoc-static -destination=/home/user/sites/docs fmt net/http ~/awesomeproject
```

When an import path is supplied, the package is sourced from `$GOPATH` or `$GOROOT`.

When no packages are supplied, documentation is generated for packages listed
by `go list ...`.

Packages are not downloaded/updated automatically.

### Usage examples

Generate documentation for `archive`, `net/http` and `~/go/src/code.rocketnine.space/tslocum/cview`:

```bash
godoc-static \
    -site-name="Rocket Nine Labs Documentation" \
    -site-description-file=/home/user/sitefiles/description.md \
    -destination=/home/user/sites/docs \
    archive net/http code.rocketnine.space/tslocum/cview
```

### Options

#### -destination
Path to write site to.

#### -exclude
Space-separated list of packages to exclude from the index.

#### -link-index
Link to index.html instead of folder.

#### -listen-address
Address for godoc to listen on while scraping pages.

#### -site-description
Site description (markdown-enabled).

#### -site-description-file
Path to markdown file containing site description.

#### -site-footer
Site footer (markdown-enabled).

#### -site-footer-file
Path to markdown file containing site footer.

#### -site-name
Site name.

#### -verbose
Enable verbose logging.

#### -zip
Site ZIP file name.

## Support

Please share issues and suggestions [here](https://code.rocketnine.space/tslocum/godoc-static/issues).
