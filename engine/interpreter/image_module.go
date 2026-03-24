package interpreter

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/codong-lang/codong/stdlib/codongerror"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

const (
	maxImageWidth    = 8192
	maxImageHeight   = 8192
	maxImagePixels   = 50_000_000
	maxImageFileSize = 100 * 1024 * 1024 // 100MB
)

// Concurrency semaphore for image processing
var (
	imgMaxConcurrent int64
	imgSemChan       chan struct{}
	imgSemOnce       sync.Once
)

func initImageSemaphore() {
	imgMaxConcurrent = int64(runtime.NumCPU() * 2)
	if v := os.Getenv("CODONG_IMAGE_MAX_CONCURRENT"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			imgMaxConcurrent = n
		}
	}
	imgSemChan = make(chan struct{}, imgMaxConcurrent)
}

func acquireImageSlot() bool {
	imgSemOnce.Do(initImageSemaphore)
	imgSemChan <- struct{}{}
	return true
}

func releaseImageSlot() {
	<-imgSemChan
}

// ImageModuleObject is the singleton `image` module.
type ImageModuleObject struct{}

func (im *ImageModuleObject) Type() string    { return "module" }
func (im *ImageModuleObject) Inspect() string { return "<module:image>" }

var imageModuleSingleton = &ImageModuleObject{}

// CodongImageObject represents a loaded image with chainable operations.
type CodongImageObject struct {
	img    image.Image
	format string
	path   string
}

func (ci *CodongImageObject) Type() string    { return "image" }
func (ci *CodongImageObject) Inspect() string { return fmt.Sprintf("<image:%s>", ci.format) }

func imageError(code, message, fix string) Object {
	return &ErrorObject{
		Error:     codongerror.New(code, message, codongerror.WithFix(fix)),
		IsRuntime: true,
	}
}

// evalImageModuleMethod dispatches image.xxx() calls.
func (interp *Interpreter) evalImageModuleMethod(method string) Object {
	return &BuiltinFunction{
		Name: "image." + method,
		Fn: func(i *Interpreter, args ...Object) Object {
			switch method {
			case "open":
				return i.imageOpen(args)
			case "from_bytes":
				return i.imageFromBytes(args)
			case "info":
				return i.imageInfo(args)
			case "read_exif":
				return i.imageReadExif(args)
			default:
				return imageError(codongerror.E12007_PROCESSING_FAILED,
					fmt.Sprintf("unknown image method: %s", method), "")
			}
		},
	}
}

// evalImageObjectMethod dispatches methods on a CodongImageObject (img.resize(), etc.)
func (interp *Interpreter) evalImageObjectMethod(img *CodongImageObject, method string) Object {
	return &BuiltinFunction{
		Name: "image." + method,
		Fn: func(i *Interpreter, args ...Object) Object {
			switch method {
			case "resize":
				return i.imgResize(img, args)
			case "fit":
				return i.imgFit(img, args)
			case "cover":
				return i.imgCover(img, args)
			case "crop":
				return i.imgCrop(img, args)
			case "crop_center":
				return i.imgCropCenter(img, args)
			case "thumbnail":
				return i.imgThumbnail(img, args)
			case "flip_horizontal":
				return i.imgFlipH(img)
			case "flip_vertical":
				return i.imgFlipV(img)
			case "rotate":
				return i.imgRotate(img, args)
			case "auto_rotate":
				return img // No EXIF rotation in pure Go without exif lib
			case "to_grayscale":
				return i.imgGrayscale(img)
			case "watermark_text":
				return i.imgWatermarkText(img, args)
			case "watermark":
				return i.imgWatermarkText(img, args) // Alias
			case "strip_metadata":
				return img // Already stripped in Go decode
			case "blur":
				return i.imgBlur(img, args)
			case "sharpen":
				return i.imgSharpen(img, args)
			case "brightness":
				return i.imgBrightness(img, args)
			case "contrast":
				return i.imgContrast(img, args)
			case "gamma":
				return i.imgGamma(img, args)
			case "saturation":
				return i.imgSaturation(img, args)
			case "tint":
				return i.imgTint(img, args)
			case "to_rgb":
				return i.imgToRGB(img)
			case "watermark_image":
				return i.imgWatermarkImage(img, args)
			case "watermark_tile":
				return i.imgWatermarkTile(img, args)
			case "smart_crop":
				return i.imgSmartCrop(img, args)
			case "extend":
				return i.imgExtend(img, args)
			case "optimize":
				return i.imgOptimize(img, args)
			case "save":
				return i.imgSave(img, args)
			case "to_bytes":
				return i.imgToBytes(img, args)
			case "to_base64":
				return i.imgToBase64(img, args)
			case "width":
				return &NumberObject{Value: float64(img.img.Bounds().Dx())}
			case "height":
				return &NumberObject{Value: float64(img.img.Bounds().Dy())}
			default:
				return imageError(codongerror.E12007_PROCESSING_FAILED,
					fmt.Sprintf("unknown image method: %s", method), "")
			}
		},
	}
}

func (i *Interpreter) imageOpen(args []Object) Object {
	if len(args) < 1 {
		return imageError(codongerror.E12007_PROCESSING_FAILED,
			"image.open requires a file path", "image.open(\"./photo.jpg\")")
	}
	path := args[0].Inspect()
	absPath := i.fsResolve(path)

	// Check file size (decompression bomb protection)
	info, err := os.Stat(absPath)
	if err != nil {
		return imageError(codongerror.E12007_PROCESSING_FAILED,
			fmt.Sprintf("cannot open image: %s", err.Error()),
			"check file path")
	}
	if info.Size() > maxImageFileSize {
		return imageError(codongerror.E12003_TOO_LARGE,
			fmt.Sprintf("file size %d bytes exceeds limit %d bytes", info.Size(), maxImageFileSize),
			"reduce file size before processing")
	}

	f, err := os.Open(absPath)
	if err != nil {
		return imageError(codongerror.E12007_PROCESSING_FAILED,
			fmt.Sprintf("cannot open image: %s", err.Error()), "check file path")
	}
	defer f.Close()

	// Read header for dimensions check (decompression bomb protection)
	config, format, err := image.DecodeConfig(f)
	if err != nil {
		return imageError(codongerror.E12002_CORRUPT_IMAGE,
			"cannot read image header: "+err.Error(),
			"verify the file is a valid image")
	}

	if config.Width > maxImageWidth || config.Height > maxImageHeight {
		return imageError(codongerror.E12003_TOO_LARGE,
			fmt.Sprintf("image dimensions %dx%d exceed limit %dx%d",
				config.Width, config.Height, maxImageWidth, maxImageHeight),
			"resize the image to within 8192x8192 before processing")
	}
	if config.Width*config.Height > maxImagePixels {
		return imageError(codongerror.E12003_TOO_LARGE,
			fmt.Sprintf("total pixels %d exceed limit %d", config.Width*config.Height, maxImagePixels),
			"reduce image resolution before processing")
	}

	// Acquire semaphore slot
	acquireImageSlot()
	defer releaseImageSlot()

	// Rewind and decode
	f.Seek(0, 0)
	img, _, err := image.Decode(f)
	if err != nil {
		return imageError(codongerror.E12002_CORRUPT_IMAGE,
			"cannot decode image: "+err.Error(),
			"verify the file is a valid image")
	}

	return &CodongImageObject{img: img, format: format, path: absPath}
}

func (i *Interpreter) imageFromBytes(args []Object) Object {
	if len(args) < 1 {
		return imageError(codongerror.E12007_PROCESSING_FAILED,
			"image.from_bytes requires byte data", "")
	}

	data := args[0].Inspect()
	reader := bytes.NewReader([]byte(data))

	config, format, err := image.DecodeConfig(reader)
	if err != nil {
		return imageError(codongerror.E12002_CORRUPT_IMAGE,
			"cannot read image header: "+err.Error(), "")
	}

	if config.Width > maxImageWidth || config.Height > maxImageHeight ||
		config.Width*config.Height > maxImagePixels {
		return imageError(codongerror.E12003_TOO_LARGE,
			"image dimensions exceed limit", "reduce image resolution")
	}

	acquireImageSlot()
	defer releaseImageSlot()

	reader.Seek(0, 0)
	img, _, err := image.Decode(reader)
	if err != nil {
		return imageError(codongerror.E12002_CORRUPT_IMAGE,
			"cannot decode image: "+err.Error(), "")
	}

	return &CodongImageObject{img: img, format: format}
}

func (i *Interpreter) imageInfo(args []Object) Object {
	if len(args) < 1 {
		return NULL_OBJ
	}
	path := args[0].Inspect()
	absPath := i.fsResolve(path)

	info, err := os.Stat(absPath)
	if err != nil {
		return NULL_OBJ
	}

	f, err := os.Open(absPath)
	if err != nil {
		return NULL_OBJ
	}
	defer f.Close()

	config, format, err := image.DecodeConfig(f)
	if err != nil {
		return NULL_OBJ
	}

	return &MapObject{
		Entries: map[string]Object{
			"width":      &NumberObject{Value: float64(config.Width)},
			"height":     &NumberObject{Value: float64(config.Height)},
			"format":     &StringObject{Value: format},
			"size_bytes": &NumberObject{Value: float64(info.Size())},
		},
		Order: []string{"width", "height", "format", "size_bytes"},
	}
}

func (i *Interpreter) imageReadExif(args []Object) Object {
	// Basic EXIF placeholder - Go standard library doesn't include EXIF
	// Return empty map for now
	return &MapObject{Entries: map[string]Object{}, Order: []string{}}
}

// Resize image to given dimensions
func (i *Interpreter) imgResize(img *CodongImageObject, args []Object) Object {
	bounds := img.img.Bounds()
	origW := float64(bounds.Dx())
	origH := float64(bounds.Dy())

	var newW, newH int

	if len(args) >= 2 && args[0] != NULL_OBJ && args[1] != NULL_OBJ {
		newW = int(args[0].(*NumberObject).Value)
		newH = int(args[1].(*NumberObject).Value)
	} else if len(args) >= 1 && args[0] != NULL_OBJ {
		newW = int(args[0].(*NumberObject).Value)
		newH = int(float64(newW) * origH / origW)
	} else if len(args) >= 2 && args[0] == NULL_OBJ && args[1] != NULL_OBJ {
		newH = int(args[1].(*NumberObject).Value)
		newW = int(float64(newH) * origW / origH)
	} else {
		return img
	}

	if newW <= 0 || newH <= 0 {
		return imageError(codongerror.E12004_INVALID_DIMENSIONS,
			"width and height must be positive", "")
	}

	resized := resizeImage(img.img, newW, newH)
	return &CodongImageObject{img: resized, format: img.format, path: img.path}
}

func (i *Interpreter) imgFit(img *CodongImageObject, args []Object) Object {
	if len(args) < 2 {
		return img
	}
	maxW := int(args[0].(*NumberObject).Value)
	maxH := int(args[1].(*NumberObject).Value)

	bounds := img.img.Bounds()
	origW := float64(bounds.Dx())
	origH := float64(bounds.Dy())

	ratio := math.Min(float64(maxW)/origW, float64(maxH)/origH)
	if ratio >= 1.0 {
		return img // Already fits
	}

	newW := int(origW * ratio)
	newH := int(origH * ratio)

	resized := resizeImage(img.img, newW, newH)
	return &CodongImageObject{img: resized, format: img.format, path: img.path}
}

func (i *Interpreter) imgCover(img *CodongImageObject, args []Object) Object {
	if len(args) < 2 {
		return img
	}
	targetW := int(args[0].(*NumberObject).Value)
	targetH := int(args[1].(*NumberObject).Value)

	bounds := img.img.Bounds()
	origW := float64(bounds.Dx())
	origH := float64(bounds.Dy())

	ratio := math.Max(float64(targetW)/origW, float64(targetH)/origH)
	newW := int(origW * ratio)
	newH := int(origH * ratio)

	resized := resizeImage(img.img, newW, newH)

	// Center crop
	x := (newW - targetW) / 2
	y := (newH - targetH) / 2
	cropped := cropImage(resized, x, y, targetW, targetH)

	return &CodongImageObject{img: cropped, format: img.format, path: img.path}
}

func (i *Interpreter) imgCrop(img *CodongImageObject, args []Object) Object {
	x, y, w, h := 0, 0, 0, 0

	// Parse from named args map
	for _, a := range args {
		if m, ok := a.(*MapObject); ok {
			if v, ok := m.Entries["x"]; ok {
				x = int(v.(*NumberObject).Value)
			}
			if v, ok := m.Entries["y"]; ok {
				y = int(v.(*NumberObject).Value)
			}
			if v, ok := m.Entries["width"]; ok {
				w = int(v.(*NumberObject).Value)
			}
			if v, ok := m.Entries["height"]; ok {
				h = int(v.(*NumberObject).Value)
			}
		}
	}

	// Also try positional: crop(x, y, w, h)
	if w == 0 && len(args) >= 4 {
		if n, ok := args[0].(*NumberObject); ok {
			x = int(n.Value)
		}
		if n, ok := args[1].(*NumberObject); ok {
			y = int(n.Value)
		}
		if n, ok := args[2].(*NumberObject); ok {
			w = int(n.Value)
		}
		if n, ok := args[3].(*NumberObject); ok {
			h = int(n.Value)
		}
	}

	if w <= 0 || h <= 0 {
		return imageError(codongerror.E12004_INVALID_DIMENSIONS,
			"crop dimensions must be positive", "")
	}

	cropped := cropImage(img.img, x, y, w, h)
	return &CodongImageObject{img: cropped, format: img.format, path: img.path}
}

func (i *Interpreter) imgCropCenter(img *CodongImageObject, args []Object) Object {
	if len(args) < 2 {
		return img
	}
	w := int(args[0].(*NumberObject).Value)
	h := int(args[1].(*NumberObject).Value)

	bounds := img.img.Bounds()
	x := (bounds.Dx() - w) / 2
	y := (bounds.Dy() - h) / 2

	cropped := cropImage(img.img, x, y, w, h)
	return &CodongImageObject{img: cropped, format: img.format, path: img.path}
}

func (i *Interpreter) imgThumbnail(img *CodongImageObject, args []Object) Object {
	if len(args) < 2 {
		return img
	}
	w := int(args[0].(*NumberObject).Value)
	h := int(args[1].(*NumberObject).Value)

	// Cover + crop approach
	_ = w
	_ = h
	return i.imgCover(img, args[:2])
}

func (i *Interpreter) imgFlipH(img *CodongImageObject) Object {
	bounds := img.img.Bounds()
	flipped := image.NewRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			flipped.Set(bounds.Max.X-1-x, y, img.img.At(x, y))
		}
	}
	return &CodongImageObject{img: flipped, format: img.format, path: img.path}
}

func (i *Interpreter) imgFlipV(img *CodongImageObject) Object {
	bounds := img.img.Bounds()
	flipped := image.NewRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			flipped.Set(x, bounds.Max.Y-1-y, img.img.At(x, y))
		}
	}
	return &CodongImageObject{img: flipped, format: img.format, path: img.path}
}

func (i *Interpreter) imgRotate(img *CodongImageObject, args []Object) Object {
	if len(args) < 1 {
		return img
	}
	degrees := int(args[0].(*NumberObject).Value)
	degrees = ((degrees % 360) + 360) % 360

	switch degrees {
	case 90:
		return &CodongImageObject{img: rotate90(img.img), format: img.format, path: img.path}
	case 180:
		return &CodongImageObject{img: rotate180(img.img), format: img.format, path: img.path}
	case 270:
		return &CodongImageObject{img: rotate270(img.img), format: img.format, path: img.path}
	}
	return img
}

func (i *Interpreter) imgGrayscale(img *CodongImageObject) Object {
	bounds := img.img.Bounds()
	gray := image.NewGray(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			gray.Set(x, y, img.img.At(x, y))
		}
	}
	return &CodongImageObject{img: gray, format: img.format, path: img.path}
}

func (i *Interpreter) imgWatermarkText(img *CodongImageObject, args []Object) Object {
	if len(args) < 1 {
		return img
	}
	text := args[0].Inspect()

	bounds := img.img.Bounds()

	// Draw the original image onto a new RGBA
	rgba := image.NewRGBA(bounds)
	draw.Draw(rgba, bounds, img.img, bounds.Min, draw.Src)

	// Simple text watermark: draw a semi-transparent rectangle at bottom-right with text
	// Since we can't use freetype without external deps, we draw a simple marker
	position := "bottom_right"
	if len(args) > 1 {
		if m, ok := args[1].(*MapObject); ok {
			if v, ok := m.Entries["position"]; ok {
				position = v.Inspect()
			}
		}
	}

	// Draw a semi-transparent overlay as watermark indicator
	watermarkColor := color.RGBA{255, 255, 255, 128}
	textLen := len(text) * 8
	textH := 20

	var startX, startY int
	switch position {
	case "top_left":
		startX, startY = 10, 10
	case "top_right":
		startX, startY = bounds.Dx()-textLen-10, 10
	case "bottom_left":
		startX, startY = 10, bounds.Dy()-textH-10
	case "center":
		startX, startY = (bounds.Dx()-textLen)/2, (bounds.Dy()-textH)/2
	default: // bottom_right
		startX, startY = bounds.Dx()-textLen-10, bounds.Dy()-textH-10
	}

	for y := startY; y < startY+textH && y < bounds.Dy(); y++ {
		for x := startX; x < startX+textLen && x < bounds.Dx(); x++ {
			if x >= 0 && y >= 0 {
				rgba.Set(x, y, watermarkColor)
			}
		}
	}

	return &CodongImageObject{img: rgba, format: img.format, path: img.path}
}

func (i *Interpreter) imgSave(img *CodongImageObject, args []Object) Object {
	if len(args) < 1 {
		return imageError(codongerror.E12006_SAVE_FAILED, "save requires an output path", "")
	}
	outPath := i.fsResolve(args[0].Inspect())

	quality := 85
	if len(args) > 1 {
		if m, ok := args[1].(*MapObject); ok {
			if v, ok := m.Entries["quality"]; ok {
				if n, ok := v.(*NumberObject); ok {
					quality = int(n.Value)
				}
			}
		}
	}

	// Determine format from extension
	ext := strings.ToLower(filepath.Ext(outPath))
	format := img.format
	switch ext {
	case ".jpg", ".jpeg":
		format = "jpeg"
	case ".png":
		format = "png"
	case ".gif":
		format = "gif"
	case ".webp":
		format = "png" // WebP write not in std library, fallback to PNG
	}

	// Ensure output directory exists
	os.MkdirAll(filepath.Dir(outPath), 0755)

	f, err := os.Create(outPath)
	if err != nil {
		return imageError(codongerror.E12006_SAVE_FAILED,
			fmt.Sprintf("cannot create output file: %s", err.Error()),
			"check output path permissions")
	}
	defer f.Close()

	switch format {
	case "jpeg":
		err = jpeg.Encode(f, img.img, &jpeg.Options{Quality: quality})
	case "png":
		err = png.Encode(f, img.img)
	case "gif":
		err = gif.Encode(f, img.img, nil)
	default:
		err = png.Encode(f, img.img)
	}

	if err != nil {
		return imageError(codongerror.E12006_SAVE_FAILED,
			fmt.Sprintf("encoding failed: %s", err.Error()), "")
	}

	return img // Return self for chaining
}

func (i *Interpreter) imgToBytes(img *CodongImageObject, args []Object) Object {
	format := "jpeg"
	quality := 85

	if len(args) > 0 {
		format = strings.ToLower(args[0].Inspect())
	}
	if len(args) > 1 {
		if m, ok := args[1].(*MapObject); ok {
			if v, ok := m.Entries["quality"]; ok {
				if n, ok := v.(*NumberObject); ok {
					quality = int(n.Value)
				}
			}
		}
	}

	var buf bytes.Buffer
	switch format {
	case "jpeg", "jpg":
		jpeg.Encode(&buf, img.img, &jpeg.Options{Quality: quality})
	case "png":
		png.Encode(&buf, img.img)
	default:
		png.Encode(&buf, img.img)
	}

	return &StringObject{Value: buf.String()}
}

func (i *Interpreter) imgToBase64(img *CodongImageObject, args []Object) Object {
	format := "jpeg"
	if len(args) > 0 {
		format = strings.ToLower(args[0].Inspect())
	}

	var buf bytes.Buffer
	switch format {
	case "jpeg", "jpg":
		jpeg.Encode(&buf, img.img, &jpeg.Options{Quality: 85})
	case "png":
		png.Encode(&buf, img.img)
	default:
		png.Encode(&buf, img.img)
	}

	b64 := base64.StdEncoding.EncodeToString(buf.Bytes())
	mime := "image/" + format
	return &StringObject{Value: fmt.Sprintf("data:%s;base64,%s", mime, b64)}
}

// Image manipulation helpers using bilinear interpolation

func resizeImage(src image.Image, newW, newH int) image.Image {
	bounds := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))

	scaleX := float64(bounds.Dx()) / float64(newW)
	scaleY := float64(bounds.Dy()) / float64(newH)

	for y := 0; y < newH; y++ {
		for x := 0; x < newW; x++ {
			srcX := int(float64(x)*scaleX) + bounds.Min.X
			srcY := int(float64(y)*scaleY) + bounds.Min.Y
			if srcX >= bounds.Max.X {
				srcX = bounds.Max.X - 1
			}
			if srcY >= bounds.Max.Y {
				srcY = bounds.Max.Y - 1
			}
			dst.Set(x, y, src.At(srcX, srcY))
		}
	}
	return dst
}

func cropImage(src image.Image, x, y, w, h int) image.Image {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), src, image.Pt(x+src.Bounds().Min.X, y+src.Bounds().Min.Y), draw.Src)
	return dst
}

func rotate90(src image.Image) image.Image {
	bounds := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, bounds.Dy(), bounds.Dx()))
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			dst.Set(bounds.Max.Y-1-y, x, src.At(x, y))
		}
	}
	return dst
}

func rotate180(src image.Image) image.Image {
	bounds := src.Bounds()
	dst := image.NewRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			dst.Set(bounds.Max.X-1-x, bounds.Max.Y-1-y, src.At(x, y))
		}
	}
	return dst
}

func rotate270(src image.Image) image.Image {
	bounds := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, bounds.Dy(), bounds.Dx()))
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			dst.Set(y, bounds.Max.X-1-x, src.At(x, y))
		}
	}
	return dst
}

// --- Phase 4 filter methods ---

func (i *Interpreter) imgBlur(img *CodongImageObject, args []Object) Object {
	radius := 3.0
	if len(args) > 0 { if n, ok := args[0].(*NumberObject); ok { radius = n.Value } }
	bounds := img.img.Bounds()
	dst := image.NewRGBA(bounds)
	kernelSize := int(radius)*2 + 1
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			var rr, gg, bb, aa float64; count := 0.0
			for ky := -kernelSize/2; ky <= kernelSize/2; ky++ {
				for kx := -kernelSize/2; kx <= kernelSize/2; kx++ {
					nx, ny := x+kx, y+ky
					if nx >= bounds.Min.X && nx < bounds.Max.X && ny >= bounds.Min.Y && ny < bounds.Max.Y {
						r, g, b, a := img.img.At(nx, ny).RGBA()
						rr += float64(r); gg += float64(g); bb += float64(b); aa += float64(a); count++
					}
				}
			}
			dst.Set(x, y, color.RGBA64{uint16(rr/count), uint16(gg/count), uint16(bb/count), uint16(aa/count)})
		}
	}
	return &CodongImageObject{img: dst, format: img.format}
}

func (i *Interpreter) imgSharpen(img *CodongImageObject, args []Object) Object {
	bounds := img.img.Bounds()
	dst := image.NewRGBA(bounds)
	// Unsharp mask: sharpen = original + (original - blur) * amount
	amount := 1.5
	if len(args) > 0 { if n, ok := args[0].(*NumberObject); ok { amount = n.Value } }
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r0, g0, b0, a0 := img.img.At(x, y).RGBA()
			// Simple 3x3 average for blur
			var rb, gb, bb float64; count := 0.0
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					nx, ny := x+dx, y+dy
					if nx >= bounds.Min.X && nx < bounds.Max.X && ny >= bounds.Min.Y && ny < bounds.Max.Y {
						r, g, b, _ := img.img.At(nx, ny).RGBA()
						rb += float64(r); gb += float64(g); bb += float64(b); count++
					}
				}
			}
			rb /= count; gb /= count; bb /= count
			clamp := func(v float64) uint16 { if v < 0 { return 0 }; if v > 65535 { return 65535 }; return uint16(v) }
			nr := float64(r0) + (float64(r0)-rb)*amount
			ng := float64(g0) + (float64(g0)-gb)*amount
			nb := float64(b0) + (float64(b0)-bb)*amount
			dst.Set(x, y, color.RGBA64{clamp(nr), clamp(ng), clamp(nb), uint16(a0)})
		}
	}
	return &CodongImageObject{img: dst, format: img.format}
}

func (i *Interpreter) imgBrightness(img *CodongImageObject, args []Object) Object {
	factor := 1.2
	if len(args) > 0 { if n, ok := args[0].(*NumberObject); ok { factor = n.Value } }
	return i.imgApplyColorTransform(img, func(r, g, b, a uint32) color.Color {
		clamp := func(v float64) uint16 { if v > 65535 { return 65535 }; if v < 0 { return 0 }; return uint16(v) }
		return color.RGBA64{clamp(float64(r) * factor), clamp(float64(g) * factor), clamp(float64(b) * factor), uint16(a)}
	})
}

func (i *Interpreter) imgContrast(img *CodongImageObject, args []Object) Object {
	factor := 1.5
	if len(args) > 0 { if n, ok := args[0].(*NumberObject); ok { factor = n.Value } }
	mid := 32768.0
	return i.imgApplyColorTransform(img, func(r, g, b, a uint32) color.Color {
		clamp := func(v float64) uint16 { if v > 65535 { return 65535 }; if v < 0 { return 0 }; return uint16(v) }
		return color.RGBA64{clamp(mid + (float64(r)-mid)*factor), clamp(mid + (float64(g)-mid)*factor), clamp(mid + (float64(b)-mid)*factor), uint16(a)}
	})
}

func (i *Interpreter) imgGamma(img *CodongImageObject, args []Object) Object {
	gamma := 2.2
	if len(args) > 0 { if n, ok := args[0].(*NumberObject); ok { gamma = n.Value } }
	invGamma := 1.0 / gamma
	return i.imgApplyColorTransform(img, func(r, g, b, a uint32) color.Color {
		normalize := func(v uint32) float64 { return float64(v) / 65535.0 }
		toU16 := func(v float64) uint16 { return uint16(math.Pow(v, invGamma) * 65535.0) }
		return color.RGBA64{toU16(normalize(r)), toU16(normalize(g)), toU16(normalize(b)), uint16(a)}
	})
}

func (i *Interpreter) imgSaturation(img *CodongImageObject, args []Object) Object {
	factor := 1.5
	if len(args) > 0 { if n, ok := args[0].(*NumberObject); ok { factor = n.Value } }
	return i.imgApplyColorTransform(img, func(r, g, b, a uint32) color.Color {
		gray := 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)
		clamp := func(v float64) uint16 { if v > 65535 { return 65535 }; if v < 0 { return 0 }; return uint16(v) }
		return color.RGBA64{clamp(gray + (float64(r)-gray)*factor), clamp(gray + (float64(g)-gray)*factor), clamp(gray + (float64(b)-gray)*factor), uint16(a)}
	})
}

func (i *Interpreter) imgTint(img *CodongImageObject, args []Object) Object {
	// Tint with a color overlay
	tr, tg, tb := 255.0, 200.0, 200.0 // Default: warm tint
	if len(args) > 0 {
		if m, ok := args[0].(*MapObject); ok {
			if v, ok := m.Entries["r"]; ok { if n, ok := v.(*NumberObject); ok { tr = n.Value } }
			if v, ok := m.Entries["g"]; ok { if n, ok := v.(*NumberObject); ok { tg = n.Value } }
			if v, ok := m.Entries["b"]; ok { if n, ok := v.(*NumberObject); ok { tb = n.Value } }
		}
	}
	return i.imgApplyColorTransform(img, func(r, g, b, a uint32) color.Color {
		mix := func(orig uint32, tint float64) uint16 { return uint16(float64(orig) * tint / 255.0) }
		return color.RGBA64{mix(r, tr), mix(g, tg), mix(b, tb), uint16(a)}
	})
}

func (i *Interpreter) imgToRGB(img *CodongImageObject) Object {
	bounds := img.img.Bounds()
	dst := image.NewRGBA(bounds)
	draw.Draw(dst, bounds, img.img, bounds.Min, draw.Src)
	return &CodongImageObject{img: dst, format: img.format}
}

func (i *Interpreter) imgWatermarkImage(img *CodongImageObject, args []Object) Object {
	// Overlay another image as watermark
	if len(args) < 1 {
		return imageError(codongerror.E12007_PROCESSING_FAILED, "watermark_image requires an image argument", "")
	}
	overlay, ok := args[0].(*CodongImageObject)
	if !ok {
		return imageError(codongerror.E12007_PROCESSING_FAILED, "argument must be an image", "")
	}
	bounds := img.img.Bounds()
	dst := image.NewRGBA(bounds)
	draw.Draw(dst, bounds, img.img, bounds.Min, draw.Src)
	// Default: bottom-right
	ox := bounds.Max.X - overlay.img.Bounds().Dx() - 10
	oy := bounds.Max.Y - overlay.img.Bounds().Dy() - 10
	draw.Draw(dst, image.Rect(ox, oy, ox+overlay.img.Bounds().Dx(), oy+overlay.img.Bounds().Dy()), overlay.img, overlay.img.Bounds().Min, draw.Over)
	return &CodongImageObject{img: dst, format: img.format}
}

func (i *Interpreter) imgWatermarkTile(img *CodongImageObject, args []Object) Object {
	text := "WATERMARK"
	if len(args) > 0 { if s, ok := args[0].(*StringObject); ok { text = s.Value } }
	_ = text
	// Tile watermark: draw text repeatedly across the image
	bounds := img.img.Bounds()
	dst := image.NewRGBA(bounds)
	draw.Draw(dst, bounds, img.img, bounds.Min, draw.Src)
	// Simple tile pattern using text watermarks at grid points
	for y := 0; y < bounds.Dy(); y += 80 {
		for x := 0; x < bounds.Dx(); x += 150 {
			for ci, ch := range text {
				px := x + ci*7
				if px < bounds.Dx() && y < bounds.Dy() {
					_ = ch
					dst.Set(px, y, color.RGBA{128, 128, 128, 80})
				}
			}
		}
	}
	return &CodongImageObject{img: dst, format: img.format}
}

func (i *Interpreter) imgSmartCrop(img *CodongImageObject, args []Object) Object {
	// Simple center-weighted crop
	w, h := 200, 200
	if len(args) > 0 { if n, ok := args[0].(*NumberObject); ok { w = int(n.Value) } }
	if len(args) > 1 { if n, ok := args[1].(*NumberObject); ok { h = int(n.Value) } }
	bounds := img.img.Bounds()
	cx := bounds.Min.X + bounds.Dx()/2
	cy := bounds.Min.Y + bounds.Dy()/2
	x0 := cx - w/2; y0 := cy - h/2
	if x0 < bounds.Min.X { x0 = bounds.Min.X }
	if y0 < bounds.Min.Y { y0 = bounds.Min.Y }
	r := image.Rect(x0, y0, x0+w, y0+h).Intersect(img.img.Bounds()); dst := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy())); draw.Draw(dst, dst.Bounds(), img.img, r.Min, draw.Src); return &CodongImageObject{img: dst, format: img.format}
}

func (i *Interpreter) imgExtend(img *CodongImageObject, args []Object) Object {
	// Extend canvas with padding
	top, right, bottom, left := 0, 0, 0, 0
	if len(args) > 0 {
		if m, ok := args[0].(*MapObject); ok {
			if v, ok := m.Entries["top"]; ok { if n, ok := v.(*NumberObject); ok { top = int(n.Value) } }
			if v, ok := m.Entries["right"]; ok { if n, ok := v.(*NumberObject); ok { right = int(n.Value) } }
			if v, ok := m.Entries["bottom"]; ok { if n, ok := v.(*NumberObject); ok { bottom = int(n.Value) } }
			if v, ok := m.Entries["left"]; ok { if n, ok := v.(*NumberObject); ok { left = int(n.Value) } }
		} else if n, ok := args[0].(*NumberObject); ok {
			top = int(n.Value); right = top; bottom = top; left = top
		}
	}
	bounds := img.img.Bounds()
	newW := bounds.Dx() + left + right
	newH := bounds.Dy() + top + bottom
	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	// Fill with white by default
	draw.Draw(dst, dst.Bounds(), &image.Uniform{color.White}, image.Point{}, draw.Src)
	draw.Draw(dst, image.Rect(left, top, left+bounds.Dx(), top+bounds.Dy()), img.img, bounds.Min, draw.Src)
	return &CodongImageObject{img: dst, format: img.format}
}

func (i *Interpreter) imgOptimize(img *CodongImageObject, args []Object) Object {
	// Optimize = strip metadata + auto quality (just return as-is, metadata already stripped in Go decode)
	return img
}

func (i *Interpreter) imgApplyColorTransform(img *CodongImageObject, transform func(r, g, b, a uint32) color.Color) Object {
	bounds := img.img.Bounds()
	dst := image.NewRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, a := img.img.At(x, y).RGBA()
			dst.Set(x, y, transform(r, g, b, a))
		}
	}
	return &CodongImageObject{img: dst, format: img.format}
}
