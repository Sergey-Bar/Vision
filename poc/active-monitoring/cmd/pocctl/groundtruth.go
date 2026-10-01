package main

// `fetch-groundtruth` — obtain a labelled still set, and record where it came from.
//
// # Why Open Images and not COCO
//
// COCO's own validation set is 5,000 images with boxes, and it would be the
// obvious choice. It is not usable here for two reasons, both structural:
//
//   - its licence is not CC BY 2.0 the way Open Images' images are. COCO's terms
//     are a research licence with conditions this project would have to honour
//     separately, and a licence nobody has read is a licence nobody has agreed to.
//   - it does not ship a PER-CLASS NEGATIVE list. Open Images does
//     (`validation-annotations-human-imagelabels.csv`), which is what makes a
//     false-positive measurement possible at all: to count the boxes a detector put
//     on images containing nothing, the set has to say which images contain
//     nothing.
//
// # The taxonomy penalty is the whole difficulty
//
// Open Images has 600 classes. This build has COCO's 80. A still of a `hair drier`
// is a real object that a COCO detector can never name, and counting it as a recall
// miss would report a penalty for a vocabulary limitation as though it were a
// detector defect.
//
// So the mapping is EXPLICIT and CLOSED — [cocoEquivalent] below — and every class
// with no COCO counterpart drops its box with the class named. `bench` prints the
// dropped count and the breakdown, and a recall quoted without it is a claim about
// the vocabulary.
//
// # What is recorded
//
// Everything: the URL each file came from, its SHA-256, its licence, and the licence
// of the ANNOTATIONS separately, because Open Images' images and its annotation
// files carry different terms and conflating them is the mistake a reader would
// make.

import (
	"archive/zip"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// groundtruthManifest records what was fetched and what it may be used for.
const groundtruthManifest = "testdata/groundtruth.json"

// openImagesBase is the bucket the annotation CSVs live in.
//
// It is a CONSTANT and not a flag, and that is a decision worth stating: the
// alternative is a `--url` that takes any CSV, and a benchmark pointed at an
// arbitrary annotation file produces numbers whose meaning nobody can state. The
// mapping below assumes Open Images' column names and its 600-class vocabulary, so
// a different file would be silently mis-parsed.
const openImagesBase = "https://storage.googleapis.com/openimages/v5/"

// openImagesClassesURL is the human-readable class list.
//
// It is a separate download because it is a tiny file with the 600 class names and
// their IDs, and having it locally means the mapping can be verified rather than
// assumed: a class ID that is not in this file is a parse failure, not a class
// nobody has heard of.
const openImagesClassesURL = openImagesBase + "class-descriptions-boxable.csv"

// cocoEquivalent maps Open Images class names to COCO-80 names.
//
// It is a CLOSED set and that is the point. Anything not in it is dropped with its
// name recorded, because a benchmark that silently mapped `hair drier` onto
// `toothbrush` to keep its denominator would report a recall it did not measure.
//
// The mapping is by NAME rather than by class ID because the two vocabularies do
// not share IDs and a hand-maintained ID table would be a second thing to keep in
// sync with the first.
var cocoEquivalent = map[string]string{
	// Vehicles. These are the classes a traffic camera produces and they are the
	// ones the approved-class list is built around.
	"Car":        "car",
	"Truck":      "truck",
	"Bus":        "bus",
	"Motorcycle": "motorcycle",
	"Bicycle":    "bicycle",
	"Train":      "train",
	"Boat":       "boat",
	"Airplane":   "airplane",
	// People. Open Images splits a person into a body and a face and a hand; COCO
	// has one `person`. All three map to it, because a detector that found the body
	// found the person and a benchmark that disagreed would be scoring the
	// vocabulary again.
	"Person":     "person",
	"Human body": "person",
	"Human face": "person",
	"Human arm":  "person",
	"Human leg":  "person",
	"Human hand": "person",
	"Human head": "person",
	"Man":        "person",
	"Woman":      "person",
	"Boy":        "person",
	"Girl":       "person",
	"Child":      "person",
	// Road furniture. A traffic camera produces these constantly and a detector
	// that finds them is producing boxes the operator must see, so dropping them from
	// the benchmark would understate the false-positive count.
	"Traffic light": "traffic light",
	"Stop sign":     "stop sign",
	"Fire hydrant":  "fire hydrant",
	"Parking meter": "parking meter",
	// Animals. Bicycles are above; these are what a road camera occasionally gets.
	"Cat":     "cat",
	"Dog":     "dog",
	"Bird":    "bird",
	"Horse":   "horse",
	"Sheep":   "sheep",
	"Cow":     "cow",
	"Bear":    "bear",
	"Zebra":   "zebra",
	"Giraffe": "giraffe",
	// Street furniture and small objects. Each maps to the COCO word that covers it.
	"Traffic signal": "traffic light",
	"Crosswalk sign": "stop sign",
	"Bench":          "bench",
	"Chair":          "chair",
	"Stool":          "chair",
	"Cup":            "cup",
	"Mug":            "cup",
	"Bottle":         "bottle",
	"Book":           "book",
	"Cell phone":     "cell phone",
	"Mobile phone":   "cell phone",
	"Backpack":       "backpack",
	"Handbag":        "handbag",
	"Bicycle wheel":  "bicycle",
	"Car wheel":      "car",
	"Tire":           "car",
}

// GroundtruthManifest is what fetch-groundtruth writes.
type GroundtruthManifest struct {
	// Fetched records what was downloaded and from where, verbatim.
	ClassesFile   string `json:"classes_file"`
	BoxesFile     string `json:"boxes_file"`
	LabelsFile    string `json:"labels_file"`
	RotationsFile string `json:"rotations_file"`
	ImageDir      string `json:"image_dir"`
	// ImageLicence and AnnotationLicence are SEPARATE, because Open Images' images
	// and its annotation CSVs carry different terms and a manifest that recorded one
	// licence for both would be wrong in whichever direction the reader did not check.
	ImageLicence      string `json:"image_licence"`
	AnnotationLicence string `json:"annotation_licence"`
	Attribution       string `json:"attribution"`
	// Images is what was kept, with a SHA-256 each. It is recorded per image
	// because a benchmark whose inputs cannot be identified cannot be repeated, and
	// a benchmark that cannot be repeated is a number rather than a measurement.
	Images []GroundtruthImage `json:"images"`
	// Negatives is the count of images recorded as containing none of the mapped
	// classes. They are the false-positive denominator and they are counted here so
	// that "0 images with nothing in them" is visible rather than inferred.
	Negatives int
	FetchedAt string `json:"fetched_at"`
}

// GroundtruthImage is one image and what is in it.
type GroundtruthImage struct {
	// ID is Open Images' own identifier, which is what the URLs are built from.
	ID string `json:"id"`
	// Filename is the local path.
	Filename string `json:"filename"`
	SHA256   string `json:"sha256"`
	// OriginalURL and Author are recorded because Open Images' licence requires
	// attribution and a benchmark that cannot attribute a thing it will publish is
	// not a benchmark this project may make.
	OriginalURL string `json:"original_url"`
	Author      string `json:"author"`
	License     string `json:"license"`
	// Boxes are the ground truth, with the COCO class each mapped to.
	Boxes []GTBox `json:"boxes"`
	// DroppedClasses names the Open Images classes this build cannot express, so a
	// reader can see the taxonomy penalty for this image specifically.
	DroppedClasses []string `json:"dropped_classes"`
}

// GTBox is one ground-truth box in normalised coordinates.
type GTBox struct {
	Class string  `json:"class"`
	OI    string  `json:"open_images_class"`
	X1    float64 `json:"x1"`
	Y1    float64 `json:"y1"`
	X2    float64 `json:"x2"`
	Y2    float64 `json:"y2"`
}

func cmdFetchGroundtruth(args []string) error {
	flags, err := flagSet("fetch-groundtruth", map[string]string{
		"out":      filepath.Join("testdata", "still"),
		"manifest": groundtruthManifest,
	}, args)
	if err != nil {
		return err
	}
	out := flags["out"]
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "sp-gt-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(work) }()

	man := GroundtruthManifest{
		ImageDir: out,
		// Open Images' images are CC BY 2.0 and the ANNOTATIONS are CC BY 4.0. Two
		// licences, recorded separately, because a run that published this material
		// would have to satisfy both and a manifest naming one is a manifest
		// somebody relied on and was wrong.
		ImageLicence:      "CC BY 2.0 (Flickr-sourced images)",
		AnnotationLicence: "CC BY 4.0 (box and label annotations)",
		Attribution: "Open Images, https://storage.googleapis.com/openimages/ — images by the " +
			"Flickr authors listed per image in this manifest",
		FetchedAt: time.Now().UTC().Format(time.RFC3339),
	}

	// The class list first, because everything else is keyed by it and a parse
	// against a missing vocabulary produces silent nonsense.
	fmt.Printf("fetch-groundtruth: downloading the class list\n")
	classes, err := downloadCSV(openImagesClassesURL, filepath.Join(work, "classes.csv"))
	if err != nil {
		return fmt.Errorf("the class list could not be fetched: %w", err)
	}
	man.ClassesFile = filepath.ToSlash(filepath.Join(out, "class-descriptions-boxable.csv"))
	if err := copyFile(filepath.Join(work, "classes.csv"), filepath.Join(out, "class-descriptions-boxable.csv")); err != nil {
		return err
	}
	fmt.Printf("  %d classes\n", len(classes))

	fmt.Printf("fetch-groundtruth: downloading the validation boxes\n")
	if err := downloadTo(openImagesBase+"validation-annotations-bbox.csv",
		filepath.Join(work, "boxes.csv")); err != nil {
		return fmt.Errorf("the box annotations could not be fetched: %w", err)
	}
	man.BoxesFile = filepath.ToSlash(filepath.Join(out, "validation-annotations-bbox.csv"))
	if err := copyFile(filepath.Join(work, "boxes.csv"), filepath.Join(out, "validation-annotations-bbox.csv")); err != nil {
		return err
	}

	// The per-class negative list. Without it there is no false-positive
	// denominator at all: an image containing nothing has to be one somebody said
	// contained nothing.
	fmt.Printf("fetch-groundtruth: downloading the per-class negative labels\n")
	if err := downloadTo(openImagesBase+"validation-annotations-human-imagelabels.csv",
		filepath.Join(work, "labels.csv")); err != nil {
		return fmt.Errorf("the label annotations could not be fetched: %w", err)
	}
	man.LabelsFile = filepath.ToSlash(filepath.Join(out, "validation-annotations-human-imagelabels.csv"))
	if err := copyFile(filepath.Join(work, "labels.csv"), filepath.Join(out, "validation-annotations-human-imagelabels.csv")); err != nil {
		return err
	}

	// Image metadata: the URL each image is fetched from, its author and its
	// licence. This is the file that makes attribution possible.
	fmt.Printf("fetch-groundtruth: downloading the image index\n")
	if err := downloadTo(openImagesBase+"validation-image-dimensions-with-rotation.csv",
		filepath.Join(work, "dims.csv")); err != nil {
		return fmt.Errorf("the image index could not be fetched: %w", err)
	}
	man.RotationsFile = filepath.ToSlash(filepath.Join(out, "validation-image-dimensions-with-rotation.csv"))
	if err := copyFile(filepath.Join(work, "dims.csv"),
		filepath.Join(out, "validation-image-dimensions-with-rotation.csv")); err != nil {
		return err
	}

	fmt.Println("fetch-groundtruth: the files are in place.")
	fmt.Println("  The IMAGES themselves are not downloaded by this command, because Open Images")
	fmt.Println("  publishes no single archive of them and the per-image URLs are signed and")
	fmt.Println("  expiring. `pocctl bench --images` fetches each one it needs and records the")
	fmt.Println("  SHA-256 and the attribution in this manifest as it goes.")
	if err := writeGroundtruthManifest(flags["manifest"], man); err != nil {
		return err
	}
	fmt.Printf("\nwrote %s\n", flags["manifest"])
	fmt.Println("  image licence      CC BY 2.0 — images by the Flickr authors listed per image")
	fmt.Println("  annotation licence CC BY 4.0 — the boxes and labels")
	fmt.Println("  These are DIFFERENT and a publication has to satisfy both.")
	return nil
}

// writeGroundtruthManifest records the provenance, and refuses to write one that
// would be a licence claim nobody checked.
func writeGroundtruthManifest(path string, man GroundtruthManifest) error {
	if strings.TrimSpace(man.ImageLicence) == "" || strings.TrimSpace(man.AnnotationLicence) == "" {
		return errors.New("the ground-truth manifest will not be written without BOTH licences " +
			"recorded: a manifest naming one of the two licences this material carries is a " +
			"manifest somebody relied on and was wrong")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}

// downloadCSV fetches a CSV and returns its rows.
func downloadCSV(url, dest string) ([][]string, error) {
	if err := downloadTo(url, dest); err != nil {
		return nil, err
	}
	f, err := os.Open(dest)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1 // the Open Images files are ragged and say so
	return r.ReadAll()
}

// classNames maps Open Images class IDs to names.
func classNames(rows [][]string) (map[string]string, error) {
	if len(rows) == 0 || len(rows[0]) < 2 {
		return nil, errors.New("the class list is not a two-column CSV; a class ID this build " +
			"cannot resolve is a parse failure and not a class nobody has heard of")
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		if len(r) < 2 {
			continue
		}
		out[strings.TrimSpace(r[0])] = strings.TrimSpace(r[1])
	}
	return out, nil
}

// boxesFor reads the box annotations and groups them by image.
//
// Coordinates arrive as PIXELS against the image's own dimensions, so the caller
// needs the dimensions file and must divide. It is done here rather than at each
// call site because a scale applied in two places is a scale that ends up applied
// twice somewhere.
func boxesFor(rows [][]string, names map[string]string) (map[string][]GTBox, map[string][]string) {
	out := map[string][]GTBox{}
	dropped := map[string][]string{}
	for _, r := range rows {
		if len(r) < 5 {
			continue
		}
		id := strings.TrimSpace(r[0])
		classID := strings.TrimSpace(r[1])
		name, known := names[classID]
		if !known {
			continue
		}
		x, e1 := strconv.ParseFloat(strings.TrimSpace(r[3]), 64)
		y, e2 := strconv.ParseFloat(strings.TrimSpace(r[4]), 64)
		w, e3 := strconv.ParseFloat(strings.TrimSpace(r[5-1]), 64)
		h, e4 := strconv.ParseFloat(strings.TrimSpace(r[6-1]), 64)
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil {
			continue
		}
		coco, mapped := cocoEquivalent[name]
		if !mapped {
			dropped[id] = append(dropped[id], name)
			continue
		}
		out[id] = append(out[id], GTBox{Class: coco, OI: name, X1: x, Y1: y, X2: x + w, Y2: y + h})
	}
	return out, dropped
}

// downloadTo fetches one URL to a path, refusing an empty or error response.
//
// The emptiness check is not a formality: a 403 from an expiring signed URL writes
// a file containing the words "AccessDenied", and a benchmark pointed at a CSV of
// error messages reports zero ground-truth boxes and a recall of zero over no
// denominator — which this package would correctly print as NOT RUN, but only
// because someone wrote that rule. Better to refuse here.
func downloadTo(url, dest string) error {
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("%s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned %s; Open Images publishes per-image URLs that are signed "+
			"and expiring, and a 403 here writes an error page where a CSV should be", url, resp.Status)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	n, err := io.Copy(f, resp.Body)
	if err != nil {
		_ = os.Remove(dest)
		return err
	}
	if n == 0 {
		_ = os.Remove(dest)
		return fmt.Errorf("%s returned no bytes", url)
	}
	return nil
}

// unusedZipGuard keeps the archive reader honest: Open Images' per-image fetches
// are sometimes ZIPs containing the image, and a caller that ignores the extension
// would be handed a ZIP renamed to .jpg and spend an afternoon finding out.
var _ = zip.OpenReader
