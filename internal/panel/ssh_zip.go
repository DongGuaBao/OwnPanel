package panel

import (
	"archive/zip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/pkg/sftp"
)

func validateZIPEntries(entries []*zip.File) error {
	if len(entries) > maxZIPEntries {
		return fmt.Errorf("ZIP 最多支持 10000 个条目")
	}
	var total uint64
	for _, entry := range entries {
		if entry.UncompressedSize64 > maxExpandedBytes-total {
			return fmt.Errorf("ZIP 展开大小超过 1 GiB")
		}
		total += entry.UncompressedSize64
		name := strings.TrimSuffix(entry.Name, "/")
		if name == "." || !fs.ValidPath(name) || strings.ContainsAny(name, "\\\x00") || (!entry.Mode().IsDir() && !entry.Mode().IsRegular()) {
			return fmt.Errorf("压缩包含不安全路径或文件类型")
		}

	}
	return nil
}

func extractSSHZIP(client *sftp.Client, source, target string) error {
	if _, err := client.Lstat(target); !os.IsNotExist(err) {
		return fmt.Errorf("请解压到尚不存在的目录")
	}
	info, err := client.Lstat(source)
	if err != nil {
		return err
	}
	if !acquireFileSlot() {
		return fmt.Errorf("文件任务繁忙")
	}
	defer releaseFileSlot()
	if info.Size() > maxZIPBytes {
		return fmt.Errorf("ZIP 超过 128 MiB")
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("仅接受普通 ZIP 文件")
	}
	input, err := client.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	archive, err := openLimitedZIP(input, info.Size())
	if err != nil {
		return err
	}
	stage := path.Join(path.Dir(target), ".ownpanel-zip-"+token()[:16])
	if err = client.Mkdir(stage); err != nil {
		return err
	}
	defer client.RemoveAll(stage)
	if err = client.Chmod(stage, 0700); err != nil {
		return err
	}
	for _, entry := range archive.File {
		destination := path.Join(stage, entry.Name)
		if entry.Mode().IsDir() {
			if err = client.MkdirAll(destination); err != nil {
				return err
			}
			continue
		}
		if err = client.MkdirAll(path.Dir(destination)); err != nil {
			return err
		}
		if err = extractSSHZIPFile(client, entry, destination); err != nil {
			return err
		}
	}
	return client.Rename(stage, target)
}

func extractSSHZIPFile(client *sftp.Client, entry *zip.File, destination string) error {
	in, err := entry.Open()
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := client.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY)
	if err != nil {
		return err
	}
	err = copyZIPEntry(out, in, entry.UncompressedSize64)
	closeErr := out.Close()
	if err != nil {
		return err
	}

	return closeErr
}

func compressSSHZIP(client *sftp.Client, source, target string) error {
	if source == target || strings.HasPrefix(target, source+"/") {
		return fmt.Errorf("压缩目标不能位于源目录内")
	}
	if _, err := client.Lstat(target); !os.IsNotExist(err) {
		return fmt.Errorf("压缩目标已存在或不可访问")
	}
	stage := path.Join(path.Dir(target), ".ownpanel-zip-"+token()[:16])
	out, err := client.OpenFile(stage, os.O_CREATE|os.O_EXCL|os.O_WRONLY)
	if err != nil {
		return err
	}
	defer client.Remove(stage)
	archive := zip.NewWriter(out)
	writeErr := writeSSHZIP(client, archive, source)
	zipErr := archive.Close()
	closeErr := out.Close()
	for _, err := range []error{writeErr, zipErr, closeErr} {
		if err != nil {
			return err
		}
	}
	return client.Rename(stage, target)
}

func writeSSHZIP(client *sftp.Client, archive *zip.Writer, source string) error {
	walk := client.Walk(source)
	for walk.Step() {
		if err := walk.Err(); err != nil {
			return err
		}
		info := walk.Stat()
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("仅支持普通文件和目录")
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = strings.TrimPrefix(walk.Path(), strings.TrimSuffix(path.Dir(source), "/")+"/")
		if info.IsDir() {
			header.Name += "/"
		} else {
			header.Method = zip.Deflate
		}
		writer, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}
		if info.IsDir() {
			continue
		}
		input, err := client.Open(walk.Path())
		if err != nil {
			return err
		}
		_, err = io.Copy(writer, input)
		closeErr := input.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}

	}
	return nil
}
