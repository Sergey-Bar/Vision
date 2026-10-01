# Active Monitoring POC — פרויקט בדיקת האפקטיביות החי

> ⚠️ **קריאה חובה לפני הרצה של שבוע.**
> קיום ה־POC הוא הוכחה להנדסה: **המערכת יכולה להפיק אירוע, לשמור קליפ
> מאומת, ולדחוף ל־API של Milestone, בלי לאבד אירוע ובלי להמציא תשובה.**
> זה לא ראיות שהמערכת מזהה עופות בעולם. זה לא ראיות שהמערכת מסובכת
> גבוהה. זה לא אומר שהאירוע הגיע ל־Smart Client של מילסטון. בסעיף
> [מה שה־POC לא מוכיח](#מה-שהpoc-לא-מוכיח) כתוב מה בדיוק **אין** כאן.
> אם מישהו ישאל אתכם "האם זה עובד?" — התשובה הנכונה היא שם הקטע הזה.

מסמך זה מיועד לאדם. הקוד, הבדיקות, הדוח והחוזה (`CONTRACT.md`) באנגלית,
מפני שהפורמטים על החוט, קריטריות הקבלה ושורות הלוג הן אנגלית. אין כאן
שכבת תרגום, כי שכבת תרגום במסלול אמינות היא מקום שבו משפט אחד נעלם.

---

## Read these, in English, before anything else

Four documents survive in this module. There were eleven; the rest are folded into
these and the fold is recorded at the bottom of each.

| | |
|---|---|
| [`docs/JUDGE.md`](docs/JUDGE.md) | **the five minutes a judge watches**, and what each step does *not* prove |
| [`docs/STATUS.md`](docs/STATUS.md) | what is built, what is not, and who each missing thing is waiting on |
| [`CONTRACT.md`](CONTRACT.md) | the law. Rules R1–R8, the acceptance arithmetic, the four absence words |
| [`contracts/hafnia/README.md`](contracts/hafnia/README.md) | what the VLM client assumes, which assumption the provider's own documentation contests, and what settles it |

### Three commands, on a clean checkout

```powershell
$env:Path += ";C:\Program Files\Go\bin;C:\msys64\mingw64\bin;$env:USERPROFILE\go\bin"
cd poc\active-monitoring

go build -o bin\pocctl.exe ./cmd/pocctl
go build -o bin\watch.exe   ./cmd/watch
go build -o bin\fixtureprobe.exe ./cmd/fixtureprobe

.\scripts\verify.ps1          # the gate: build, vet, boundary, tests, 20 red checks
```

**Build comes first because the binaries are not in git.** `bin/` is gitignored,
so on a fresh clone `.\bin\pocctl.exe` does not exist yet, and a document that
opens with a command from `bin\` is a document whose first line fails.

Then the two commands that make the detector real:

```powershell
.\bin\pocctl.exe fetch-runtime --url <onnxruntime-release-zip-url>
.\bin\pocctl.exe fetch-model   --url <your-onnx-url>
.\bin\fixtureprobe.exe -detector qa\fixtures\crossing.mp4
```

`fetch-runtime` prints the library's SHA-256. **Read it against the release page
and re-run with `--sha256=` to confirm it** — a digest nobody checked is not a
provenance record, it is a number. It ships no hardcoded digest on purpose: one
baked into a source file is a digest somebody verified once, on a build that may
not be the one being downloaded.

`fixtureprobe -detector` prints the declared output shape, the record width, the
raw candidate count, what each suppression pass removed, and the per-class
histogram. **Those five facts are what make a zero diagnosable** rather than merely
observable, and this repository shipped three silent zeros before this pass.

### The system, on one page

```
  camera ──► recorder ──► frame loop ──► TRIGGER ──► line test ──► detection
             (ffmpeg)      (soak.Runner)    │            (detect)      │
                                             │                            │
                        ┌────────────────────┴───────┐                    │
                        │  detector  (internal/     │                    │
                        │  ortdetect, ONNX, cgo)    │                    │
                        │  detector  (pyworker,     │                    │
                        │  Python)                 │                    │
                        │  novelty  (pure Go)      │                    │
                        └───────────────────────────┘                    │
                                                                            ▼
                                          store ──► evidence ──► ledger ──► seal
                                            │                        │
                                            ├──► outbox ──► XProtect ──┘
                                            └──► box_observations ──► replay
```

Three trigger modes, all still selectable:

- **`detector`** — the shipped path. In-process ONNX Runtime, real classes and
  scores per frame, Kalman-filtered tracks, two-stage ghost recovery.
- **`sidecar`** — the per-frame Python classifier. Kept as the cross-decoder
  parity reference: the implementation that produced every number this project has
  reported so far, in a different language against a different Numpy.
- **`novelty`** — pure-Go whole-frame change measurement. Licence-free, no
  weights, and **no class vocabulary at all**, which is why the previous build
  reported zero vehicles.

The AI plane is optional and its absence is a first-class state, not an error.
`not_asked` and `no_objects` are different words and the report prints them
differently; a run that asked nothing and a run that asked and found nothing are
not the same claim.

### Module boundary

`scripts/verify.ps1` holds ten import-graph rows. The load-bearing ones:

- `internal/vlm` may not reach the store or the pipeline. RC-4's replay proof is
  structural **only** because of this: a verifier that can read what somebody said
  last time is a recorder.
- `internal/bench/metrics.go` may not reach anything. Its golden test works
  because it can be checked against hand-computed arithmetic, and a metric that
  read the database could not be.
- `internal/ortdetect` may not reach storage, because the cross-decoder parity
  claim is only meaningful if neither decoder can be handed the other's answer.
- `internal/proposal` is the strictest leaf: `ai.Box`, `detect.Box` and
  `novelty.Box` are ALIASES of it, so all three import it and it may import none
  of them back.

### What changed, and what it cost

The trigger path is no longer a per-frame ONNX sidecar. It is a **pure-Go novelty
engine** (`internal/novelty`) that MEASURES which part of the frame changed, and a
**hosted model** (`internal/vlm`) that is asked **once per event** what is happening
in that region. The split is the honest one:

> Cheap perception proposes; the expensive model verifies.

The Hafnia API documents **no region output and no bounding box** — it returns text.
So the box on a pushed event is ours and measured, and the label on it is the model's
and is attributed as a claim. There is no synthetic model hash anywhere.

The `SP_POC_AI_MODEL`, `SP_POC_AI_ANCHORS` and `SP_POC_SIDECAR_SCRIPT` variables are
still read — `SP_POC_TRIGGER_MODE=sidecar` is a real mode and a useful one, it is how
a threshold gets chosen — but the **shipped pipeline does not require them**, and
`pyworker/` is a measurement tool rather than a dependency.

New commands:

```
pocctl demo --live             the same demo against a live camera and a live VMS
pocctl prove <detection>       recompute one decision from stored facts, zero provider calls
pocctl audit ledger            recompute the hash-chained decision ledger
pocctl audit seal              sign the chain head, or verify a seal
pocctl audit seals             verify every seal against a trusted public key
pocctl audit decisions         the stored decision inputs: score, threshold, measured region
pocctl audit observations      every model's answer, with its attribution and reason code
pocctl probe --live           read-only dial of the XProtect instance; per route: reachable / contradicted / unauthenticated / absent
pocctl vlm-probe              ONE call to the pinned VMS host, no clip; the raw body lands in contracts/hafnia/raw/
pocctl mark <tag>             one human verdict on one pushed event; this is what makes the false-positive rate measurable
pocctl search <text>          what the model SAID, with its clip ref and its attribution
pocctl brief                  one line per hour: frames read, proposals, and whether the model was asked at all
```

| | |
|---|---|
| [`docs/vss-mapping.md`](docs/vss-mapping.md) | how the class and family columns relate to VSS 4.2, and the four places the mapping is **not** one |

---

## מה זה

מערכת אחת ש:

1. קוראת זרם וידאו **פעם אחת**, בתהליך `ffmpeg` אחד, ושני פלטים ממנו:
   הקלטה מתגלגלת (`-c copy`) **ו** צינור MJPEG לניתוח.
2. שולחת פריים RGB גולמי לתהליך Python צד־שרת (sidecar) שמריץ מודל ONNX.
3. עושה זיהוי קו־חצייה עם מעקב IoU ומכונת מצבים.
4. שומר קליפ לכל אירוע, **מתמלל ומאמת שהבייטים תואמים** את ה־SHA-256.
5. דוחק אירוע ל־Milestone VMS דרך ה־REST API הרשמי, **ומאמת בקריאת חזרה** שהוא
   באמת נמצא שם.
6. כותב הכול ל־SQLite במצב WAL, ומדפיס דוח שכל מספר בו נקרא משורה.

## שלוש עקרונות שלא נגעים בהם

| | |
|---|---|
| **קורא אחד, שעון אחד** | תהליך `ffmpeg` אחד הוא הקורא היחיד. Python לא קורא וידאו, לא עוקב אחרי אובייקטים, לא מחזיק מצב. חותכות `ffmpeg` חוזרות לפי **שעון הקיר של Go** — כך הקליט תמיד מראה את הדבר שהפעיל אותו. |
| **רישום לפני אפקט** | שורת הזיהוי נכתבת ונשמרת **לפני** חיתוך הקליט ולפני הדחיפה. `kill -9` בכל רגע משאיר מצב שניתן להמשיך ממנו, לא אירוע שאבד. |
| **תשובה חסרה אינה תשובה שלילית** | ארבע תשובות נפרדות: `no_objects` (המודל רץ ולא מצא), `degraded` (נשלח, נכשל/איחר), `not_asked` (לא נשלח), `cannot_tell` (הגיע ולא מפורש). **אין** מצב שבו כישלון הופך ל־"אין כלום". |

---

## מה צריך להיות מותקן

```powershell
# כל הכלים נמצאים ב־PATH אצלכם; ה־Go וה־gcc צריכים התקנה מפורשת.
$env:Path += ";C:\Program Files\Go\bin;C:\msys64\mingw64\bin;$env:USERPROFILE\go\bin"

go version          # 1.26 ומעלה
ffmpeg -version     # חובה (D2)
ffprobe -version    # חובה
py -3 --version     # חובה

# תלויות Python של הצד־שרת (D2)
py -3 -m pip install -r pyworker/requirements.txt
```

> **שים לב:** `go`, `gofmt` ו־`gcc` **אינם** ב־PATH ב־Windows רגיל. זה לא באג —
> זה המצב שה־`scripts/verify.ps1` נועד לו. הוא מאתר כל אחד בעצמו ומדפיס **SKIPPED**
> עם הסיבה כשמשהו חסר, ולא "עבר בשקט".

---

## התחלה מהירה (בדיקה, שלוש דקות)

```powershell
cd poc\active-monitoring
.\scripts\verify.ps1                                   # השער היחיד של המודול
.\bin\pocctl.exe probe                                  # טבלת ביטחון החוזה מול Milestone
.\bin\pocctl.exe rehearse --data-root C:\poc-rehearsal  # שער D: התנהגות בכשל
.\bin\pocctl.exe demo                                   # הדמו במלואו, כולל המסך
```

`pocctl demo` מריץ את הצינור האמיתי, מראה את המסך האמיתי, **ומציג אירוע שהוכח שנמצא
בקריאת חזרה**. הוא גם מסמן `mode=demo-replay` בכל מקום, כי צופה לא אמור לחפש
את זה.

---

## שבוע ימים

```powershell
# 1. חומר מסומן
scripts\make-bird-clip.sh        # קליפ סינתטי, ניתן לשחזר ביט־לביט
scripts\make-loop.sh             # מדפיס expected_events_per_day *לפני* הרצה

# 2. מדדים — מבנה ההרצה המלא
$env:SP_POC_MODE='live'
$env:SP_POC_SOURCE_KIND='loop'
$env:SP_POC_SOURCE_ARG='testdata\loop.mp4'
$env:SP_POC_SOAK_TARGET='168h'
$env:SP_POC_LABEL='week-1'
.\bin\watch.exe                  # יוצא עם Ctrl-C

# 3. הדוח — ניתן לייצר מחדש מהמסד
.\bin\pocctl.exe report --data-root C:\poc-data
```

`SP_POC_SOAK_TARGET` הוא **הכרזה שנדרשת מראש**. הרצה קצרה יותר מהיעד מדווחת
`PROVISIONAL`, לא `PASS`, כי משהו שלא הסתיים אינו יכול לסיים טענה על משהו שארך
שבוע.

---

## כל משתנה `SP_POC_*` חייב להיות מוגדר

המודול **מסרב לעלות** אם משתנה חסר. אין ברירת מחדל שמשנה התנהגות, ואין
"ברירת מחדל שסבירה". כל הסרטוב שאתם רואים כאן נכתב כדי לומר למפעיל **מה ייפגע
ולמה**, לא רק שמשהו לא בסדר:

```
SP_POC_DATA_ROOT: not set; without it there is nowhere to put the SQLite WAL,
the rolling segments or the evidence store, and the run cannot record anything;
set it with a path on a LOCAL disk
```

כל המשתנים במקום אחד: `internal/config/config.go`. כל אחד מהם מוגדר בטסט
(`TestEveryVariableIsRequiredByName`) שמוחק את המשתנה **אחד** ודורש שהסירוב יציין
אותו בשמו.

---

## קריטריות הקבלה

שלוש גריטות בלבד: `PASS`, `FAIL`, `NOT RUN`. `NOT RUN` תמיד כולל "מה חסר", ואף פעם
אינו נכתב כ־`PASS`.

| # | קריטריון | החישוב בדיוק כפי שהוא מחושב |
|---|---|---|
| `AC1` | אפס נפילות | `process_exits == 0` **and** `count(heartbeat_gap > 30s) == 0`. אפס מוחלט. לא "99.9%". |
| `AC2` | אפס אירועים אבודים | `count(detections where status != 'done' and age > 5m) == 0` |
| `AC3` | עדות שלמה | `count(clip_stored where readback_sha256 != sha256) == 0` |
| `AC4` | דחיפה בלי אובדן ובלי כפילות | `count(detections with no outbox row) == 0` **and** `count(attempts with no result) == 0` **and** `count(duplicate ids per tag) == 0` |
| `AC5` | משאבים תקופים | `rss <= declared` **and** `db <= declared` **and** `disk <= declared` **and** `cpu <= declared` |
| `AC6` | אין תשובות מומצאות | `count(unparsed responses) == 0` **and** `count(healthy from a failed read) == 0` **and** `ai_p99 <= declared` |
| `AC7` | תקלות מדומות התנהגו כמצופה | כל תקלה שהוזרקה חייבת להכיל תוצאה צפויה ונצפית |

שלוש הערות שחשובות יותר מרוב המספרים:

- **`AC5`'s CPU** הוא `NOT RUN` אם לא הוגדר תקרה. לא `PASS`.
- **`AC7`** הוא `NOT RUN` אם לא הוזרקה תקלה. הוא **לא** מקבל `PASS` אוטומטית
  מהעובדה שמערכת לא נפלה. הפרה קודמת לשעון ההרצה.
- **`AC3` ו־`AC4`** הן `NOT RUN` אם לא נמצא אירוע אחד, כי קריטרייה שלא נוצה עליה
  לא נמדדה. הרצה שלא מצאה כלום אינה מדווחת כ־"הכול עבר" — היא אומרת `NOT RUN`.

---

## מה שה־POC **לא** מוכיח

זה הקטע החשוב ביותר במסמך הזה, והוא נכתב כרשימה כדי שאף עובד לא יעלה אותו
לכותרת בעתיד:

- ❌ **"עבר את כל מחלקות התקלה"** — זו רשימה של תקלות שאדם הזריק.
- ❌ **"אין אזעקות שווא"** — איש לא סימן אף אירוע כשווא, ולכן **אין מספר**.
- ❌ **"אומת מול Milestone"** — שום שרת VMS היה נגיש מעולם.
- ❌ **"100% זיהוי"** — אין אמת מוחלט; יש שיעור זיהוי.
- ❌ **"המסך מראה שהכול בסדר"** — המסך מראה מה שיש במסד הנתונים.
- ❌ **"מזהה עופות"** — קליפ העוף הוא `synthetic`: מלבן נע.
- ❌ **"פלט ל־XProtect"** — מה שהוכח הוא שה־`POST` תואם לחוזה ושקריאת החזרה
  מצאה אותו.

כל מסמך שהמודול מייצר — הדוח, המסך, תדפיס ההדגמה — מדפיס רשימה זו במלואה.

---

## ה־gate: `scripts/verify.ps1`

זה **ה־gate היחיד** של המודול. שורש המאגר **אינו** בונה מודול מקונן — `go build ./...`
שם פשוט מדלג את `poc/active-monitoring/`, ולכן אין לו זכות לומר דבר עליו. הסקריפט
גם בודק שהשורש **אכן** לא בונה אותו, כי גבול שקיים רק בכיתוב אינו גבול.

הוא רץ, בסדר זה, ועוצר בכשל הראשון:

| שלב | מה הוא בודק |
|---|---|
| toolchain | `go`, `gofmt`, `gcc` (ל־`-race`), `ffmpeg`, `ffprobe`, Python. כל אחד מאותר **במפורש**. חסר ⇒ `SKIPPED` עם סיבה, לא "עבר". |
| build | `go build ./...` |
| format | `gofmt -l` חייב להדפיס **כלום** |
| vet | `go vet ./...` |
| boundary | אין import של הפלטפורמה; השורש לא בונה את המודול; `internal/xprotect` לא מכיר את `xpsim`; `internal/detect` ו־`internal/ai` לא מכירים אחסון או את התול |
| tests | `go test -race -p 1 -count=1 ./...` |
| sidecar | `py -3 -m unittest` על `pyworker/` |
| **red check** | **אחד עשרה בקרות מוסרות מהקוד**, וכל אחת חייבת להפוך את הסביבה ל־אדום |

### ה־red check

זה השלב שהופך את כל הששה האחרים משמעותיים. **הוא מוחק קו מהקוד בייצור**, לא
טסט. מחיקת טסט לא מוכיחה דבר: סביבה עם טסט אחד פחות עדיין ירוקה, והדבר שהטסט
שמר עליו עדיין לא מוגן. מחיקת הבדיקה עצמה מוכיחה שהטסט **שומר** עליה.

שלוש הראשונות היו קיימות לפני עבודת ה־VLM, ושבע האחרונות הן `RC-1` עד `RC-7` —
שבע הבדיקות שהתוכנית קוראת להן "ההבדל בין הדמו למערכת". כל אחת מסירה הכחשה
אחת מסוימת:

| נמחק | למה הסביבה חייבת להאדים |
|---|---|
| `internal/evidence/store.go` — בדיקת צורת ה־SHA-256 | בלי היא שם שנבחר על ידי קורא מתקבל, ושתי תקלות יכולות להצביע על אותו אובייקט |
| `internal/store/detections.go` — ה־`WHERE … status NOT IN ('done')` | בלי היא זיהוי שהסתיים אפשר להזיז שוב, וחצייה אחת מקבלת שתי סיפורים |
| `internal/detect/engine.go` — רצועת הסבילות | בליה כל פריים "על הקו", ולא חוצה דבר: ריצה מדווחת אפס אירועים **ואין שום שגיאה** |
| `internal/vlm/parse.go` — ניקוי גזירה כשלא נשאלו | מפעיל רואה "לא קרה דבר" כי **אנחנו** לא הצלחנו לשאול |
| `internal/store/observations.go` — אותו כלל בכתיבה | כותב שני שכח את הפרסר מכניס את אותה שקר לטבלה שההוכחה קוראת |
| `internal/store/ledger.go` — השוואת הגיבוב | "הלדגר שלם" הופך להערה |
| `internal/store/ledger.go` — הטריגרים של append-only | אדם עם `sqlite3` עורך החלטה שכבר נרשמה |
| `internal/soak/replay.go` — אפס קריאות ספק בהשרדה | "ניתן להשמע מעובדות שמורות" מפסיק להיות טענה על הקוד הזה |
| `internal/soak/snapshot.go` — השיוך הפריים | התמונה שבקובץ היא פריים אמיתי מאותה מצלמה ב**רגע אחר** |
| `internal/xprotect/client.go` — סירוב תיבת אפס | Milestone מצייר כלום בזמן שמסד הנתונים אומר שהאזור אמיתי |
| `internal/vlm/vlm.go` — סירוב חוצה־אזור | אתר ב־EU שולח וידאו למארח ה־US |

---

## מה נבדק מול החוזה של Milestone

`contracts/xprotect/` מכיל את המפרטים של Milestone, **כפי שהורדו מהאתר שלהם בעצמם**,
עם תאריך השליפה והרישיון (MIT, מצוין בתוך הקובץ). `pocctl probe` מדפיס את טבלת הביטחון.

כל נתיב וכל שדה שהלקוח שולח נבדקים בטסט
(`internal/xprotect/contract_test.go`) מול הקובץ שהורד. **שינוי נתיב או שדה גורם
לטסט להיפול, ואין צורך בשרת כלל כדי שהוא ייפול.**

שלושה מקומות שבהם הקוד הקיים של הפלטפורמה שגויים, ולכן הקוד שלו **לא** שוכפל כאן:

1. `internal/milestone/routes.go` משתמש ב־`/api/v1/…`. ה־API האמיתי הוא תחת
   `/API/rest/v1/`.
2. `internal/milestone/writeback.go` שולח
   `{"bookmarks":[{idempotencyKey, deviceRef, time, label, description}]}`. הגוף
   שפורסם הוא `newBookmarkBody{header, devicePath{type,id}, timeTriggered, …}`.
   `idempotencyKey` ו־`deviceRef` לא קיימים.
3. **אין לפלטפורמה כלל נתיב "הפעלת אירוע".** היא קוראת אירועים ומוציאה סימניות;
   ה־POC מוסיף את זה, וזה בדיוק מה ש־MIP plug-in קיים כדי לעשות.

---

## מפת המודול

| חבילה | אחראית ל | יודע על |
|---|---|---|
| `internal/config` | כל הקריאה היחידה לסביבה | כלום |
| `internal/source` | בניית שורת הפקודה של `ffmpeg`, `ffprobe`, טבלת יכולות | כלום |
| `internal/recorder` | תהליך `ffmpeg` היחיד, צינור MJPEG, קטעים, גלילת תקציב דיסק, חיתוך קליפים | `internal/source` |
| `internal/ai` | לקוח הצד־שרת, אוצר האחווה, p50/p95/p99 | כלום |
| `internal/novelty` | **מנוע החדשנות המקומי** — מודד את האזור מפיקסלים, בלי ONNX ובלי Python | כלום |
| `internal/vlm` | **לקוח Hafnia** — העלאה, קריאה, פריסה, סימון אזור, תקציב יומי | `contracts/hafnia` |
| `internal/detect` | מעקב IoU, מכונת מצבי חציית קו, קירור | כלום |
| `internal/store` | SQLite ב־WAL, outbox, **לדגר משורש גיבוב**, חתימת ed25519, הפקה חוזרת מהמסד | כלום |
| `internal/evidence` | מאגר מבוסס תוכן, הגנות נתיׁ, ניקוי לפי גיל | כלום |
| `internal/xprotect` | תבניות חוט Milestone, דחיפת **אירוע ו־bookmark** עם קריאת־חזרה | `contracts/xprotect` |
| `internal/xpsim` | מדמה VMS מקומי, כולל קריאת־חזרה ל־bookmarks | כלום — הוא **שרת**, לא משתתף |
| `internal/supervisor` | תהליכי הילדים, backpressure, `stream_gap` | `internal/recorder` |
| `internal/soak` | preflight, קריטריות, מנוע ההרצה, `prove`, הדוח | הכול |
| `internal/ui` | המסך, **הגזירה שרופה בפיקסלים**, כותרות אבטחה | `internal/store` |
| `pyworker/` | צד־שרת Python: פריים → תיבות. **כלי מדידה**, לא במסלול ההפעלה | `numpy`, `onnxruntime`. **לא** OpenCV, **לא** Pillow |

`internal/vlm` ו־`internal/novelty` הם **עלים**: הגרף־הייבוא ב־gate אוסר עליהם לייבא
מסד, צינור, זיהוי או ממשק. לוקח ה־VLM אינו יכול לקרוא מהמסד ולהחזיר מה שמישהו אמר
בפעם האחרונה — וזה בדיוק מה ש־`pocctl prove` טוען שאין בו.

`internal/xpsim` **לא** מיובא על ידי `internal/xprotect`. זו לא בדיקת סגנון: הלקוח
אינו יודע שיש מדמה, והמדמה אינה יודעת שיש צינור. הגבול נבדק ב־**גרף הייבוא** על
ידי ה־gate, לא בחיפוט טקסט.

---

## שאלות שיגיעו, והתשובות

**"זה מזהה עופות?"**
לא. קליפ העוף הוא מלבן נע שנבנה מפריימים של `ffmpeg`. הוא מסומן `synthetic` בכל
מקום שיש בו מספר.

**"האירוע הגיע ל־מילסטון?"**
לא. יעד הדחיפה הוא מדמה מקומי. המסך והדוח אומרים זאת במילים בכל עמוד. מה שכן
הוכח: שה־`POST` תואם למפרט שהורד מהאתר של Milestone, ושקריאת חזרה מצאה את
האירוע.

**"הדטקטור היה ירוק?"**
אם `verify.ps1` הדפיס GREEN, הוא הריץ גם את ה־red check — כלומר שלוש בקרות הוסרו
מהקוד והסביבה הפכה לאדומה כל פעם. אם הוא הדפיס GREEN **עם SKIPS**, קרא את הדללת
לפני שאתה מתייחס אליו כ־עבר.

**"למה לא שתי הרצות במקביל?"**
כי זו לא סיבה. התוכנית אומרת חד־מקור, והשתנות של סביבת הריצה היא רעיון שרוצה
להיכנס לתוך מדידה. אם יש סיבה אמיתית, היא נכתבת ב־ADR לפני השינוי, לא אחריו.

**"אפשר להתחיל קיצור את התקציב?"**
אפשר, והדוח יסמן `PROVISIONAL` — לא `PASS`. `PROVISIONAL` הוא אמרה שהריצה
טרם הסתיימה, לא `PASS` רך.

---

## מה נמדד בהרצה אמיתית (30.09.2026)

שלוש הממצאים מהרצה אמיתית, רשומים כאן כי הם משנים את מה שהרצה על חומר
סינתטי שווה:

**1. מודל COCO לא רואה את החומר המחויב.** הרצה אמיתית עם `yolov4.onnx` מול
`car-crossing.mp4` החזירה **אפס תיבות** בשלושה רגעים. מלבן צבעוני שטוח על רקע
`testsrc` אינו תמונה, ומנבא שמופה על תמונות צודק לא לראות בו כלום. זו לא תקלה
וזו לא כיוון יתר על כיוון — לכן שדת ה־`real` ב־`labels.json` ריקה.

**מה זה אומר בפועל:** הרצה על חומר סינתטי מפיקה **אפס אירועים**, והדוח אומר
את זה: `AC2`, `AC3` ו־`AC4` יוצאים `NOT RUN` עם הסיבה "אין אירוע שנרשם". זו
**התנהגות מכוונת ונכונה**, וזה הדבר החשוב ביותר להבין לפני שמראים למישהו הרצה
על חומר סינתטי.

**נתיב הזיהוי מוכח בדרך אחרת.** בדיקת אינטגרציה ב־`internal/soak` מפעילה את כל
הצינור האמיתי — `ffmpeg` אמיתי, מקליט אמיתי, מזהה אמיתי, מסד אמיתי, מאגר
תוכן אמיתי, לקוח Milestone אמיתי, סימולטור אמיתי — ומתחלף רק את **דיוק המודל**.
היא מראה פריים שהופך לחציית קו, לשורה מחויבת, לקליפ מאומת, לאירוע נדחף, ולקריאת
חזרה שמוצאת אותו תחת התג שמופק מתוכן. שלמות הקליט עצמה — קידוד מחדש, קריאה
בייט־לבייט, hash, ביטול כפילויות, ניקוי — מוכחת בנפרד ב־`internal/recorder` וב־
`internal/evidence` מול `ffmpeg` אמיתי.

**2. תפוקה היא האילוץ האמיתי, והיא נמדדת.** `yolov4.onnx` ב־416×416 על המעבד
של המכונה הזו לוקח **כ־0.9 שניות לפריים**, מול מו־תקן 0.8 שניות. ב־`--ai-fps 5`
הצינור מתמלא תוך שניות והריצה מסתיימת ב־`ai_pipe_backpressure` — שהוא בדיוק
ההתנהגות המתוכננת: תקלה שנרשמה, `ffmpeg` נהרג, ואין שום drop שקט. ב־
`--ai-fps 1` הצינור נשאר בעומק 0–5 והריצה מגיעה ליעד עם `AC1`, `AC5` ו־`AC6` עוברים
על מספרים מדודים. הריצו `pocctl cap` כדי למדוד על המכונה שלפניכם.

**3. פער heartbeat בן 71 שניות היה ארטיפקט, לא נפילה.** גרסה ראשונה של לול הריצה
השתמשה ב־goroutine אחד גם לפריימים וגם ל־heartbeat, ולכן הסטת מודל ראשונה
שתקה את אות החיות ו־`AC1` נפל מסיבה שאין לה קשר לאמינות הצינור. ה־heartbeat
רץ עכשיו ב־goroutine נפרד, ול־`internal/soak/loop_test.go` יש בדיקה בדיוק לזה.

**4. כל אירוע שנדחף נשאר `boundingBox` של אפסים, בעוד השורה במסד הנתונים החזיקה
מספרים אמיתיים.** `internal/soak/runner.go` קבע `Box: xprotect.Box{}` בדיוק כשהוא
בונה את האירוע. כל 286 הבדיקות היו ירוקות, המזהה מצא אובייקטים אמיתיים, והדחיפה
הצליחה — ועדיין כל מה שיצא לחוט היה ארבעה אפסים. זהו הדבר שהיה הכי גרוע בדמו:
ה־bounding box הוא מה שמצייר את האובייקט ל־Smart Client, והצגת אפסים שם היא
הצגה שהמוצר לא עובד. עכשיו התיקון הוא אותו חישוב בדיוק ש־`detections.box_*` משתמש
בו (אלפיות), והבדיקה ב־`internal/soak/pipeline_e2e_test.go` משווה את האובייקט
שנדחף לשורה שלו — **השוואה, לא מבט על המסך**. ההשוואה נשברת אם מחזירים את
האפסים.

**5. שדה אחד שהסיידקר שלח, וה־POC לא זיהה דבר בכלל.** הסיידקר החל לשלוח
`input_w`; בצד ה־Go הפענח מפענח **קשוח** (`DisallowUnknownFields`) — וזה נכון,
זה מדויק, וזה בדיוק מה שהכיס את התקלה. כל תשובה נדחתה, כל הזמן. הריצה דיווחה
`cannot_tell` על כל פריים, וזה **סטטוס מתוכנן** — הוא נראה כמו ה־tripwire עושה
את שלו. הבדיקות היו ירוקות. ושתי הרשעות שהיו אמורות לתפוס את זה לא ירצו:
`AC6` "אין תשובות מומצאות" **לא יכול היה להיכשל** (`AICounters()` לא היה לו
קורא), והחשבון של המזהה לא נחשב בשום מקום (`detect.Counters` נבדק אבל `Observe`
לעולם לא נקרא). היום החוט מועמד משני הצדדים, `report.md` מספר **למה** אין
תקרות, והריצה הבאה על חומר אמיתי החזירה: 366 פריימים נשאלו ו־366 נענו, תקרה
אחת, קליפ שנאומת בקריאה חזרה, דחיפה עם תוצאה, **PASS**.

---

## מה נמדד: צורת המודל, הרישיון, והגבלות החומר

שלושה דברים שלא נמדדים בתוך ריצה — הם תכונות של **הבנייה** ושל **החומר**, זהים
לכל הרצה, והם שאלות שישאלו בחדר. כל אחד מהם רשום בקובץ שמחויב לצד הדבר שהוא
מתארים, כדי שדף אחד לא יוכל לטעון צורת מודל או רישיון שהרשומה שלידו לא תומכת
בו.

### המודל שנמפק מכנה `yolov8n.onnx` הוא **לא** YOLOv8

| | |
|---|---|
| **שדות לזיהוי** | 85 — כלומר יש `objectness` בעמודה 4. ל־YOLOv8 יש 84 ואין לו עמודת objectness |
| **פלטים** | שלושה, במדרגים 52 / 26 / 13 על קלט 416×416 ב־`NHWC` — כלומר **anchored**. YOLOv8 ממוזג, פלט אחד, ללא anchors |
| **שמות מחלקות** | **ריקים**. לייצוא אין מטא־נתונים של מחלקות, ולכן המפענח נופל חזרה למילון COCO־80 לפי אינדקס |
| **ספק ביצוע** | `CPUExecutionProvider` בלבד |

הכתובת המלאה: [`pyworker/fixtures/yolov8n.contract.json`](pyworker/fixtures/yolov8n.contract.json),
הופקה ע"י `pyworker/probe_model.py`.

### הרישיון של המודל **לא רשום**, וזה עוצר מכירה — לא את הדמו

המשקלות לא נשלפו מדף ההורדות הציבורי של Ultralytics, ובמאגר הזה **אין** שום
רשומה של מקורן. אם הן מ־Ultralytics — הרישיון הוא **AGPL-3.0**, ובעמוד הרישיון
שלהם עצמם כתוב ששימוש מסחרי ושימוש פנימי דורשים רישיון Enterprise. אם הן מ־
ONNX Model Zoo תחת רישיון מקליח — החובה היא לשמוך נקרא.

**את שניהם אי אפשר להבחין מכל מה שיש במאגר הזה**, ולכן אין כאן טענה לכיוון
הזה. הרשומה אומרת `NOT RECORDED` כי `NOT RECORDED` זה מה שנכון.

**מה נעשה עכשיו:** מדידה, על המכונה הזו, להערכת עצמית של ה־POC.
**מה לא נעשה עכשיו:** מכירה, הפצה מחוץ למכונה זו, או כל מוצר נגזר מהמשקלות.

הרשומה: [`pyworker/fixtures/yolov8n.licence.json`](pyworker/fixtures/yolov8n.licence.json).

**שמו לא השתנה.** שינוי שם בלי שינוי רישיון רק מזיז את הבעיה: הקובץ ייקרא
`yolov4.onnx` והרישיון יישאר לא רשום.

### החומר בדמו **אינו מורשה להיות מוצג**

`testdata/car-crossing.mp4` ו־`testdata/youtube/sTF-6_xinUU.mp4` שניהם בעלי
רישיון לא רשום ב־[`testdata/labels.json`](testdata/labels.json). הקליפ של יוטיוב
מסומן במפורש: **לא מורש להפצה, לפרסום או להדגמה לאף אחד מחוץ למכונה הזו**.

עד שהשדות האלה ימולאו, הדמו קריא על חומר **כאן בלבד**. זה לא עוצר את הדמו
עצמו, וזה יעצור אותו דווקא בחדר.

### נתיב ה־RTSP הוכח, אך **אף מצלמה לא היתה מחוברת**

`pocctl demo --rtsp` מפרסם את אותו קליפ דרך `mediamtx` וקורא אותו דרך סוקט
חי. זה מוכיח את **התעבורה**. מקור ההזנה על המסך, ביומן ובדוח הוא ביטוי
נגזר: `live rtsp (relay of recorded material, NOT a camera)`. שום מצלמה לא
חוברה לעולם, ושום מסמך כאן לא טוען אחרת.

---

## מה נמצא בבדיקת איכות (QA)

עשרה באגים נמצאו ע"י הרצת כל נתיבי הפקודה שלא נבדקו קודם — אף אחד לא היה נראה מהטסטים. כולם תוקנו, ולכל אחת מהם נוספה בדיקה שנשברת בלי התיקון.

| מה שבר | ההשלכה |
|---|---|
| `flagSet` קרא `os.Args[2:]` בעצמו | `poc soak status` ו־`poc soak report` נכשלו ב**כל** הרצה: `"status" is not a flag` |
| `pocctl soak start` העביר את כל הדגלים לשני תהליכים | `--data-root` נדחה ע"י preflight, והפקודה לא יכלה לעבוד בכלל |
| `soak start` היה סביבתי-עגולה לא יכולה | preflight דורש "תחזורת תקלות ירוקה", והתחזורת רצה אחריו — שער שאי אפשר לפתוח |
| `zone --pick` דרס קובץ zones ידני | **הרס את `testdata/car-crossing.zones.json`**. עכשיו הוא מסרב, ועם `--force` שומר את הישן כ־`.replaced` |
| `zone --pick` רנדר still כ־MJPEG | ffmpeg 8 מסרב לקודד YUV שאינו full-range — **הפיקר מעולם לא עבד**. עכשיו PNG |
| `watch --config` נכשל | הסריקה התעצבה *לפני* ש־os.Args נכתב מחדש, והבדיקה על סודות עברה **ריקה** כי הפקודה מתה לפני שהדפיס דבר |
| preflight היה לו **העתאה שנייה** של בדיקת המפענח | עמודת היכולת סיפרה `h264: present`, והשער אמר `ABSENT` — כי תו היכולת משתנה לפי build |
| מדידת שטח פנוי על תיקייה שעדיין לא נוצרה | SQLite מחזיר `out of memory (14)` — והשער דחה מכונה עם 900 GB פנויים בגלל "אין מספיק מקום" |
| מצב השעון דרוש דגל שני לאותו עובדה | מי שהגדיר `SP_POC_CLOCK_SOURCE` נדחה ע"י `--clock-state` |
| `xpsim` כבר היה על הפורט | התחזורת **של מישהו אחר** על הפורט, ולא בדקה כלל. שער שאפשר להכריע לפי מי שאקרא — אינו שער |
| עמודת `Why` הייתה ריקה בדיוק כשהבדיקה עברה | כלל שמפסיק להסביר את עצמו בדיוק כשאיש פחות ערנין לכך |

`pocctl soak start` עכשיו ירוק לגמרי: **תחזורת תקלות 7/7**, **preflight 11/11**, והוא מדפיס את פקודת ה־`watch` המדויקת להרצה.