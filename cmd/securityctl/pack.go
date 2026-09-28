package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/release"
)

func background() context.Context { return context.Background() }

func marshalJSON(v any) ([]byte, error) { return json.Marshal(v) }

func unmarshalJSON(b []byte, v any) error { return json.Unmarshal(b, v) }

func jsonIndent(dst *[]byte, src []byte) error {
	var out any
	if err := json.Unmarshal(src, &out); err != nil {
		return err
	}
	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	*dst = body
	return nil
}

// packDirectory writes a gzipped tar of a directory tree, returning its size.
//
// Every path is validated on the way in, and a symlink is refused rather than
// followed. A backup that follows a symlink out of the evidence directory would
// copy whatever it points at, which may be the whole filesystem and is never what
// an operator asked for.
func packDirectory(root, dest string) (int64, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return 0, err
	}
	info, err := os.Stat(absRoot)
	if err != nil {
		return 0, err
	}
	if !info.IsDir() {
		return 0, &notADirectory{root}
	}

	f, err := os.Create(dest)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	var written int64
	err = filepath.Walk(absRoot, func(path string, entry os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == absRoot {
			return nil
		}
		rel, err := filepath.Rel(absRoot, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)

		if entry.Mode()&os.ModeSymlink != 0 {
			return &symlinkInTree{name}
		}
		if entry.IsDir() {
			return tw.WriteHeader(&tar.Header{
				Name: name + "/", Typeflag: tar.TypeDir, Mode: 0o750,
				ModTime: time.Unix(0, 0).UTC(),
			})
		}
		if !entry.Mode().IsRegular() {
			// A socket, device or fifo in an evidence tree is not evidence and
			// archiving it would hang the backup.
			return nil
		}
		if err := release.ValidateFilePath(name); err != nil {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Typeflag: tar.TypeReg, Mode: 0o640,
			Size: int64(len(body)), ModTime: time.Unix(0, 0).UTC(),
		}); err != nil {
			return err
		}
		if _, err := tw.Write(body); err != nil {
			return err
		}
		written += int64(len(body))
		return nil
	})
	if err != nil {
		_ = tw.Close()
		_ = gz.Close()
		_ = os.Remove(dest)
		return 0, err
	}
	if err := tw.Close(); err != nil {
		_ = gz.Close()
		return 0, err
	}
	if err := gz.Close(); err != nil {
		return 0, err
	}
	if err := f.Sync(); err != nil {
		return 0, err
	}
	return written, nil
}

type notADirectory struct{ path string }

func (e *notADirectory) Error() string {
	return "lifecycle: " + e.path + " is not a directory"
}

type symlinkInTree struct{ name string }

func (e *symlinkInTree) Error() string {
	return "lifecycle: " + e.name + " is a symlink. A backup that follows a link out of the " +
		"evidence directory would copy whatever it points at, which is never what the " +
		"operator asked for. Replace it with the file it names, or exclude it deliberately"
}

var (
	_ = io.EOF
	_ = strings.TrimSpace
)
