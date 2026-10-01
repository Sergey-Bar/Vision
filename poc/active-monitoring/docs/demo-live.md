# Running the demo against a LIVE camera and a LIVE XProtect

This is the document the earlier plan never had. Everything until now ran against a
local simulator, and the three claims that matter — *the region is measured*, *the
verdict is the model's*, *the event reached Milestone* — have never been checked
against the real thing.

Read the four refusals below before you run anything. A live demo writes into a real
alarm list, and a demo that writes an unverified alarm into a real alarm list is
worse than no demo.

## What changes, and what does not

| | `pocctl demo` | `pocctl demo --live` |
|---|---|---|
| Source | a labelled recording, looped | `SP_POC_SOURCE_ARG` — a real camera |
| Push target | `xpsim`, a simulator this repo runs | the XProtect instance in `SP_POC_XPROTECT_BASE_URL` |
| Trigger | the local novelty engine | the local novelty engine |
| Verifier | `none` unless configured | `SP_POC_VLM_PROVIDER=hafnia` or `none` |
| Alarm list | nothing | **somebody's** |
| Banner | `demo-replay` | `live` |

The trigger is the same in both. The novelty engine does not know what camera it is
looking at, and neither does anything else in the module except the recorder.

## Before you start: the four things that must already be true

```powershell
# 1. A push target. Not the simulator.
$env:SP_POC_XPROTECT_BASE_URL = 'https://<your-vms>/api/rest/v1'
$env:SP_POC_XPROTECT_CLIENT_ID = '...'
$env:SP_POC_XPROTECT_CLIENT_SECRET = '...'

# 2. The camera and the event type. The event type GUID is the one in your
#    Analytics Event Type, or in the vendor's event list. The camera GUID is the
#    one Milestone gave you for the device.
$env:SP_POC_CAMERA_GUID    = '<camera guid>'
$env:SP_POC_DEVICE_PATH    = '/Local/<camera guid>'
$env:SP_POC_EVENT_TYPE_GUID = '<event type guid>'

# 3. A live source, and a declared lawful basis for reading it.
$env:SP_POC_SOURCE_KIND    = 'rtsp'
$env:SP_POC_SOURCE_ARG     = 'rtsp://user:pass@<camera>:554/<path>'
$env:SP_POC_SOURCE_LICENCE = 'site-security-officer-authorised-2026-09-30'
$env:SP_POC_SOURCE_USER    = '<user>'
$env:SP_POC_SOURCE_PASSWORD= '<password>'

# 4. A verifier, or an honest decision to have none.
$env:SP_POC_VLM_PROVIDER  = 'hafnia'
$env:SP_POC_VLM_REGION    = 'eu'          # eu | us. THERE IS NO FAILOVER.
$env:SP_POC_VLM_API_KEY   = '<from the secret store>'
$env:SP_POC_VLM_MODEL     = 'cosmos-3-nano-16b'
$env:SP_POC_VLM_DAILY_CAP = '200'
```

`SP_POC_SOURCE_PASSWORD` and `SP_POC_VLM_API_KEY` come from the secret store. They are
never a flag, never in a file in this repository, and never in a screenshot — and the
demo prints the source URL with its credentials stripped, because that line lands in
a terminal that ends up in a photo of a monitor.

If any of these is missing, `pocctl demo --live` **refuses before it builds
anything**, and names each one together with what breaks without it. The first four
refusals are the ones that would otherwise produce a demo that looks live and is not.

## Run it

```powershell
pocctl demo --live
```

Seven steps, the same order as the offline demo, with one difference at step 5: no
simulator is started, because there is nothing to simulate.

At step 7 the demo reads the run back and prints four things:

1. `pocctl audit ledger` — the hash chain over every decision step, recomputed
2. `pocctl audit observations` — every model's answer, with its attribution, its
   output hash and — if there was no answer — a reason code
3. `pocctl prove <detection>` — the newest decision recomputed from stored facts,
   with the provider call count **measured**
4. then it tells you to go and look at the camera in Milestone XProtect

## What you should see in Milestone

Within a second or two of a detection:

- an **alarm** in the alarm list, on the alarm definition you configured
- a **bounding box** on the timeline. If the box is missing, stop: read
  `pocctl audit decisions` and compare the printed region with the one Milestone
  drew. They are the same number, and RC-6 exists because they used not to be.
- a **bookmark** on the recording. That is what puts an operator on the footage.
  Without `SP_POC_DEVICE_PATH` there is no bookmark and the demo says so, on every
  page, rather than looking complete.

## If the model is unavailable

The run continues. Every event becomes an **unverified proposal**, and every surface
says so in words:

- the overlay burns `VERDICT not asked` into the picture
- the report's truth line prints `model: hafnia` and `reason=cap_reached` per row
- the bookmark description says the model was not asked and why

That is the design. A degraded answer is never a negative answer: the system must
not tell an operator "nothing happened" because we could not ask.

## Region pinning

One site, one region, no failover.

During an outage the run degrades and **the footage stays on site**. There is
deliberately no code path that tries the other region's host, and RC-7 is the red
check that would go green if somebody added one. `pocctl preflight` prints the
exact host your footage is about to go to, every time.

## After the demo

```powershell
# Verify the chain, from the database, with no network at all.
pocctl audit ledger

# Sign the chain head, so the verification is attested rather than merely true.
# The key never leaves this machine; the public half does.
openssl genpkey -algorithm ed25519 -out demo-seal.pem
pocctl audit seal --key demo-seal.pem
openssl pkey -in demo-seal.pem -pubout -out demo-seal.pub

# On another machine, or next month:
pocctl audit seals --trusted-key demo-seal.pub

# Replay one decision from stored facts, with zero provider calls.
pocctl prove --run <run id>
```

A chain that verifies is not the same claim as a chain somebody signed. `pocctl`
prints which of the two you are looking at, and prints `NOT SEALED` rather than an
empty cell when there is no signature.

## The four refusals this document is for

The defects this work found were not in the interesting parts. They were refusals
that were missing:

- a pushed event carried a bounding box of all zeros, so Milestone drew nothing
  while the database said the region was real
- the bookmark path was a required configuration variable that no code read, so
  every event went out with no pointer to any recording
- the per-frame sidecar was a required dependency of a pipeline that does not use it
- the incident class was a hard-coded family enum, so the second class was a
  code change instead of a prompt file

Each is now a refusal with a name, and each has a red check in
`scripts\verify.ps1` that removes the refusal and asserts the suite goes red. Eleven
in total. If one of them ever goes green without the fix, the gate says so.