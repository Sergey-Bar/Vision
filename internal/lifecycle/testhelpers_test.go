package lifecycle_test

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"testing"
	"time"
)

// packTar writes a tar.gz with the given contents, for the tamper cases. The
// manifest is not involved: these exist to prove extraction and verification
// re-check the archive rather than trusting the manifest beside it.
func packTar(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o644, Size: int64(len(body)),
			Typeflag: tar.TypeReg, ModTime: time.Unix(0, 0).UTC(),
		}); err != nil {
			t.Fatalf("header %s: %v", name, err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	_ = tw.Close()
	_ = gz.Close()
}
