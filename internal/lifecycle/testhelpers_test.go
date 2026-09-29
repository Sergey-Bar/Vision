package lifecycle_test

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"testing"
	"time"
)

// emptySHA256 is the hash of zero bytes, which is what a link entry's empty
// body hashes to. The manifest lists it so the entry is "listed" and the refusal
// has to come from the type check rather than from the unlisted-file check -
// which is the point: both are refusals, and only one of them is the control
// under test.
const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// packTar writes a tar.gz with the given contents, for the tamper cases. The
// manifest is not involved: these exist to prove extraction and verification
// re-check the archive rather than trusting the manifest beside it.
func packTar(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer func() { _ = f.Close() }()
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

// tarEntry is one entry for packTarEntries, including the ones packTar cannot
// express: a symlink, a hard link, a device node. The tamper cases need to
// produce a well-formed archive of a kind a real attacker would, and a helper
// that can only write regular files cannot test the refusal of the others.
type tarEntry struct {
	Name string
	Flag byte
	Link string
	Body string
}

// packTarEntries writes a tar.gz with an explicit entry type per entry.
func packTarEntries(t *testing.T, path string, entries []tarEntry) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer func() { _ = f.Close() }()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		if err := tw.WriteHeader(&tar.Header{
			Name: e.Name, Mode: 0o644, Size: int64(len(e.Body)),
			Typeflag: e.Flag, Linkname: e.Link, ModTime: time.Unix(0, 0).UTC(),
		}); err != nil {
			t.Fatalf("header %s: %v", e.Name, err)
		}
		if e.Body != "" {
			if _, err := tw.Write([]byte(e.Body)); err != nil {
				t.Fatalf("write %s: %v", e.Name, err)
			}
		}
	}
	_ = tw.Close()
	_ = gz.Close()
}
