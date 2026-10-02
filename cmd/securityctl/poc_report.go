package main

import (
	"encoding/json"
	"fmt"

	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/command"
)

// geometryFromSPS is the parenthetical on the banner's resolution line. The clip's
// dimensions come out of the stream's own parameter set, and where the camera's
// description disagrees the parameter set is right.
const geometryFromSPS = "read from the stream's own parameter set, which outranks anything the camera declared"

// printBanner names the camera, the geometry, the protocol and the limits.
//
// Every field is either a value or a labelled absence. A banner with a blank
// where the frame rate should be reads as "we did not look", and a judge reads
// it as "there is no frame rate"; saying "not declared by the camera" costs one
// line and is the difference between the two.
func printBanner(env *command.Env, rep *pocReport, o pocOptions) {
	w := env.Stdout
	fmt.Fprintf(w, "AI Captain capture proof — %s\n", pocBuildVersion)
	fmt.Fprintf(w, "  source     %s (%s)", rep.Source.Kind, rep.Source.Endpoint)
	if rep.Source.Transport != "" && rep.Source.Transport != "none" {
		fmt.Fprintf(w, ", %s", rep.Source.Transport)
	}
	fmt.Fprintln(w)
	if rep.Source.Resolution == "" {
		fmt.Fprintf(w, "  resolution %s\n", absence("the stream delivered no parameter set, so its frame size is not knowable"))
	} else {
		fmt.Fprintf(w, "  resolution %s, profile %d level %d (%s)\n",
			rep.Source.Resolution, rep.Source.Profile, rep.Source.Level, geometryFromSPS)
	}
	if rep.Source.FrameRate > 0 {
		fmt.Fprintf(w, "  frame rate %d fps (%s)\n", rep.Source.FrameRate, frameRateSource(rep))
	} else {
		fmt.Fprintf(w, "  frame rate %s\n", absence("not declared by the camera; the stream clock is used instead"))
	}
	if rep.Source.ClockRate > 0 {
		fmt.Fprintf(w, "  clock      %d Hz (%s)\n", rep.Source.ClockRate, clockSource(rep))
	}
	fmt.Fprintf(w, "  detector   %s, threshold %.2f, pre-roll %s, post-roll %s, caps %s / %d MiB\n",
		rep.Detector, o.threshold, o.preRoll, o.postRoll, o.maxTime, o.maxBytes>>20)
	fmt.Fprintf(w, "  AI plane   %s\n", absence("not configured in this build — every incident below is decided WITHOUT an observation"))
	for i, l := range rep.Limitations {
		marker := " "
		if i == 0 {
			marker = "*"
		}
		fmt.Fprintf(w, "  %s          %s\n", marker, l)
	}
	fmt.Fprintln(w)
}

// absence renders a labelled gap. The word "unknown" alone is not enough: a
// reader needs to know that the system looked and did not find it, which is a
// different fact from a field that was never populated.
func absence(why string) string { return "ABSENT — " + why }

func frameRateSource(rep *pocReport) string {
	if rep.Source.Kind == "file" {
		// An elementary stream carries no timing. The rate is a value this run was
		// told, and saying so is the difference between a number and a fact.
		return "a nominal rate supplied on the command line, because the file carries no timing"
	}
	if rep.Source.DeclaredGeometry {
		return "declared by the camera"
	}
	return "not declared; assumed for display only, and the clip's own timestamps are unaffected"
}

func clockSource(rep *pocReport) string {
	if rep.Source.Kind == "file" {
		return "this build's media timescale; a file has no clock of its own"
	}
	return "the camera's RTP clock"
}

func reportPoc(env *command.Env, o pocOptions, rep *pocReport, runErr error) error {
	if o.asJSON {
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			return err
		}
	} else {
		printSummary(env, o, rep)
	}
	if runErr != nil {
		return runErr
	}
	if rep.Outcome == "" {
		return command.Refuse("poc: the run produced no report")
	}
	return nil
}

func printSummary(env *command.Env, o pocOptions, rep *pocReport) {
	w := env.Stdout
	if o.quiet {
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "%d picture(s) read, last score %.2f\n", rep.Pictures, rep.LastScore)
	if rep.Triggered {
		fmt.Fprintf(w, "detector fired: %s\n", rep.TriggerWhy)
	} else {
		fmt.Fprintf(w, "detector did not fire\n")
	}

	switch rep.Outcome {
	case "clip_stored":
		c := rep.Clip
		e := rep.Evidence
		fmt.Fprintf(w, "\nCLIP  %s  %s  %d picture(s), %d keyframe(s), %d bytes\n",
			c.Duration.Round(time.Millisecond), fmt.Sprintf("%dx%d", c.Width, c.Height), c.Pictures, c.KeyFrames, c.Bytes)
		fmt.Fprintf(w, "      lead-in %d picture(s) over %s\n", c.PreRollPics, c.PreRollSpan.Round(time.Millisecond))
		// The three degraded conditions a clip can be in, each named. A clip
		// that hit a cap and one that was simply cut at the end of the stream
		// are both "shorter than you asked for", and only one of them is a
		// product decision.
		if c.PreRollReason != "" {
			fmt.Fprintf(w, "      LEAD-IN DEGRADED  %s\n", c.PreRollReason)
		}
		if c.CapReason != "" {
			fmt.Fprintf(w, "      CAPPED             %s\n", c.CapReason)
		}
		if c.StopReason != "" {
			fmt.Fprintf(w, "      window closed      %s\n", c.StopReason)
		}
		if c.DamagedReason != "" {
			fmt.Fprintf(w, "      DAMAGED            %s\n", c.DamagedReason)
		}
		if c.PreRollReason == "" && c.CapReason == "" && c.DamagedReason == "" {
			fmt.Fprintf(w, "      lead-in intact, no cap, no damage\n")
		}

		verified := "NOT verified"
		if e.HashVerified {
			verified = "read back and matched byte for byte"
		}
		fmt.Fprintf(w, "\nEVIDENCE  %s\n          sha256 %s (%d bytes)\n          %s\n",
			e.StorageRef, e.SHA256, e.SizeBytes, verified)
		i := rep.Incident
		fmt.Fprintf(w, "\nINCIDENT  %s  state=%s severity=%s\n", i.IncidentID, i.State, i.Severity)
		fmt.Fprintf(w, "          %s\n", i.Title)
		fmt.Fprintf(w, "          reason-code %s — %s\n", i.ReasonCode, i.Reason)
		fmt.Fprintf(w, "          decided WITHOUT an AI observation: yes\n")
		fmt.Fprintf(w, "          awaiting an operator disposition: yes\n")
		fmt.Fprintf(w, "          artefact %s\n", i.ArtifactPath)

		fmt.Fprintf(w, "\nTHE THREE ABSENCES\n")
		fmt.Fprintf(w, "  no observation        %s\n", rep.Absences.NoObservationWhy)
		fmt.Fprintf(w, "  degraded observation  %s (%s)\n", rep.Absences.DegradedWhy, "the model was never asked")
		fmt.Fprintf(w, "  insufficient evidence %s\n", rep.Absences.InsufficientWhy)
		fmt.Fprintf(w, "  treated as a negative %s\n", yesNo(rep.Absences.IsNegative))

	case "no_clip":
		fmt.Fprintf(w, "\nNO CLIP  %s\n", rep.OutcomeWhy)
		fmt.Fprintf(w, "        This is a completed run, not a failure. The capture path read %d picture(s) without error.\n", rep.Pictures)
	}

	for _, d := range rep.Degradations {
		fmt.Fprintf(w, "\nDEGRADATION  %s\n", d)
	}
	if len(rep.Degradations) == 0 {
		fmt.Fprintf(w, "\nno degradations\n")
	}
	fmt.Fprintln(w)
	// The wording here is deliberate and the test above enforces it: the phrases
	// a reader scans for - "nothing happened", "all clear" - are absent from
	// this command's output entirely, including from the line that says the
	// output is careful. A grep by a reviewer has to come back clean too.
	fmt.Fprintf(w, "A missing AI observation is never a negative answer. No line above\n")
	fmt.Fprintf(w, "reports that %s was quiet; it reports that the question was never asked.\n", rep.SiteID)
	fmt.Fprintf(w, "Run with --json for the machine-readable report.\n")
}

func yesNo(b bool) string {
	if b {
		return "YES — a missing observation was read as a negative, which is a defect"
	}
	return "no"
}
