---
name: milestone-probe
description: Turn a securityctl adapter-probe run against a real Milestone XProtect installation into a reviewed route-table change. Use when a probe report exists or needs reading, when a route's Unverified flag has evidence behind it, when someone asks what a confirmed or contradicted verdict means, or when preparing docs/vendor/milestone/route-audit.md verdicts. Also use before touching internal/milestone/routes.go, whose entries are assumptions about a vendor API nobody has reached. Not for writing adapter code.
---

# Milestone adapter probe

DoD item 1 for M1 is **All 8 Milestone adapter routes are `Unverified`; the
fixtures are synthetic.** It is the only open product item that a person with
access to a real installation can move today.

This skill is the procedure for that, and its whole content is one rule:

> **The probe never changes a route's `Unverified` flag. The flag is a claim a
> human makes after reading the report.** A probe that promoted its own findings
> would make the flag a statement the tool makes about itself.

`cmd/securityctl/adapter_probe.go` already refuses to do this. The failure mode
this skill exists to prevent is the opposite one: an agent reading the JSON,
clearing eight flags in one edit, and reporting DoD item 1 closed.

## What the probe gives you

```powershell
securityctl adapter-probe --base-url https://milestone.example:8080 --token "$SP_MILESTONE_TOKEN" --clock --json
```

Per route, one of four verdicts:

| Verdict | Means | What it does **not** mean |
|---|---|---|
| `confirmed` | the route answered in the declared shape | that the mapping is right — only that the endpoint exists and is shaped as expected |
| `contradicted` | it answered, but not in the shape the adapter accepts | that the route is absent. **This is the most valuable outcome** |
| `unreachable` | could not get there, or could not authenticate | anything at all about the route. Network, TLS, credentials and a wrong path all look identical |
| `skipped` | a write-side route, which a read probe will not touch | anything |

`--clock` additionally samples the vendor's clock and reports drift. That number
is real evidence about `internal/clock`, because `normalized_at` is drift-corrected
and every time-window predicate reads only that column.

## Procedure

### 1. Read the report before editing anything

Reproduce it in the record. `docs/vendor/milestone/route-audit.md` carries one
verdict per route and `route_audit_test.go` **fails if a route has none**. That
test is the thing keeping this honest, so it is the thing to satisfy.

### 2. Treat `contradicted` as the win

Five of the nine documented routes were already declared *wrong* by the audit and
corrected in `routes.go` — the prefix was documented and the **resource** was not.
`/System/UserGroups` and `/Alarms` are `NOT-ANSWERABLE-FROM-DOCS`: alarms appear
in `events-rest-v1.reference.md` as Configuration API objects that events
trigger, never as a feed to read.

A probe that contradicts five routes and confirms one has told this repository
more than a probe that confirms eight, because "confirmed" only ever means the
endpoint exists. **Say that in the record rather than reporting a count.**

### 3. Clearing a flag, one route at a time

For a route to lose `Unverified`, all of these have to be true, and each one is a
separate thing to verify:

- the route returned `confirmed`
- you can say **which** published document said so, or that no document did
- the field names the adapter reads were present in the response, not just the
  endpoint
- for a paginated route, the `_links`/`eventCount` shape the cursor depends on
  actually appeared

Then, in `internal/milestone/routes.go`: drop `Unverified: true`, and put the
**evidence** in `Notes` — the document and line, or the probe date and verdict.
A route with no flag and no note is indistinguishable from a route nobody checked.

**One route per edit.** Eight flags in one diff is not reviewable, and a reviewer
who finds one wrong has to reconstruct which seven were right.

### 4. What still does not follow from a probe

- **Fixtures are still synthetic.** The probe reached an API; it did not capture
  a real event. `R4` in `docs/threat-model.md` stands until someone captures.
- **`/eventTypes` GUIDs.** The adapter's vocabulary is names and a real server
  speaks GUIDs. A GUID this site did not answer for is **unresolved** — never
  guessed, never "probably motion". A defaulted type is a fabricated fact wearing
  a uniform, and it is invariant 3 in a different costume.
- **Clock drift is not a time-window fix.** A measured drift goes in the
  observation; it does not license reading `observed_at` instead of
  `normalized_at`.

## Rules

- **Never let a script or an agent clear the flag.** If asked to automate this,
  the honest answer is that the gate is the judgement, and a tool making it makes
  it worth nothing.
- **`unreachable` is not a negative result.** Never write it up as "route does
  not exist". Report it as not reached and name what was not ruled out.
- **Never contact the vendor.** `cmd/securityctl/acceptance.go` states it: the
  vendor is never contacted, the probe is pointed at an installation someone
  already runs. Nothing here authorises probing somebody else's server.
- **A token comes from the secret store or the environment, never a file and
  never a command line that lands in a shell history or a log.**
- **Do not run the probe without `--base-url` that you were given.** It has no
  default on purpose: a default here is a probe pointed at somebody else's server
  by a typo.

## What this does not close

DoD item 1 is one of four blockers on M1, and this is the only one a single
person with one installation can move. The others need a model and a GPU, a load
generator, and fourteen consecutive days at a site. Reporting item 1 closed does
not move M1, and `docs/status.md` should say which blocker is now closed rather
than that M1 is nearer.