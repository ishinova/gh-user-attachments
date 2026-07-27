package userattachments

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writePNG(t *testing.T, path string) {
	t.Helper()
	value := image.NewRGBA(image.Rect(0, 0, 2, 2))
	value.Set(0, 0, color.RGBA{R: 255, A: 255})
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, value); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestLoadAssetAcceptsValidatedPNG(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Screen Shot [1].png")
	writePNG(t, path)

	asset, err := loadAsset(path)
	if err != nil {
		t.Fatal(err)
	}
	if asset.Name != "Screen Shot [1].png" || asset.MediaType != "image/png" {
		t.Fatalf("asset = %#v", asset)
	}
	if asset.Size <= 0 {
		t.Fatalf("asset size=%d", asset.Size)
	}
}

func TestLoadAssetsAcceptsMultipleDistinctFilesInOrder(t *testing.T) {
	directory := t.TempDir()
	first := filepath.Join(directory, "first.png")
	second := filepath.Join(directory, "second.png")
	writePNG(t, first)
	writePNG(t, second)

	assets, err := loadAssets([]string{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 2 || assets[0].Name != "first.png" || assets[1].Name != "second.png" {
		t.Fatalf("assets=%#v", assets)
	}
	for _, asset := range assets {
		if asset.Size <= 0 {
			t.Fatalf("asset size=%d for %s", asset.Size, asset.Name)
		}
	}
}

func TestMaterializeAssetReadsOneValidatedFileForUpload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "selected.png")
	writePNG(t, path)
	descriptor, err := loadAsset(path)
	if err != nil {
		t.Fatal(err)
	}

	loaded, err := materializeAsset(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(loaded.Content)) != descriptor.Size {
		t.Fatalf("content=%d bytes want=%d", len(loaded.Content), descriptor.Size)
	}
}

func TestMaterializeAssetRejectsAFileChangedAfterBatchValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "changed.png")
	writePNG(t, path)
	descriptor, err := loadAsset(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = materializeAsset(descriptor)
	if err == nil || !strings.Contains(err.Error(), "changed after validation") {
		t.Fatalf("error=%v", err)
	}
}

func TestLoadAssetsRejectsDuplicatePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "same.png")
	writePNG(t, path)

	_, err := loadAssets([]string{path, path})
	if err == nil || !strings.Contains(err.Error(), "duplicate --file") {
		t.Fatalf("error=%v", err)
	}
}

func TestLoadAssetsRejectsHardlinkDuplicate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hardlink creation is not reliably available on Windows hosts")
	}
	directory := t.TempDir()
	original := filepath.Join(directory, "original.png")
	link := filepath.Join(directory, "link.png")
	writePNG(t, original)
	if err := os.Link(original, link); err != nil {
		t.Fatal(err)
	}

	_, err := loadAssets([]string{original, link})
	if err == nil || !strings.Contains(err.Error(), "duplicate --file") {
		t.Fatalf("error=%v", err)
	}
}

// documentedFileTypes is the extension set GitHub's Attaching files
// documentation advertises, maintained from that page rather than from
// supportedFileTypes. Keeping it independent of the production map lets a test
// notice an extension the implementation stopped accepting.
var documentedFileTypes = map[string]string{
	".png":        "image/png",
	".gif":        "image/gif",
	".jpg":        "image/jpeg",
	".jpeg":       "image/jpeg",
	".svg":        "image/svg+xml",
	".mp4":        "video/mp4",
	".mov":        "video/quicktime",
	".webm":       "video/webm",
	".pdf":        "application/pdf",
	".docx":       "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".pptx":       "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	".xlsx":       "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".xls":        "application/vnd.ms-excel",
	".xlsm":       "application/vnd.ms-excel.sheet.macroEnabled.12",
	".odt":        "application/vnd.oasis.opendocument.text",
	".fodt":       "application/vnd.oasis.opendocument.text-flat-xml",
	".ods":        "application/vnd.oasis.opendocument.spreadsheet",
	".fods":       "application/vnd.oasis.opendocument.spreadsheet-flat-xml",
	".odp":        "application/vnd.oasis.opendocument.presentation",
	".fodp":       "application/vnd.oasis.opendocument.presentation-flat-xml",
	".odg":        "application/vnd.oasis.opendocument.graphics",
	".fodg":       "application/vnd.oasis.opendocument.graphics-flat-xml",
	".odf":        "application/vnd.oasis.opendocument.formula",
	".rtf":        "application/rtf",
	".doc":        "application/msword",
	".txt":        "text/plain",
	".md":         "text/markdown",
	".copilotmd":  "text/markdown",
	".csv":        "text/csv",
	".tsv":        "text/tab-separated-values",
	".log":        "text/plain",
	".json":       "application/json",
	".jsonc":      "application/json",
	".c":          "text/x-c",
	".cs":         "text/plain",
	".cpp":        "text/x-c++",
	".css":        "text/css",
	".drawio":     "application/xml",
	".dmp":        "application/octet-stream",
	".html":       "text/html",
	".htm":        "text/html",
	".java":       "text/x-java-source",
	".js":         "text/javascript",
	".ipynb":      "application/x-ipynb+json",
	".patch":      "text/x-diff",
	".php":        "application/x-httpd-php",
	".cpuprofile": "application/json",
	".pdb":        "application/octet-stream",
	".py":         "text/x-python",
	".sh":         "application/x-sh",
	".sql":        "application/sql",
	".ts":         "text/typescript",
	".tsx":        "text/typescript",
	".xml":        "application/xml",
	".yaml":       "application/yaml",
	".yml":        "application/yaml",
	".zip":        "application/zip",
	".gz":         "application/gzip",
	".tgz":        "application/gzip",
	".debug":      "text/plain",
	".msg":        "application/vnd.ms-outlook",
	".eml":        "message/rfc822",
	".bmp":        "image/bmp",
	".tif":        "image/tiff",
	".tiff":       "image/tiff",
	".mp3":        "audio/mpeg",
	".wav":        "audio/wav",
}

func TestLoadAssetAcceptsEveryGitHubDocumentedFileExtension(t *testing.T) {
	for extension, expectedMediaType := range documentedFileTypes {
		t.Run(extension, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "attachment"+extension)
			writeSupportedTestFile(t, path)

			asset, err := loadAsset(path)
			if err != nil {
				t.Fatal(err)
			}
			if asset.MediaType != expectedMediaType {
				t.Fatalf("media type=%q want=%q", asset.MediaType, expectedMediaType)
			}
		})
	}
}

func TestDocumentedFileSizeCategories(t *testing.T) {
	tests := map[string]int64{
		"image.png":    10 * 1024 * 1024,
		"image.svg":    10 * 1024 * 1024,
		"image.tiff":   10 * 1024 * 1024,
		"video.mp4":    100 * 1024 * 1024,
		"document.pdf": 25 * 1024 * 1024,
		"audio.wav":    25 * 1024 * 1024,
	}
	for name, expected := range tests {
		t.Run(name, func(t *testing.T) {
			_, fileType, err := supportedType(name)
			if err != nil {
				t.Fatal(err)
			}
			if fileType.maxBytes != expected {
				t.Fatalf("limit=%d want=%d", fileType.maxBytes, expected)
			}
		})
	}
}

func TestLoadAssetsDoesNotApplyUndocumentedAggregateSizeLimit(t *testing.T) {
	directory := t.TempDir()
	first := filepath.Join(directory, "first.pdf")
	second := filepath.Join(directory, "second.pdf")
	for _, path := range []string{first, second} {
		content := make([]byte, 13*1024*1024)
		copy(content, "%PDF-1.7\n")
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	assets, err := loadAssets([]string{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 2 {
		t.Fatalf("assets=%d want=2", len(assets))
	}
}

func TestLoadAssetRejectsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on some Windows hosts")
	}
	directory := t.TempDir()
	target := filepath.Join(directory, "target.png")
	link := filepath.Join(directory, "link.png")
	writePNG(t, target)
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	_, err := loadAsset(link)
	if err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("error = %v, want symbolic link rejection", err)
	}
}

func writeSupportedTestFile(t *testing.T, path string) {
	t.Helper()
	extension := strings.ToLower(filepath.Ext(path))
	switch extension {
	case ".png":
		writePNG(t, path)
	case ".jpg", ".jpeg":
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := jpeg.Encode(file, image.NewRGBA(image.Rect(0, 0, 1, 1)), nil); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	case ".gif":
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := gif.Encode(file, image.NewPaletted(image.Rect(0, 0, 1, 1), color.Palette{color.Black}), nil); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	case ".mp4", ".mov":
		if err := os.WriteFile(path, isoBox("ftyp", []byte("isom0000")), 0o600); err != nil {
			t.Fatal(err)
		}
	case ".webm":
		if err := os.WriteFile(path, []byte{0x1a, 0x45, 0xdf, 0xa3, 0x9f}, 0o600); err != nil {
			t.Fatal(err)
		}
	case ".svg":
		if err := os.WriteFile(path, []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), 0o600); err != nil {
			t.Fatal(err)
		}
	case ".pdf":
		if err := os.WriteFile(path, []byte("%PDF-1.7\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	default:
		if err := os.WriteFile(path, []byte("attachment"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestIdentifyMediaRejectsExtensionMismatch(t *testing.T) {
	var content bytes.Buffer
	if err := png.Encode(&content, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}

	_, err := identifyTestMedia("fake.jpg", content.Bytes())
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("error = %v, want mismatch", err)
	}
}

func TestIdentifyMediaAcceptsSupportedVideoContainers(t *testing.T) {
	tests := []struct {
		name      string
		content   []byte
		mediaType string
	}{
		{name: "clip.mp4", content: isoBox("ftyp", []byte("qt  0000")), mediaType: "video/mp4"},
		{name: "clip.mov", content: isoBox("ftyp", []byte("mp420000")), mediaType: "video/quicktime"},
		{name: "leading-free.mov", content: append(isoBox("free", nil), isoBox("ftyp", []byte("isom0000"))...), mediaType: "video/quicktime"},
		{name: "clip.webm", content: []byte{0x1a, 0x45, 0xdf, 0xa3, 0x9f}, mediaType: "video/webm"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mediaType, err := identifyTestMedia(test.name, test.content)
			if err != nil {
				t.Fatal(err)
			}
			if mediaType != test.mediaType {
				t.Fatalf("mediaType = %q", mediaType)
			}
		})
	}
}

func TestIdentifyMediaRejectsMalformedISOBox(t *testing.T) {
	content := isoBox("ftyp", []byte("isom"))
	binary.BigEndian.PutUint32(content[:4], uint32(len(content)+1))

	_, err := identifyTestMedia("clip.mp4", content)
	if err == nil || !strings.Contains(err.Error(), "not a supported ISO media") {
		t.Fatalf("error = %v", err)
	}
}

func identifyTestMedia(name string, content []byte) (string, error) {
	return identifyMediaReader(name, bytes.NewReader(content), int64(len(content)))
}

func isoBox(kind string, payload []byte) []byte {
	box := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(box[:4], uint32(len(box)))
	copy(box[4:8], kind)
	copy(box[8:], payload)
	return box
}
