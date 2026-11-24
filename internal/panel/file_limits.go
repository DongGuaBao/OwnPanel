package panel

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"net/http"
)

const (
	maxPreviewBytes  = 2 << 20
	maxUploadBytes   = 256 << 20
	maxZIPBytes      = 128 << 20
	maxExpandedBytes = 1 << 30
	maxZIPEntries    = 10000
)

// Shared by Agent requests and by all SSH nodes in a center.
var largeFileOperations = make(chan struct{}, 4)

func acquireFileSlot() bool {
	select {
	case largeFileOperations <- struct{}{}:
		return true
	default:
		return false
	}
}
func releaseFileSlot() { <-largeFileOperations }

func previewFile(w http.ResponseWriter, r io.Reader, size int64) {
	if size > maxPreviewBytes {
		fail(w, 413, fmt.Errorf("预览最多支持 2 MiB，请下载文件"))
		return
	}
	if !acquireFileSlot() {
		fail(w, 429, fmt.Errorf("文件任务繁忙"))
		return
	}
	defer releaseFileSlot()
	b, err := io.ReadAll(io.LimitReader(r, maxPreviewBytes+1))
	if err != nil {
		fail(w, 500, err)
		return
	}
	if len(b) > maxPreviewBytes {
		fail(w, 413, fmt.Errorf("预览最多支持 2 MiB，请下载文件"))
		return
	}
	respond(w, map[string]string{"content": string(b)})
}

func parseUpload(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			fail(w, 413, fmt.Errorf("上传请求超过 256 MiB"))
		} else {
			fail(w, 400, err)
		}
		return false
	}
	return true
}

// Check the error/CRC at EOF, including for an entry declared to be empty.
func copyZIPEntry(out io.Writer, in io.Reader, size uint64) error {
	if size > maxExpandedBytes {
		return fmt.Errorf("ZIP 展开大小超过上限")
	}
	n, err := io.Copy(out, io.LimitReader(in, int64(size)+1))
	if err != nil {
		return err
	}
	if uint64(n) != size {
		return fmt.Errorf("ZIP 条目大小与声明不符")
	}
	return nil
}

// zip.NewReader parses the central directory before callers can count entries.
// Bound that initial metadata read as well as the eventual extracted payload.
type zipMetadataReader struct {
	io.ReaderAt
	remaining int64
}

func (r *zipMetadataReader) ReadAt(b []byte, off int64) (int, error) {
	if r.remaining >= 0 {
		if int64(len(b)) > r.remaining {
			return 0, fmt.Errorf("ZIP 索引超过 4 MiB 读取预算")
		}
		r.remaining -= int64(len(b))
	}
	return r.ReaderAt.ReadAt(b, off)
}

func openLimitedZIP(source io.ReaderAt, size int64) (*zip.Reader, error) {
	if size > maxZIPBytes {
		return nil, fmt.Errorf("ZIP 超过 128 MiB")
	}
	index := &zipMetadataReader{ReaderAt: source, remaining: 4 << 20}
	z, err := zip.NewReader(index, size)
	if err != nil {
		return nil, err
	}
	index.remaining = -1
	if err := validateZIPEntries(z.File); err != nil {
		return nil, err
	}
	return z, nil
}
