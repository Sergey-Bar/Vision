package main

// Vehicle classes get their colour estimated from the frame; the rest do not.
var vehicleClasses = map[string]bool{
	"car": true, "truck": true, "bus": true, "motorcycle": true,
	"bicycle": true, "train": true, "boat": true, "airplane": true,
}

var animalClasses = map[string]bool{
	"bird": true, "cat": true, "dog": true, "horse": true, "sheep": true,
	"cow": true, "elephant": true, "bear": true, "zebra": true, "giraffe": true,
}

// mapCOCOClass maps a COCO class name (what the model emits) onto the POC's
// three categorization buckets (what the mission's dashboard shows). Unknown
// classes land in "other" rather than being dropped: an object the model saw
// is still an object, and a silent drop would make the dashboard's counts a
// lie about the frame.
func mapCOCOClass(cls string) (category, sub string) {
	switch {
	case cls == "person":
		return "biological", "human"
	case vehicleClasses[cls]:
		return "vehicle", cls
	case animalClasses[cls]:
		return "biological", cls
	default:
		return "other", cls
	}
}

// detectionLabel is the human-readable label on the overlay and in the index:
// vehicles carry their colour ("red car"), a person is a "person" (the mission
// words it that way, and the sub-category keeps "human" for structured query),
// everything else is its class name.
func detectionLabel(category, sub, color string) string {
	switch category {
	case "vehicle":
		if color != "" {
			return color + " " + sub
		}
		return sub
	case "biological":
		if sub == "human" {
			return "person"
		}
		return sub
	default:
		if sub == "" {
			return "object"
		}
		return sub
	}
}
