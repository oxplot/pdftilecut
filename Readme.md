![Build Static Binaries](https://github.com/oxplot/pdftilecut/workflows/Build%20Static%20Binaries/badge.svg)

**pdftilecut** lets you sub-divide a PDF page(s) into smaller pages so you
can print them on small form printers. This operation is sometimes called
*posterizing* (related to printing large posters on home printers) and
*[tile cropping](http://www.imagemagick.org/Usage/crop/#crop_tile)*.

# Installation

Latest statically linked binaries [are available on releases
page](https://github.com/oxplot/pdftilecut/releases).

Alternatively, you can install it:

* on macOS with [homebrew](https://formulae.brew.sh/formula/pdftilecut):
  `brew install pdftilecut`
* on ArchLinux from
  [AUR](https://aur.archlinux.org/packages/pdftilecut/): `yay -S
  pdftilecut`

# Features

* Statically compiled Go binaries with no OS dependencies.
* Multi page PDF support. Each page can be of arbitrary size.
* Each output page has appropriate PDF bleed and trim boxes set (so you
  can give it to a printing house and get back correctly trimmed pages).
* Print marks to assist with accurate trimming and arranging.

# Usage

We want to print `mars.pdf` from the [beautiful NASA
posters](https://www.jpl.nasa.gov/visions-of-the-future/)
collection, which comes in 20x30 inch size, on A4 papers:

```sh
$ pdftilecut -tile-size A4 -in mars.pdf -out mars_a4.pdf
```

Below on the left is the original poster, and two of its tiles on the
right:

![Poster before and after](/img/example.png?raw=true "Poster before and
after")

The header contains the original page number and a tile reference for
easier arranging:

![Tile heading](/img/heading.png?raw=true "Tile heading")

# Build & Development

You need `yasm`, `cmake`, `automake`, `autogen`, `git`, `go >= 1.13`,
`coreutils` and C compiler.

Build using `make` and the static binary will be output to `bin/pdftilecut`.

# Credits

The amazing [QPDF library](https://github.com/qpdf/qpdf) is used to
parse and write back modified PDF files. QPDF relies on
[libz](https://www.zlib.net) and
[libjpeg](https://github.com/libjpeg-turbo). Library sources are not
included in this repo and must be downloaded separately.
