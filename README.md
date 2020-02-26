# godoc-static
[![CI status](https://gitlab.com/tslocum/godoc-static/badges/master/pipeline.svg)](https://gitlab.com/tslocum/godoc-static/commits/master)
[![Donate](https://img.shields.io/liberapay/receives/rocketnine.space.svg?logo=liberapay)](https://liberapay.com/rocketnine.space)

Generate static Go documentation

## Demo

[Rocket Nine Labs Documentation](https://docs.rocketnine.space)

## Installation

Install `godoc-static`:

```bash
go get gitlab.com/tslocum/godoc-static
```

Also install `godoc`:

```bash
go get golang.org/x/tools/cmd/godoc
``` 

## Documentation

To generate documentation for specific packages, execute `godoc-static`
supplying at least one package name:

```bash
godoc-static -destination=/home/user/sites/docs fmt net/http
```

When no packages are supplied, documentation is generated for packages listed
by `go list ...` instead.

Packages are not downloaded/updated automatically.

### Usage examples

Generate documentation for `archive`, `net/http` and `gitlab.com/tslocum/cview` targeting `https://docs.rocketnine.space`:

```bash
godoc-static \
    -base-path=/ \
    -site-name="Rocket Nine Labs Documentation" \
    -site-description="Welcome!" \
    -destination=/home/user/sites/docs \
    archive net/http gitlab.com/tslocum/cview
```

Targeting `https://rocketnine.space/docs/`:

```bash
godoc-static \
    -base-path=/docs/ \
    -site-name="Rocket Nine Labs Documentation" \
    -site-description-file=/home/user/sitefiles/description.md \
    -destination=/home/user/sites/docs \
    archive net/http gitlab.com/tslocum/cview
```

### Options

#### -base-path
Site relative URL path with trailing slash.

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

## Support

Please share issues/suggestions [here](https://gitlab.com/tslocum/godoc-static/issues).
