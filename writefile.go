package main

import (
	"os"
	"path/filepath"
)

// writeFile replaces the file that path resolves to with a renamed temporary
// file in the same directory, so the file is never left half written. A file
// that is not writable is refused. A file with other hard links or another
// owner, or one whose directory takes no temporary file, is written in place.
func writeFile(path string, content []byte) error {
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(target, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	f.Close()
	info, err := os.Stat(target)
	if err != nil {
		return err
	}
	if !replaceable(info) {
		return os.WriteFile(target, content, info.Mode().Perm())
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".*")
	if err != nil {
		return os.WriteFile(target, content, info.Mode().Perm())
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), target)
}
