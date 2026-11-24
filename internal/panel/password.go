package panel

import (
	"os"
	"path/filepath"
)

func replacePasswordHash(path string, hash []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".password-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := file.Write(hash); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
