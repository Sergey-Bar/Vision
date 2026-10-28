---
name: Bug report
about: Something behaves in a way the invariants say it must not
title: ''
labels: ''
assignees: ''

---

<!--
This product's failure modes are not all the same, and the distinction matters
more than usual here. A missed real incident and a spurious alarm are not the
same severity even though both are "wrong". Please answer 1 and 2 accurately;
it is the first thing we triage on.
-->

## 1. What did you expect, and what happened instead

<!-- Be specific. "Wrong" is not reproducible. -->

## 2. Severity in this domain

Pick the one that fits. If you are unsure, say so — do not understate it to be
polite, and do not overstate it to be heard.

- [ ] **A real security event was missed.** An intrusion, a forced door, or a
      verified activity that the system did not surface.
- [ ] **An incident was invented.** The system reported something that did not
      happen, or escalated without evidence.
- [ ] **A decision cannot be explained.** The system did something and the audit
      trail does not account for it.
- [ ] **An audit or evidence problem.** A row will not verify, evidence is
      missing, or a checkpoint fails.
- [ ] **A correctness bug** with no security consequence.
- [ ] **Usability** — the operator cannot do the job the interface makes hard.

## 3. Reproduction

- Version or commit:
- Platform (Windows / Linux, version):
- Install type (dev / pilot / production):
- Milestone adapter, if relevant:
- Steps:
- What you saw:
- What you expected:

## 4. Evidence, if you have it

<!-- Logs are fine, but redact customer names, site names, badge or credential
     numbers, and anything that identifies a person. If a real incident is
     involved, say so in the issue and describe it in the abstract: we do not
     want customer footage in a public tracker, and this tracker is public. -->

## 5. Was the AI plane involved

- [ ] Yes — an AI observation or summary was involved
- [ ] No
- [ ] Unknown

If the AI plane was unavailable or degraded at the time, say so. "The model was
down" and "the model said no" are different bugs, and the product treats them
differently on purpose.

## 6. Anything else

<!-- A regression from a known-good version is worth saying explicitly. -->
