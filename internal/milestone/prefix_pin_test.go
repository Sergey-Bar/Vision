package milestone_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision/internal/emulator"
	"github.com/Sergey-Bar/vision/internal/milestone"
)

// The two implementations of the vendor's shape must agree about the prefix.
//
// There are two of them: the platform's adapter, which READS the vendor's
// routes, and the POC module's own client, which WRITES them. They are in two
// Go modules, and a Go module boundary is a visibility boundary, so neither can
// import the other's constant and a test that reads both has to read one of
// them as text.
//
// That is not as good as a shared constant, and it is said so here: the
// alternative was a public package in one module for the other to import, and
// that would make the POC's client depend on the platform's adapter -- which
// is the dependency the POC exists to avoid.
//
// What the test buys is the thing that matters: a change to either prefix
// without the other is a FAILING TEST, and a failing test in the POC module
// breaks the POC's own gate, which runs in the same repository. A drift cannot
// reach a release unnoticed.
//
// The POC's prefix is read from the constant that declares it, and the
// emulator's from the platform's route table. Both are the places a reader
// looks when they want to know what the prefix is.
func TestTheTwoImplementationsAgreeOnThePrefix(t *testing.T) {
	pocPrefix := pocPrefixFromSource(t)
	if pocPrefix == "" {
		t.Fatal("the POC module's prefix could not be read from its source")
	}
	if pocPrefix != milestone.APIPrefix {
		t.Fatalf("the adapter's prefix is %q and the POC module's is %q. They are two "+
			"implementations of one vendor's shape, and a disagreement is every call "+
			"answering 404", milestone.APIPrefix, pocPrefix)
	}
	if pocPrefix != emulator.APIPrefix {
		t.Fatalf("the emulator's prefix is %q and the adapter's is %q. The emulator is the "+
			"device the adapter polls, and a disagreement is a quiet site",
			emulator.APIPrefix, milestone.APIPrefix)
	}
}

// pocPrefixFromSource reads the POC module's declared prefix.
func pocPrefixFromSource(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", "poc", "active-monitoring", "internal", "xprotect", "client.go")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the POC module's client could not be read: %v", err)
	}
	// The declaration is a constant assignment, and the test reads THAT rather
	// than any use of it: a use could be a path in a help string.
	match := regexp.MustCompile(`(?m)^\s*APIRestPrefix\s*=\s*"([^"]+)"`).FindSubmatch(raw)
	if match == nil {
		t.Fatalf("the POC module's client no longer declares APIRestPrefix")
	}
	return string(match[1])
}

// TestTheEmulatorsPrefixIsTheDocumentedOne says which spelling this
// repository uses, and why.
//
// The vendor's own documents spell it in upper case, and the platform's route
// table spells it in lower case. The two are the same path on a case-
// insensitive server and DIFFERENT paths on a case-sensitive one, which is
// exactly the kind of difference that works on the developer's machine and
// 404s in production.
//
// The lower-case spelling is the one this repository uses, because the
// platform's route table is the product's own contract and the POC's simulator
// answers to it. The vendor's published spelling is recorded in the POC's
// specification, which is a recorded document and is not edited to match.
func TestTheEmulatorsPrefixIsTheDocumentedOne(t *testing.T) {
	if emulator.APIPrefix != "/api/rest/v1" {
		t.Fatalf("the emulator's prefix is %q, and the product's route table says %q",
			emulator.APIPrefix, milestone.APIPrefix)
	}
	if strings.ContainsAny(emulator.APIPrefix, " \t") {
		t.Error("the prefix has whitespace in it, and a path with whitespace is a path that needs quoting")
	}
}
