// Package operatormetrics is the durable half of the operator-efficiency layer.
//
// `internal/observability` is the live half: a registry of counters and gauges, a
// set of objectives, and an exposition format. It answers "what is happening
// right now" and it is explicitly not the source of truth for a number anybody
// signs. The registry is a process's memory; it resets on restart, and a report
// built on it would restart too.
//
// This package is the source of truth. Every number here is a Postgres
// computation over the tables the product already writes, which is the rule
// `docs/roadmap/engineering/metrics-and-analytics.md` states outright: Postgres
// is the source of truth and the counter is for live SLO evaluation only. A gate
// artefact, a release note and a customer report all read these queries.
//
// What it exists to fix:
//
//   - `attention_minutes`, the North Star numerator, had no producer at all.
//     Nothing recorded when an operator STARTED paying attention: the dispositions
//     table records when they finished, the incidents table records when the
//     incident appeared, and the gap between them was a duration nobody wrote
//     down. The console now appends one `incident.opened` ledger row on the first
//     authenticated read of an incident detail page by a given session
//     (`internal/audit.ActionIncidentOpened`), and this package reads it.
//   - `mt_ti_seconds` and `mt_tv_seconds` were definitions with no computation.
//   - `duplicate_total` and `suppressed_total` are two verification states out of
//     five with no number anywhere, and they are the two an operator asks about
//     when they suspect the rules are eating incidents.
//   - `evidence_fetch_failed_total` had no durable source: the counter was live,
//     and the only durable record of a failed fetch is a dead-lettered job.
//
// Everything here is read-only. Nothing in this package writes, because a metric
// that changes what it measures is not a metric.
package operatormetrics
