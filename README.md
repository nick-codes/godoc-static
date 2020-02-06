# godoc-static
[![CI status](https://gitlab.com/tslocum/godoc-static/badges/master/pipeline.svg)](https://gitlab.com/tslocum/godoc-static/commits/master)
[![Donate](https://img.shields.io/liberapay/receives/rocketnine.space.svg?logo=liberapay)](https://liberapay.com/rocketnine.space)

Generate static Go documentation

## Demo

[Rocket Nine Labs Documentation](https://docs.rocketnine.space)

## Installation

Install `godoc-static`:

`go get gitlab.com/tslocum/godoc-static` 

Also install `godoc`:

`go get golang.org/x/tools/cmd/godoc` 

## Documentation

Execute `godoc-static` with the `-help` flag for more information.

### Usage examples

Generate documentation for `archive`, `fmt` and `net/http` targeting `https://docs.rocketnine.space`:

`godoc-static -base-path=/ -site-name="Rocket Nine Labs Documentation" -site-description="Welcome!" -out=/home/user/sites/docs archive fmt net/http`

Targeting `https://rocketnine.space/docs/`:

`godoc-static -base-path=/docs/ -site-name="Rocket Nine Labs Documentation" -site-description-file=/home/user/sitefiles/description.md -out=/home/user/sites/docs archive fmt net/http`

## Support

Please share issues/suggestions [here](https://gitlab.com/tslocum/godoc-static/issues).
