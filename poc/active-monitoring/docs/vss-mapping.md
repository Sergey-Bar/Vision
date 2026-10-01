# VSS 4.2 → this module

The European Video Security System taxonomy is the standard an operator's dashboard
is written in. This POC stores two class words and a family, and a claim on top of
them. This document is the mapping, and — more usefully — the places where the
mapping is **not** one.

It exists because the tempting thing to do is declare `bird` a `Person` subclass and
call the work done. It is not done: VSS distinguishes a person from a human body,
gives animals their own top-level class, and a detector that guesses between them
produces a dashboard that is confidently wrong.

## The two columns this module stores

| | where | what it is |
|---|---|---|
| `detections.class` | the row | the **model's** word. What an Alarm Definition is written against: `bird`, `car`, `person`, `camera_integrity`. It is a label on a claim, not a taxonomy node. |
| `detections.family` | the row | the **product's** grouping, resolved by `internal/classify`: `person`, `vehicle`, `animal`, `other`, … The one a report is read by. |

They are stored **beside** each other and not inside each other, and
`internal/classify` has a test called `TestOneWayOwnership` that fails if the class
is ever derived from the family or the reverse. The reason is the same reason a
report needs two: a rule that says `car` must keep matching when the taxonomy is
extended, and a person counting vehicles must not need a lookup table open in the
next window.

## The mapping, and where it stops

| this module | VSS 4.2 | honest? |
|---|---|---|
| `class=bird`, `family=animal` | `Animal` (`0.2.x`) | yes, with a gap: VSS separates `Person` from `HumanBody` from `Animals`, and this module has one animal word. A detector that can tell a cat from a dog cannot express it here. |
| `class=car`, `family=vehicle` | `Vehicle` (`1.x`) | yes, coarsely. A `car` is not a `Vehicle`, but nothing downstream needs the difference yet. |
| `class=person`, `family=person` | `Person` (`0.1.x`) | **not the same thing.** A detected human silhouette is a *claim about a person*, not a VSS `Person` object: VSS's `Person` implies a tracked individual, and this module has no identity, no track across minutes and no re-identification. Mapping `class=person` onto VSS `Person` would be asserting an identity the module has never measured. |
| `class=camera_integrity` | **no VSS class** | correct, and the reason is the point. A moved or covered camera is an **Alarm Definition** condition in VSS, evaluated against the camera object, not an object class. The `class` word names the trigger because the trigger is what fired. |
| `family=other` | **no VSS class** | correct: an unclassified detection must land somewhere visible, and "somewhere visible" is the point. Mapping it to a real class would invent a class the model did not name. |

## The four things a VSS mapping cannot carry, and what carries them instead

| | what a VMS would hold | what this module holds | why |
|---|---|---|---|
| object identity | a `Person` object with a track and an id | `detections.track_id`, within one run | re-identification across runs is not implemented and is not claimed |
| event time vs. ingest time | one timestamp | `detections.at` (the crossing), `created_at`, `updated_at` | a drift-corrected `normalized_at` does not exist in this module; the platform's does |
| bookmark → media link | `bookmarks.externalId` | `evidence.storage_ref`, hashed, derived from bytes | the path is derived from a SHA-256 and never supplied, so it cannot be pointed at somebody's file |
| alarm definition | server-side rule | `class` + `prompt_id` + the stored `contract_version` | a rule that moved but whose prompt did not is visible as a `contract_version` that no longer matches the prompt file. A VMS rule has no such trail. |

## What to say when a judge asks "does it map to VSS?"

> The class word maps. The identity, the track-across-runs and the alarm-definition
> evaluation do not, and this page is where that is written down rather than in the
> middle of a demo when it becomes a surprise.

`camera_integrity` is the sharpest of these. It has **no** VSS object class, because
a moved camera is not an object a VMS can draw a box around — it is the condition of
an object that is already there. This module makes it a whole-frame region and says
so (`region_source=whole_frame`), and the `region` column is what a VMS would have
dismissed as bounding the entire image.