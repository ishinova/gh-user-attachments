package userattachments

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	minFiles          = 2
	maxFiles          = 10
	maxImageFileBytes = 10 * 1024 * 1024
	maxVideoFileBytes = 100 * 1024 * 1024
	maxOtherFileBytes = 25 * 1024 * 1024
)

type supportedFileType struct {
	mediaType string
	maxBytes  int64
}

// supportedFileTypes mirrors GitHub's Attaching files documentation as verified
// on 2026-07-23:
// https://docs.github.com/en/get-started/writing-on-github/working-with-advanced-formatting/attaching-files
var supportedFileTypes = map[string]supportedFileType{
	".png":        {mediaType: "image/png", maxBytes: maxImageFileBytes},
	".gif":        {mediaType: "image/gif", maxBytes: maxImageFileBytes},
	".jpg":        {mediaType: "image/jpeg", maxBytes: maxImageFileBytes},
	".jpeg":       {mediaType: "image/jpeg", maxBytes: maxImageFileBytes},
	".svg":        {mediaType: "image/svg+xml", maxBytes: maxImageFileBytes},
	".mp4":        {mediaType: "video/mp4", maxBytes: maxVideoFileBytes},
	".mov":        {mediaType: "video/quicktime", maxBytes: maxVideoFileBytes},
	".webm":       {mediaType: "video/webm", maxBytes: maxVideoFileBytes},
	".pdf":        {mediaType: "application/pdf", maxBytes: maxOtherFileBytes},
	".docx":       {mediaType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document", maxBytes: maxOtherFileBytes},
	".pptx":       {mediaType: "application/vnd.openxmlformats-officedocument.presentationml.presentation", maxBytes: maxOtherFileBytes},
	".xlsx":       {mediaType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", maxBytes: maxOtherFileBytes},
	".xls":        {mediaType: "application/vnd.ms-excel", maxBytes: maxOtherFileBytes},
	".xlsm":       {mediaType: "application/vnd.ms-excel.sheet.macroEnabled.12", maxBytes: maxOtherFileBytes},
	".odt":        {mediaType: "application/vnd.oasis.opendocument.text", maxBytes: maxOtherFileBytes},
	".fodt":       {mediaType: "application/vnd.oasis.opendocument.text-flat-xml", maxBytes: maxOtherFileBytes},
	".ods":        {mediaType: "application/vnd.oasis.opendocument.spreadsheet", maxBytes: maxOtherFileBytes},
	".fods":       {mediaType: "application/vnd.oasis.opendocument.spreadsheet-flat-xml", maxBytes: maxOtherFileBytes},
	".odp":        {mediaType: "application/vnd.oasis.opendocument.presentation", maxBytes: maxOtherFileBytes},
	".fodp":       {mediaType: "application/vnd.oasis.opendocument.presentation-flat-xml", maxBytes: maxOtherFileBytes},
	".odg":        {mediaType: "application/vnd.oasis.opendocument.graphics", maxBytes: maxOtherFileBytes},
	".fodg":       {mediaType: "application/vnd.oasis.opendocument.graphics-flat-xml", maxBytes: maxOtherFileBytes},
	".odf":        {mediaType: "application/vnd.oasis.opendocument.formula", maxBytes: maxOtherFileBytes},
	".rtf":        {mediaType: "application/rtf", maxBytes: maxOtherFileBytes},
	".doc":        {mediaType: "application/msword", maxBytes: maxOtherFileBytes},
	".txt":        {mediaType: "text/plain", maxBytes: maxOtherFileBytes},
	".md":         {mediaType: "text/markdown", maxBytes: maxOtherFileBytes},
	".copilotmd":  {mediaType: "text/markdown", maxBytes: maxOtherFileBytes},
	".csv":        {mediaType: "text/csv", maxBytes: maxOtherFileBytes},
	".tsv":        {mediaType: "text/tab-separated-values", maxBytes: maxOtherFileBytes},
	".log":        {mediaType: "text/plain", maxBytes: maxOtherFileBytes},
	".json":       {mediaType: "application/json", maxBytes: maxOtherFileBytes},
	".jsonc":      {mediaType: "application/json", maxBytes: maxOtherFileBytes},
	".c":          {mediaType: "text/x-c", maxBytes: maxOtherFileBytes},
	".cs":         {mediaType: "text/plain", maxBytes: maxOtherFileBytes},
	".cpp":        {mediaType: "text/x-c++", maxBytes: maxOtherFileBytes},
	".css":        {mediaType: "text/css", maxBytes: maxOtherFileBytes},
	".drawio":     {mediaType: "application/xml", maxBytes: maxOtherFileBytes},
	".dmp":        {mediaType: "application/octet-stream", maxBytes: maxOtherFileBytes},
	".html":       {mediaType: "text/html", maxBytes: maxOtherFileBytes},
	".htm":        {mediaType: "text/html", maxBytes: maxOtherFileBytes},
	".java":       {mediaType: "text/x-java-source", maxBytes: maxOtherFileBytes},
	".js":         {mediaType: "text/javascript", maxBytes: maxOtherFileBytes},
	".ipynb":      {mediaType: "application/x-ipynb+json", maxBytes: maxOtherFileBytes},
	".patch":      {mediaType: "text/x-diff", maxBytes: maxOtherFileBytes},
	".php":        {mediaType: "application/x-httpd-php", maxBytes: maxOtherFileBytes},
	".cpuprofile": {mediaType: "application/json", maxBytes: maxOtherFileBytes},
	".pdb":        {mediaType: "application/octet-stream", maxBytes: maxOtherFileBytes},
	".py":         {mediaType: "text/x-python", maxBytes: maxOtherFileBytes},
	".sh":         {mediaType: "application/x-sh", maxBytes: maxOtherFileBytes},
	".sql":        {mediaType: "application/sql", maxBytes: maxOtherFileBytes},
	".ts":         {mediaType: "text/typescript", maxBytes: maxOtherFileBytes},
	".tsx":        {mediaType: "text/typescript", maxBytes: maxOtherFileBytes},
	".xml":        {mediaType: "application/xml", maxBytes: maxOtherFileBytes},
	".yaml":       {mediaType: "application/yaml", maxBytes: maxOtherFileBytes},
	".yml":        {mediaType: "application/yaml", maxBytes: maxOtherFileBytes},
	".zip":        {mediaType: "application/zip", maxBytes: maxOtherFileBytes},
	".gz":         {mediaType: "application/gzip", maxBytes: maxOtherFileBytes},
	".tgz":        {mediaType: "application/gzip", maxBytes: maxOtherFileBytes},
	".debug":      {mediaType: "text/plain", maxBytes: maxOtherFileBytes},
	".msg":        {mediaType: "application/vnd.ms-outlook", maxBytes: maxOtherFileBytes},
	".eml":        {mediaType: "message/rfc822", maxBytes: maxOtherFileBytes},
	".bmp":        {mediaType: "image/bmp", maxBytes: maxImageFileBytes},
	".tif":        {mediaType: "image/tiff", maxBytes: maxImageFileBytes},
	".tiff":       {mediaType: "image/tiff", maxBytes: maxImageFileBytes},
	".mp3":        {mediaType: "audio/mpeg", maxBytes: maxOtherFileBytes},
	".wav":        {mediaType: "audio/wav", maxBytes: maxOtherFileBytes},
}

type localAsset struct {
	Path      string
	Name      string
	MediaType string
	Size      int64
	fileInfo  os.FileInfo
}

type uploadAsset struct {
	Name      string
	MediaType string
	Content   []byte
}

func loadAssets(paths []string) ([]localAsset, error) {
	assets := make([]localAsset, 0, len(paths))
	seenPaths := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", path, err)
		}
		if _, exists := seenPaths[absolute]; exists {
			return nil, fmt.Errorf("duplicate --file: %s", path)
		}
		seenPaths[absolute] = struct{}{}
		asset, err := loadAsset(absolute)
		if err != nil {
			return nil, err
		}
		for _, previous := range assets {
			if previous.fileInfo != nil && asset.fileInfo != nil && os.SameFile(previous.fileInfo, asset.fileInfo) {
				return nil, fmt.Errorf("duplicate --file: %s", path)
			}
		}
		assets = append(assets, asset)
	}
	return assets, nil
}

func loadAsset(path string) (localAsset, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return localAsset{}, fmt.Errorf("inspect %s: %w", path, err)
	}
	if before.Mode()&os.ModeSymlink != 0 {
		return localAsset{}, fmt.Errorf("refusing symbolic link: %s", path)
	}
	if !before.Mode().IsRegular() {
		return localAsset{}, fmt.Errorf("not a regular file: %s", path)
	}
	_, fileType, err := supportedType(path)
	if err != nil {
		return localAsset{}, err
	}
	if before.Size() > fileType.maxBytes {
		return localAsset{}, fmt.Errorf("%s is %d bytes; maximum is %d", path, before.Size(), fileType.maxBytes)
	}

	file, err := os.Open(path)
	if err != nil {
		return localAsset{}, fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return localAsset{}, fmt.Errorf("inspect opened file %s: %w", path, err)
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return localAsset{}, fmt.Errorf("file changed while opening: %s", path)
	}
	name := filepath.Base(path)
	mediaType, err := identifyMediaReader(name, file, opened.Size())
	if err != nil {
		return localAsset{}, err
	}
	after, err := file.Stat()
	if err != nil {
		return localAsset{}, fmt.Errorf("reinspect %s: %w", path, err)
	}
	if after.Size() != opened.Size() || after.ModTime() != opened.ModTime() {
		return localAsset{}, fmt.Errorf("file changed while reading: %s", path)
	}

	return localAsset{
		Path:      path,
		Name:      sanitizeLabel(name),
		MediaType: mediaType,
		Size:      after.Size(),
		fileInfo:  after,
	}, nil
}

func materializeAsset(asset localAsset) (uploadAsset, error) {
	pathInfo, err := os.Lstat(asset.Path)
	if err != nil {
		return uploadAsset{}, fmt.Errorf("reinspect %s: %w", asset.Path, err)
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || asset.fileInfo == nil || !os.SameFile(asset.fileInfo, pathInfo) {
		return uploadAsset{}, fmt.Errorf("file changed after validation: %s", asset.Path)
	}
	file, err := os.Open(asset.Path)
	if err != nil {
		return uploadAsset{}, fmt.Errorf("reopen %s: %w", asset.Path, err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return uploadAsset{}, fmt.Errorf("inspect reopened file %s: %w", asset.Path, err)
	}
	if !opened.Mode().IsRegular() || !os.SameFile(asset.fileInfo, opened) ||
		opened.Size() != asset.Size || opened.ModTime() != asset.fileInfo.ModTime() {
		return uploadAsset{}, fmt.Errorf("file changed after validation: %s", asset.Path)
	}
	content, err := io.ReadAll(io.LimitReader(file, asset.Size+1))
	if err != nil {
		return uploadAsset{}, fmt.Errorf("read %s: %w", asset.Path, err)
	}
	if int64(len(content)) != asset.Size {
		return uploadAsset{}, fmt.Errorf("file changed while reading: %s", asset.Path)
	}
	after, err := file.Stat()
	if err != nil {
		return uploadAsset{}, fmt.Errorf("reinspect %s: %w", asset.Path, err)
	}
	if after.Size() != asset.Size || after.ModTime() != opened.ModTime() {
		return uploadAsset{}, fmt.Errorf("file changed while reading: %s", asset.Path)
	}
	mediaType, err := identifyMediaReader(asset.Name, bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return uploadAsset{}, err
	}
	if mediaType != asset.MediaType {
		return uploadAsset{}, fmt.Errorf("file type changed after validation: %s", asset.Path)
	}
	return uploadAsset{Name: asset.Name, MediaType: asset.MediaType, Content: content}, nil
}

func identifyMediaReader(name string, content io.ReaderAt, size int64) (string, error) {
	extension, fileType, err := supportedType(name)
	if err != nil {
		return "", err
	}
	switch extension {
	case ".png", ".jpg", ".jpeg", ".gif":
		_, format, err := image.DecodeConfig(io.NewSectionReader(content, 0, size))
		if err != nil {
			return "", fmt.Errorf("%s is not a valid %s image", name, extension)
		}
		switch {
		case extension == ".png" && format == "png":
			return fileType.mediaType, nil
		case (extension == ".jpg" || extension == ".jpeg") && format == "jpeg":
			return fileType.mediaType, nil
		case extension == ".gif" && format == "gif":
			return fileType.mediaType, nil
		default:
			return "", fmt.Errorf("%s extension does not match its image content", name)
		}
	case ".mp4", ".mov":
		if !containsISOFileTypeBoxReader(content, size) {
			return "", fmt.Errorf("%s is not a supported ISO media file", name)
		}
		return fileType.mediaType, nil
	case ".webm":
		signature := make([]byte, 4)
		if size < int64(len(signature)) {
			return "", fmt.Errorf("%s is not a WebM container", name)
		}
		if _, err := content.ReadAt(signature, 0); err != nil || !bytes.Equal(signature, []byte{0x1a, 0x45, 0xdf, 0xa3}) {
			return "", fmt.Errorf("%s is not a WebM container", name)
		}
		return fileType.mediaType, nil
	default:
		return fileType.mediaType, nil
	}
}

func supportedType(name string) (string, supportedFileType, error) {
	extension := strings.ToLower(filepath.Ext(name))
	fileType, ok := supportedFileTypes[extension]
	if !ok {
		return "", supportedFileType{}, fmt.Errorf("unsupported file type %q; see GitHub's Attaching files documentation", extension)
	}
	return extension, fileType, nil
}

func containsISOFileTypeBoxReader(content io.ReaderAt, contentSize int64) bool {
	header := make([]byte, 16)
	for offset := int64(0); offset+8 <= contentSize; {
		if _, err := content.ReadAt(header[:8], offset); err != nil {
			return false
		}
		size := uint64(binary.BigEndian.Uint32(header[:4]))
		headerSize := uint64(8)
		switch size {
		case 1:
			if offset+16 > contentSize {
				return false
			}
			if _, err := content.ReadAt(header[8:16], offset+8); err != nil {
				return false
			}
			size = binary.BigEndian.Uint64(header[8:16])
			headerSize = 16
		case 0:
			size = uint64(contentSize - offset)
		}
		if size < headerSize || size > uint64(contentSize-offset) {
			return false
		}
		if bytes.Equal(header[4:8], []byte("ftyp")) {
			return size >= headerSize+4
		}
		offset += int64(size)
	}
	return false
}

func sanitizeLabel(value string) string {
	value = strings.Map(func(character rune) rune {
		if character == '\n' || character == '\r' || character == '\t' || character < 0x20 || character == 0x7f {
			return ' '
		}
		return character
	}, value)
	return strings.TrimSpace(strings.Join(strings.Fields(value), " "))
}
