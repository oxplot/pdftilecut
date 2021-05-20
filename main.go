package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/oxplot/papersizes"

	"github.com/oxplot/pdftilecut/qpdf"
)

const (
	ptsInInch = 72
	mmInInch  = 25.4
	mmInCm    = 10

	bleedMargin       = ptsInInch * 5 / 6 // in pt from media box
	trimMargin        = ptsInInch / 6     // in pt from bleed box
	trimMarkLineWidth = 0.5               // in pt

	// Min page size in mm
	minPageDimension = (bleedMargin + trimMargin + trimMarkLineWidth) * 2 * mmInInch / ptsInInch

	creditLine = "CUT WITH PDFTILECUT"
)

type TileSizeFlag struct {
	name string

	// in millimeters
	width  float32
	height float32

	isDim bool
}

func (v *TileSizeFlag) String() string {
	if v.isDim {
		return fmt.Sprintf("%.0fmm x %.0fmm", v.width, v.height)
	} else {
		return fmt.Sprintf("%s (%.0fmm x %.0fmm)", v.name, v.width, v.height)
	}
}

func (v *TileSizeFlag) Set(s string) error {
	// unit to mm ratios
	unitsToMillimeter := map[string]float32{
		"mm": 1,
		"cm": mmInCm,
		"in": mmInInch,
		"pt": mmInInch / ptsInInch,
	}
	// known paper sizes
	size := papersizes.FromName(s)
	if size != nil {
		v.name = size.Name
		v.width = float32(size.Width)
		v.height = float32(size.Height)
		v.isDim = false
	} else {
		// w x h dimensions
		dimRe := regexp.MustCompile(`^\s*(\d+(?:\.\d+)?)\s*(mm|cm|in|pt)\s*x\s*(\d+(?:\.\d+)?)\s*(mm|cm|in|pt)\s*$`)
		parts := dimRe.FindStringSubmatch(s)
		if parts == nil {
			return errors.New("invalid tile size")
		}
		v.name = parts[1] + parts[2] + "x" + parts[3] + parts[4]
		w, _ := strconv.ParseFloat(parts[1], 32)
		v.width = float32(w) * unitsToMillimeter[parts[2]]
		h, _ := strconv.ParseFloat(parts[3], 32)
		v.height = float32(h) * unitsToMillimeter[parts[4]]
		v.isDim = true
	}
	if v.width < minPageDimension || v.height < minPageDimension {
		return fmt.Errorf("min. tile dimension is %fmm x %fmm", minPageDimension, minPageDimension)
	}
	return nil
}

var (
	inputFile     = flag.String("in", "-", "input PDF")
	outputFile    = flag.String("out", "-", "output PDF")
	tileTitle     = flag.String("title", "", "title to show on margin of each tile (defaults to input filename)")
	debugMode     = flag.Bool("debug", false, "run in debug mode")
	longTrimMarks = flag.Bool("long-trim-marks", false, "Use full width/height trim marks")
	hideLogo      = flag.Bool("hide-logo", false, "Hide the logo")
	tileSize      TileSizeFlag
)

func init() {
	tileSize.Set("A4")
	flag.Var(&tileSize, "tile-size",
		"maximum size - can be a standard paper size (eg A5), or width x height dimension with a unit (mm, cm, in, pt) (e.g. 6cm x 12in)")
}

// getNextFreeObjectId returns the largest object id in the document + 1
func getNextFreeObjectId(d string) (int, error) {
	m := regexp.MustCompile(`(?m)^xref\s+\d+\s+(\d+)`).FindStringSubmatch(d)
	if m == nil {
		return 0, fmt.Errorf("cannot find the next free object id")
	}
	return strconv.Atoi(m[1])
}

type rect struct {
	// ll = lower left
	// ur = upper right
	llx, lly, urx, ury float32
}

func (r rect) isValid() bool {
	return r.llx <= r.urx && r.lly <= r.ury
}

type page struct {
	id     int
	number int

	tileX int
	tileY int

	mediaBox   rect
	cropBox    rect
	bleedBox   rect
	trimBox    rect
	contentIds []int

	parentId int
	raw      string
}

var (
	boxReTpl    = `(?m)^\s+/%s\s*\[\s*(-?[\d.]+)\s+(-?[\d.]+)\s+(-?[\d.]+)\s+(-?[\d.]+)\s*\]`
	bleedBoxRe  = regexp.MustCompile(fmt.Sprintf(boxReTpl, "BleedBox"))
	cropBoxRe   = regexp.MustCompile(fmt.Sprintf(boxReTpl, "CropBox"))
	mediaBoxRe  = regexp.MustCompile(fmt.Sprintf(boxReTpl, "MediaBox"))
	trimBoxRe   = regexp.MustCompile(fmt.Sprintf(boxReTpl, "TrimBox"))
	contentsRe  = regexp.MustCompile(`(?m)^\s+/Contents\s+(?:(\d+)|\[([^\]]*))`)
	pageObjRmRe = regexp.MustCompile(
		`(?m)^\s+/((Bleed|Crop|Media|Trim|Art)Box|Contents|Parent)\s+(\[[^\]]+\]|\d+\s+\d+\s+R)\n`)
)

// marshal serializes the page to string that can be inserted into
// PDF document.
func (p *page) marshal() string {
	b := &strings.Builder{}
	fmt.Fprintf(b, "\n%d 0 obj\n<<\n", p.id)
	fmt.Fprintf(b, "  /MediaBox [ %f %f %f %f ]\n", p.mediaBox.llx, p.mediaBox.lly, p.mediaBox.urx, p.mediaBox.ury)
	fmt.Fprintf(b, "  /CropBox [ %f %f %f %f ]\n", p.cropBox.llx, p.cropBox.lly, p.cropBox.urx, p.cropBox.ury)
	fmt.Fprintf(b, "  /BleedBox [ %f %f %f %f ]\n", p.bleedBox.llx, p.bleedBox.lly, p.bleedBox.urx, p.bleedBox.ury)
	fmt.Fprintf(b, "  /TrimBox [ %f %f %f %f ]\n", p.trimBox.llx, p.trimBox.lly, p.trimBox.urx, p.trimBox.ury)
	fmt.Fprintf(b, "  /Contents [ ")
	for _, cid := range p.contentIds {
		fmt.Fprintf(b, " %d 0 R ", cid)
	}
	fmt.Fprintf(b, " ]\n")
	fmt.Fprintf(b, "  /Parent %d 0 R\n", p.parentId)
	b.WriteString(p.raw)
	fmt.Fprintf(b, "\n>>\nendobj\n")
	return b.String()
}

// extractAttrs extracts interesting attributes of the page into
// struct elements and removes them from raw string of the page.
func (p *page) extractAttrs() error {
	atoi := func(s string) int {
		i, err := strconv.Atoi(s)
		if err != nil {
			panic(err)
		}
		return i
	}
	atof := func(s string) float32 {
		f, err := strconv.ParseFloat(s, 32)
		if err != nil {
			panic(err)
		}
		return float32(f)
	}

	var m []string

	m = contentsRe.FindStringSubmatch(p.raw)
	if m == nil {
		return fmt.Errorf("cannot find Contents for page:\n%s", p.raw)
	}
	if m[1] != "" {
		p.contentIds = []int{atoi(m[1])}
	} else {
		m := regexp.MustCompile(`(?m)^\s+(\d+)\s+\d+\s+R`).FindAllStringSubmatch(m[2], -1)
		p.contentIds = []int{}
		for _, r := range m {
			p.contentIds = append(p.contentIds, atoi(r[1]))
		}
	}

	m = mediaBoxRe.FindStringSubmatch(p.raw)
	if m == nil {
		return fmt.Errorf("cannot find MediaBox for page:\n%s", p.raw)
	}
	p.mediaBox = rect{atof(m[1]), atof(m[2]), atof(m[3]), atof(m[4])}
	if !p.mediaBox.isValid() {
		return fmt.Errorf("invalid MediaBox for page:\n%s", p.raw)
	}

	m = cropBoxRe.FindStringSubmatch(p.raw)
	if m == nil {
		p.cropBox = p.mediaBox
	} else {
		p.cropBox = rect{atof(m[1]), atof(m[2]), atof(m[3]), atof(m[4])}
	}
	if !p.cropBox.isValid() {
		return fmt.Errorf("invalid CropBox for page:\n%s", p.raw)
	}

	m = bleedBoxRe.FindStringSubmatch(p.raw)
	if m == nil {
		p.bleedBox = p.cropBox
	} else {
		p.bleedBox = rect{atof(m[1]), atof(m[2]), atof(m[3]), atof(m[4])}
	}
	if !p.bleedBox.isValid() {
		return fmt.Errorf("invalid BleedBox for page:\n%s", p.raw)
	}

	m = trimBoxRe.FindStringSubmatch(p.raw)
	if m == nil {
		p.trimBox = p.cropBox
	} else {
		p.trimBox = rect{atof(m[1]), atof(m[2]), atof(m[3]), atof(m[4])}
	}
	if !p.trimBox.isValid() {
		return fmt.Errorf("invalid TrimBox for page:\n%s", p.raw)
	}

	// Delete all the extracted raw content

	p.raw = pageObjRmRe.ReplaceAllString(p.raw, "")

	return nil
}

// cutPageToTiles slices the page into tiles of the given size, setting
// appropriate *Box attributes of the tiles. All other page attributes
// are copied from the original page.
func cutPageToTiles(p *page, tileW, tileH, bleedMargin, trimMargin float32) []*page {

	// Adjust tileW and tileH such that all tiles end up with the same dimensions
	pageWidth := p.trimBox.urx - p.trimBox.llx
	pageHeight := p.trimBox.ury - p.trimBox.lly
	hTiles := int(math.Ceil(float64(pageWidth / tileW)))
	vTiles := int(math.Ceil(float64(pageHeight / tileH)))
	tileW = pageWidth / float32(hTiles)
	tileH = pageHeight / float32(vTiles)

	var tilePages []*page
	tgy := 0
	for y := 0; y < vTiles; y++ {
		lly := p.trimBox.lly + float32(y)*tileH
		tgx := 0
		for x := 0; x < hTiles; x++ {
			llx := p.trimBox.llx + float32(x)*tileW

			tile := page{
				tileX: tgx,
				tileY: tgy,
				mediaBox: rect{
					llx - trimMargin - bleedMargin,
					lly - trimMargin - bleedMargin,
					llx + tileW + trimMargin + bleedMargin,
					lly + tileH + trimMargin + bleedMargin,
				},
				bleedBox: rect{llx - trimMargin, lly - trimMargin, llx + tileW + trimMargin, lly + tileH + trimMargin},
				trimBox:  rect{llx, lly, llx + tileW, lly + tileH},

				number:     p.number,
				contentIds: append([]int{}, p.contentIds...),
				raw:        p.raw,
			}
			tile.cropBox = tile.mediaBox
			tilePages = append(tilePages, &tile)

			tgx += 1
		}
		tgy += 1
	}

	return tilePages
}

// appendPagesToDoc appends the given pages after all the other objects
// but before the xref block. It also updates the object ids as it goes
// starting with startId.
func appendPagesToDoc(d string, startId int, pages []*page) string {
	var b strings.Builder
	for pi, p := range pages {
		p.id = pi + startId
		b.WriteString(p.marshal())
	}
	return strings.Replace(d, "\nxref\n", "\n"+b.String()+"\n\nxref\n", 1)
}

// replaceAllDocPagesWith updates the first node of the page tree with array
// containing references to the given pages, effectively replacing all
// the existing page trees.
func replaceAllDocPagesWith(d string, pages []*page, pageTreeId int) string {
	b := &strings.Builder{}
	for _, p := range pages {
		fmt.Fprintf(b, "%d 0 R\n", p.id)
	}
	// Replace the count
	r := regexp.MustCompile(fmt.Sprintf(`(?ms)^(%d 0 obj\n.*?^\s+/Count\s+)\d+`, pageTreeId))
	d = r.ReplaceAllString(d, fmt.Sprintf(`${1}%d`, len(pages)))
	// Replace page references
	r = regexp.MustCompile(fmt.Sprintf(`(?ms)^(%d 0 obj\n.*?^\s+/Kids\s+\[)[^\]]*`, pageTreeId))
	d = r.ReplaceAllString(d, fmt.Sprintf(`${1} %s `, b.String()))
	return d
}

// getAllPages returns all the page objects in the document in order
// they appear in input.
func getAllPages(d string) []*page {
	pages := []*page{}
	// Match all the pages
	pageRe := regexp.MustCompile(`(?ms)^%% Page (\d+)\n%%[^\n]*\n\d+\s+\d+\s+obj\n<<\n(.*?)\n^>>\n^endobj`)

	pageM := pageRe.FindAllStringSubmatch(d, -1)
	for _, pm := range pageM {
		pNum, _ := strconv.Atoi(pm[1])
		p := page{number: pNum, raw: pm[2]}
		if err := p.extractAttrs(); err != nil {
			log.Print(err)
			continue
		}
		pages = append(pages, &p)
	}

	return pages
}

// numToDoubleAlpha converts a given integer to a 26 base number
// system with digits each between A-Z
func numToAlpha(n int) string {
	s := []byte(strconv.FormatInt(int64(n), 26))
	for i, c := range s {
		if c < 'a' {
			s[i] = byte('A' + (c - '0'))
		} else {
			s[i] = byte('A' + 10 + (c - 'a'))
		}
	}
	return string(s)
}

// createOverlayForPage returns a PDF object which contains:
// - white opaque margin up to bleedMargin
// - trim marks up to bleedMargin
// - other printmarks such as tile/page number
// This will update the contentIds of the page to include a ref
// to the new overlay object.
func createOverlayForPage(overlayId int, p *page) string {
	mb, bb, tb := p.mediaBox, p.bleedBox, p.trimBox
	// Draw opaque bleed margin
	stream := fmt.Sprintf(` q
	    1 1 1 rg %f %f m %f %f l %f %f l %f %f l h
	    %f %f m %f %f l %f %f l %f %f l h f
	  Q `,
		// +1s and -1s are to bleed the box outside of viewpoint
		mb.llx-1, mb.lly-1, mb.llx-1, mb.ury+1, mb.urx+1, mb.ury+1, mb.urx+1, mb.lly-1,
		bb.llx, bb.lly, bb.urx, bb.lly, bb.urx, bb.ury, bb.llx, bb.ury,
	)
	// Draw trim marks

	if !*longTrimMarks {
		stream += fmt.Sprintf(` q
		    0 0 0 rg %f w
	      %f %f m %f %f l S
	      %f %f m %f %f l S
	      %f %f m %f %f l S
	      %f %f m %f %f l S
	      %f %f m %f %f l S
	      %f %f m %f %f l S
	      %f %f m %f %f l S
	      %f %f m %f %f l S
	    Q `,
			trimMarkLineWidth,
			mb.llx-1, tb.lly, bb.llx, tb.lly,
			mb.llx-1, tb.ury, bb.llx, tb.ury,
			tb.llx, mb.ury+1, tb.llx, bb.ury,
			tb.urx, mb.ury+1, tb.urx, bb.ury,
			bb.urx, tb.ury, mb.urx+1, tb.ury,
			bb.urx, tb.lly, mb.urx+1, tb.lly,
			tb.llx, bb.lly, tb.llx, mb.lly-1,
			tb.urx, bb.lly, tb.urx, mb.lly-1,
		)
	} else {
		stream += fmt.Sprintf(` q
		    0 0 0 rg %f w
	      %f %f m %f %f l S
	      %f %f m %f %f l S
	      %f %f m %f %f l S
	      %f %f m %f %f l S
	    Q `,
			trimMarkLineWidth,
			mb.llx-1, tb.lly, mb.urx+1, tb.lly, // bottom trim line
			mb.llx-1, tb.ury, mb.urx+1, tb.ury, // top trim line
			tb.llx, mb.lly-1, tb.llx, mb.ury+1, // left trim line
			tb.urx, mb.lly-1, tb.urx, mb.ury+1, // right trim line
		)
	}
	// Draw tile ref
	vch := float32(vecCharHeight)
	stream += fmt.Sprintf(`
    q 0 0 0 rg
      q 1 0 0 1 %f %f cm %s Q
      q 1 0 0 1 %f %f cm %s Q
    Q
    q
      0 0 0 rg %f w 2 J
      %f %f m %f %f l S
      %f %f m %f %f l S
      %f %f m %f %f l %f %f l h f
      %f %f m %f %f l %f %f l h f
    Q
  `,
		bb.urx, bb.ury+vch/2, strToVecChars(numToAlpha(p.tileY), -1, 1),
		bb.urx+vch/2, bb.ury, strToVecChars(strconv.Itoa(p.tileX+1), 1, -1),
		trimMarkLineWidth,
		bb.urx+vch/2, bb.ury+vch/2, bb.urx+vch/2, bb.ury+vch*1.5,
		bb.urx+vch/2, bb.ury+vch/2, bb.urx+vch*1.5, bb.ury+vch/2,
		bb.urx+vch/4, bb.ury+vch*1.5, bb.urx+vch*3/4, bb.ury+vch*1.5, bb.urx+vch/2, bb.ury+vch*2,
		bb.urx+vch*1.5, bb.ury+vch/4, bb.urx+vch*1.5, bb.ury+vch*3/4, bb.urx+vch*2, bb.ury+vch/2,
	)
	// Draw page ref
	stream += fmt.Sprintf(` q 0 0 0 rg
    q 1 0 0 1 %f %f cm %s Q
    q 1 0 0 1 %f %f cm %s Q
  Q `,
		tb.llx-vch/2, bb.ury+vch/2, strToVecChars(strconv.Itoa(p.number), -1, 1),
		bb.llx-vch/2, bb.ury, strToVecChars("PAGE", -1, -1),
	)
	// Draw page title
	stream += fmt.Sprintf(` q 0 0 0 rg q 1 0 0 1 %f %f cm %s Q Q `,
		tb.llx+vch/2, bb.lly-vch/2, strToVecChars(*tileTitle, 1, -1),
	)
	// Draw logo
	if !*hideLogo {
		logoScale := float32(trimMargin+bleedMargin) / (4 * float32(logoDim))
		logoScaledSize := float32(logoDim) * logoScale
		stream += fmt.Sprintf(` q 0 0 0 rg q 1 0 0 1 %f %f cm q %f 0 0 %f 0 0 cm %s Q Q Q `,
			bb.llx-logoScaledSize, bb.lly-logoScaledSize, logoScale, logoScale, logoGSCmds,
		)
	}
	p.contentIds = append(p.contentIds, overlayId)
	return fmt.Sprintf("%d 0 obj\n<< /Length %d >> stream\n%sendstream\nendobj\n",
		overlayId, len(stream), stream)
}

func process() error {

	// Convert to QDF form
	data, err := convertToQDF(*inputFile)
	if err != nil {
		return err
	}

	// Get the root page tree object id
	m := regexp.MustCompile(`(?m)^\s+/Pages\s+(\d+)\s+\d+\s+R`).FindStringSubmatch(data)
	if m == nil {
		return fmt.Errorf("cannot find root page tree")
	}
	pageTreeId, _ := strconv.Atoi(m[1])

	nextId, err := getNextFreeObjectId(data)
	if err != nil {
		return err
	}

	// Convert page size (which includes margins) in mm to
	// tile sizes (which excludes margins) in pt for use with PDF
	tileW := (tileSize.width * ptsInInch / mmInInch) - (bleedMargin+trimMargin)*2
	tileH := (tileSize.height * ptsInInch / mmInInch) - (bleedMargin+trimMargin)*2

	pages := getAllPages(data)

	// Sort pages by page number if not already sorted
	sort.Slice(pages, func(i, j int) bool {
		return pages[i].number < pages[j].number
	})

	var tiles []*page
	for _, p := range pages {
		ts := cutPageToTiles(p, tileW, tileH, bleedMargin, trimMargin)
		for _, t := range ts {
			t.parentId = pageTreeId
		}
		tiles = append(tiles, ts...)
	}

	{
		// Wrap page content with graphics state preserving streams
		objs := fmt.Sprintf(
			"%d 0 obj\n<< /Length 1 >> stream\nqendstream\nendobj\n%d 0 obj\n<< /Length 1 >> stream\nQendstream\nendobj\n",
			nextId, nextId+1)
		data = strings.Replace(data, "\nxref\n", "\n"+objs+"\nxref\n", 1)
		for _, t := range tiles {
			t.contentIds = append([]int{nextId}, t.contentIds...)
			t.contentIds = append(t.contentIds, nextId+1)
		}
		nextId += 2
	}

	{
		// Create overlays and add it to the doc
		b := &strings.Builder{}
		for _, t := range tiles {
			b.WriteString(createOverlayForPage(nextId, t))
			nextId += 1
		}
		data = strings.Replace(data, "\nxref\n", "\n"+b.String()+"\nxref\n", 1)
	}

	data = appendPagesToDoc(data, nextId, tiles)
	data = replaceAllDocPagesWith(data, tiles, pageTreeId)

	// Write data back to temp file
	f, err := ioutil.TempFile("", "pdftilecut-im2-")
	if err != nil {
		return err
	}
	if !*debugMode {
		defer os.Remove(f.Name())
	}
	if _, err := f.Write([]byte(data)); err != nil {
		f.Close()
		return err
	}
	f.Close()
	data = "" // let garbage collector clean this up

	// Fix and write back an optimized PDF
	if err := convertToOptimizedPDF(f.Name(), *outputFile); err != nil {
		return err
	}

	return nil
}

// convertToOptimizedPDF converts in PDF to a compressed with
// object streams PDF using QPDF.
func convertToOptimizedPDF(in string, out string) error {
	q, err := qpdf.New()
	if err != nil {
		return err
	}
	defer q.Close()
	if !*debugMode {
		q.SetSuppressWarnings(true)
	}
	if err := q.ReadFile(in); err != nil {
		return err
	}
	// TODO enable optimization flags
	if err := q.InitFileWrite(out); err != nil {
		return err
	}
	q.SetObjectStreamMode(qpdf.ObjectStreamGenerate)
	q.SetStreamDataMode(qpdf.StreamDataPreserve)
	q.SetCompressStreams(true)
	if err := q.Write(); err != nil {
		return err
	}
	return nil
}

// convertToQDF uses QPDF to convert an input PDF to a normalized
// format that is easy to parse and manipulate.
func convertToQDF(in string) (string, error) {
	q, err := qpdf.New()
	if err != nil {
		return "", err
	}
	defer q.Close()
	if !*debugMode {
		q.SetSuppressWarnings(true)
	}
	if err := q.ReadFile(in); err != nil {
		return "", err
	}
	f, err := ioutil.TempFile("", "pdftilecut-im-")
	if err != nil {
		return "", nil
	}
	f.Close()
	if !*debugMode {
		defer os.Remove(f.Name())
	}
	if err := q.InitFileWrite(f.Name()); err != nil {
		return "", err
	}
	q.SetQDFMode(true)
	q.SetObjectStreamMode(qpdf.ObjectStreamDisable)
	q.SetStreamDataMode(qpdf.StreamDataPreserve)
	if err := q.Write(); err != nil {
		return "", err
	}
	q.Close() // free up memory as soon as possible

	f, err = os.Open(f.Name())
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := ioutil.ReadAll(f)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	flag.Parse()

	// Create temp file for input and output if needed
	if *inputFile == "-" {
		f, err := ioutil.TempFile("", "pdftilecut-in-")
		if err != nil {
			return err
		}
		defer os.Remove(f.Name())
		if _, err := io.Copy(f, os.Stdin); err != nil {
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		*inputFile = f.Name()
		if *tileTitle == "" {
			*tileTitle = "stdin"
		}
	} else if *tileTitle == "" {
		*tileTitle = filepath.Base(*inputFile)
	}
	*tileTitle = strings.ToUpper(*tileTitle)

	var toStdout bool

	if *outputFile == "-" {
		f, err := ioutil.TempFile("", "pdftilecut-out-")
		if err != nil {
			return err
		}
		f.Close()
		defer os.Remove(f.Name())
		*outputFile = f.Name()
		toStdout = true
	}

	// Tile cut
	if err := process(); err != nil {
		return err
	}

	// Cleanup
	if toStdout {
		f, err := os.Open(*outputFile)
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := io.Copy(os.Stdout, f); err != nil {
			return err
		}
	}

	return nil
}
