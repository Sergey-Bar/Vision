import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { parse } from 'yaml'

// The console's own types, against the published contract.
//
// # Why this exists
//
// The console declares sixteen interfaces by hand, in two files, and until this
// test existed nothing tied either of them to `contracts/openapi/v1/openapi.yaml`.
// The types were written from reading the handlers, which is how a type
// becomes a rumour.
//
// The rumour this file found the first time it ran was `Principal.siteScope`.
// The console declared it a `string`; the product, the published contract, the
// hook, the identity page and the upload page all treated it as a list. The
// page maps over it, a string has no `.map`, and the identity screen went
// BLANK. The screen going blank is the console's most honest-looking failure
// mode, which is exactly why it needed a machine and not a careful reader.
//
// # What the rule is
//
// For every interface the console declares:
//
//   - if the contract's schema is CLOSED (`additionalProperties: false`), the
//     console's field set must EQUAL the contract's. A closed schema says
//     "this object has exactly these fields", so a console field the schema
//     does not have is a field the wire will not send.
//   - if the schema is OPEN, the console's field set must be a SUBSET of the
//     contract's. A field the console declares and the contract does not have
//     is the same defect in a different shape.
//   - and the TYPES must agree, wherever both sides say something specific.
//
// # The refusal ledger
//
// Two schemas are KNOWN to disagree, and they are listed below with the
// reason. A known disagreement is not an exemption: the test asserts that the
// disagreement is STILL THERE, so a fixed one fails the gate until the entry
// is deleted. That is `internal/capability`'s rule -- closing a refusal
// requires deleting the entry -- and it is the difference between a record of
// known debt and a hole in a gate.
//
// The two are the PRODUCT's drift, not the console's: `incidents.Summary`
// writes `ruleId`, `degradedReasons`, `ruleBundleVersion` and `policyVersion`,
// and the contract's closed `IncidentSummary` does not list them, while it
// does list `disposition`, `threatScore` and `threatReasonChain`, which the
// console's summary interface does not declare. The contract is stale, and
// changing it needs a human to read the diff -- `AGENTS.md` says so -- so the
// drift is recorded here rather than papered over.

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '../../..')

// The console's hand-declared interfaces, and the file each lives in.
const consoleSources = ['src/api.ts', 'src/hooks/product.ts']

// The interfaces the console declares that have NO counterpart in the
// published contract, and why.
//
// A closed list, because an open one is a list that grows until it means
// nothing. Each entry says what the shape IS, so a reader can tell a shape
// the contract has not caught up with from a shape the contract will never
// have.
const notOnTheWire: Record<string, string> = {
  AIRuntimeStatus:
    'the console reads the readiness probe, whose `checks.aiRuntime` shape the contract describes as prose rather than as a schema',
  AttentionItem:
    'the attention queue is the product\'s ranked list and the contract has no schema for it; `IncidentSummary.threatScore` and `threatReasonChain` carry the same facts and the queue adds the rank',
  Camera: 'the camera inventory is the console server\'s own shape, read from `/api/cameras` and not from the product',
  ChatParams: 'a request body the console sends; the contract describes the response, not this',
  ChatResponse:
    'the console server\'s own chat shape, read from `/api/chat`; the contract has no schema for the console server\'s own routes',
  Clip: 'the console server\'s own clip shape, read from `/api/clips`',
  ClockDrift:
    'the drift report is a console-only view of the clock skew the product records; the contract describes the event timestamps, not the report',
  CoverageRow: 'one row of the `Coverage` schema\'s `rows` array, which the contract types only as an array',
  CriticalStream:
    'the critical-stream list is the console server\'s own shape; the contract describes the incident, not this projection of it',
  Detection:
    'the VMS\'s detection box is the device\'s own shape, read through the console server\'s `/vms` proxy',
  DemoHafniaResponse:
    'the demo bridge response is a local demo shape served by `/api/demo-hafnia`; the contract has no schema for it',
  EventHit: 'one hit of the evidence search; the contract types the search response as a `hits` array',
  HandoverBrief: 'the brief artifact in its published `brief/v1` shape, which the contract carries as an opaque object',
  HealthState: 'the health shape the console reads; the contract\'s `Health` is the product\'s own health route',
  IncidentExplanation:
    'the incident\'s explanation; the contract\'s `Explanation` carries the same reasons under a different name',
  RuleBundle: 'the bundle metadata the console reads; the contract\'s `RuleBundleMeta` carries the same facts under a different name',
  SearchParams: 'a query the console sends; the contract describes the response, not this',
  SearchResponse: 'the evidence search response; the contract types it as an inline `hits` array with no schema of its own',
  VMSCamera: 'the VMS\'s own camera shape, read through the console server\'s `/vms` proxy',
  VMSState: 'the VMS\'s own detector state, read through the console server\'s `/vms` proxy',
}

// The schemas where the contract and the console are KNOWN to disagree.
//
// The rule about a known disagreement is the same as the rule about a known
// refusal: the test asserts the disagreement is STILL THERE, so a fix that
// lands without the entry being deleted fails the gate. A record that cannot
// go stale is a record; a record that silently becomes wrong is a lie with a
// table around it.
const knownDrift: Record<string, string> = {
  IncidentSummary:
    "the PRODUCT writes `ruleId`, `degradedReasons`, `ruleBundleVersion` and `policyVersion` -- `internal/incidents`'s Summary row -- and the contract's closed `IncidentSummary` does not list them, while it does list `disposition`, `threatScore` and `threatReasonChain`, which the console's summary interface does not declare. The contract is stale. Changing `contracts/` needs a human to read the diff, so the drift is recorded rather than edited",
  EvidenceHit:
    "the console's evidence hit carries `summary`, `clipUrl` and `evidenceId`, which the contract's closed `EvidenceHit` does not list, and the contract lists `zoneId` and `correlationId`, which the console does not declare. The contract is stale here too",
  IncidentDetail:
    "the PRODUCT sends ONE stored decision as `verification` -- `internal/incidents`'s `Detail.Verification` is a single `VerificationView` -- and the contract's `IncidentDetail` types it as `verifications`, an ARRAY of `VerificationResult`. The console follows the product. The contract is stale",
  DeadLetter:
    "the PRODUCT sends the attempt count as `attempts` and carries `siteId` -- `internal/api`'s dead-letter row -- and the contract names the count `attemptCount` and has no `siteId` at all. The console follows the product. The contract is stale",
}

interface ConsoleField {
  name: string
  ts: string
  optional: boolean
}

/**
 * Reads the top-level fields of every `export interface` in a TypeScript file.
 *
 * The nesting matters: `IncidentDetail` declares `verification`, `events`,
 * `observations`, `evidence` and `dispositions` as INLINE object types, and a
 * parser that took every `name: type` line would flatten them into the parent
 * and then compare the wrong thing.
 *
 * So the body is walked by line, and a line counts as a field only when the
 * brace depth is zero. The depth is counted AFTER the line has been judged,
 * because a field's own type may open a brace on the same line --
 * `events: Array<{` is a field, and a parser that counted its brace first
 * would drop it and then report that the contract has a field the console
 * does not, which is the opposite of the truth.
 */
function readInterfaces(source: string): Map<string, ConsoleField[]> {
  const out = new Map<string, ConsoleField[]>()
  const interfaceRe = /export interface (\w+)\s*\{/g
  let match: RegExpExecArray | null
  while ((match = interfaceRe.exec(source)) !== null) {
    const name = match[1]
    // Find the matching closing brace of the interface body.
    let depth = 1
    let i = interfaceRe.lastIndex
    const bodyStart = i
    while (i < source.length && depth > 0) {
      const ch = source[i]
      if (ch === '{') depth++
      else if (ch === '}') depth--
      i++
    }
    const body = source.slice(bodyStart, i - 1)
    interfaceRe.lastIndex = i

    const fields: ConsoleField[] = []
    let lineDepth = 0
    for (const rawLine of body.split('\n')) {
      const line = rawLine.trim()
      const isComment = line.startsWith('//') || line.startsWith('/*') || line.startsWith('*')
      if (lineDepth === 0 && !isComment && line) {
        const fieldMatch = /^(\w+)(\?)?:\s*(.+)$/.exec(line)
        if (fieldMatch) {
          fields.push({
            name: fieldMatch[1],
            optional: Boolean(fieldMatch[2]),
            ts: fieldMatch[3].trim(),
          })
        }
      }
      for (const ch of line) {
        if (ch === '{') lineDepth++
        else if (ch === '}') lineDepth--
      }
      if (lineDepth < 0) lineDepth = 0
    }
    out.set(name, fields)
  }
  return out
}

interface ContractSchema {
  type?: string
  enum?: string[]
  items?: Record<string, unknown>
  properties?: Record<string, ContractSchema>
  required?: string[]
  additionalProperties?: boolean
  allOf?: ContractSchema[]
  $ref?: string
  format?: string
  description?: string
  [key: string]: unknown
}

function loadContract(): Record<string, ContractSchema> {
  const raw = readFileSync(resolve(repoRoot, 'contracts/openapi/v1/openapi.yaml'), 'utf8')
  const doc = parse(raw) as { components: { schemas: Record<string, ContractSchema> } }
  return doc.components.schemas
}

/**
 * The full property set of a schema, with `allOf` resolved and `$ref` followed.
 *
 * `IncidentDetail` is `allOf: [IncidentSummary, {...}]`, so its property set
 * is the union of the two. A comparison that took only the inline half would
 * say the console declares `incidentId`, which the contract does not -- and
 * would be reporting a defect that does not exist.
 */
function contractFields(
  schema: ContractSchema,
  schemas: Record<string, ContractSchema>,
  seen = new Set<string>(),
): { fields: Set<string>; closed: boolean } {
  const fields = new Set<string>()
  if (schema.$ref) {
    const name = schema.$ref.split('/').pop() ?? ''
    if (seen.has(name)) return { fields, closed: false }
    seen.add(name)
    const target = schemas[name]
    if (!target) return { fields, closed: false }
    return contractFields(target, schemas, seen)
  }
  if (schema.allOf) {
    for (const part of schema.allOf) {
      const sub = contractFields(part, schemas, seen)
      sub.fields.forEach((f) => fields.add(f))
    }
  }
  if (schema.properties) {
    Object.keys(schema.properties).forEach((f) => fields.add(f))
  }
  // The closedness is the schema's OWN, and it is not inherited through
  // `allOf`. `IncidentDetail` is `allOf: [IncidentSummary, {...}]`, and
  // `IncidentSummary` is closed, so inheriting would say the detail is
  // closed too -- and then a closed detail would have to have EXACTLY the
  // summary's fields, which is the opposite of what `allOf` means. A detail
  // carries the summary AND more.
  return { fields, closed: schema.additionalProperties === false }
}

/**
 * Whether a TypeScript type and a contract schema say the same thing.
 *
 * `undefined` means "this comparison cannot be made" -- a `$ref`, a free-form
 * object, a union the contract does not spell out. An unmakeable comparison is
 * not a disagreement, and treating it as one would make this test cry wolf.
 */
function typeAgrees(ts: string, schema: ContractSchema): boolean | undefined {
  const nullable = ts.includes('| null')
  const base = ts.replace(/\s*\|\s*null/g, '').trim()
  const contractType = schema.type
  const contractTypes = Array.isArray(contractType) ? contractType : contractType ? [contractType] : []

  if (nullable) {
    const saysNull = contractTypes.includes('null') || (schema.$ref !== undefined)
    if (!saysNull && contractTypes.length > 0) return false
  }

  if (base.endsWith('[]')) {
    if (contractType !== 'array') return contractType === undefined ? undefined : false
    const items = schema.items
    if (!items) return undefined
    const itemBase = base.slice(0, -2)
    return typeAgrees(itemBase, items)
  }
  if (base === 'string') return contractTypes.length === 0 ? undefined : contractTypes.includes('string')
  if (base === 'number') {
    if (contractTypes.length === 0) return undefined
    return contractTypes.includes('number') || contractTypes.includes('integer')
  }
  if (base === 'boolean') {
    return contractTypes.length === 0 ? undefined : contractTypes.includes('boolean')
  }
  return undefined
}

describe('the console\'s types and the published contract', () => {
  const schemas = loadContract()
  const consoleInterfaces = new Map<string, ConsoleField[]>()
  for (const rel of consoleSources) {
    const source = readFileSync(resolve(repoRoot, 'web/spa', rel), 'utf8')
    for (const [name, fields] of readInterfaces(source)) {
      consoleInterfaces.set(name, fields)
    }
  }

  it('declares the interfaces the console is known to use', () => {
    // The control on the control: a parser that found nothing would make every
    // comparison below vacuous, and a vacuous comparison is a gate that
    // reports green forever.
    expect(consoleInterfaces.size).toBeGreaterThan(20)
    expect(consoleInterfaces.get('Principal')).toBeDefined()
  })

  it('classifies every interface the console declares', () => {
    // A closed list of the shapes that are not on the wire, and a closed list
    // of the known drifts. An interface that is in neither is one nobody has
    // decided about, and the gate says so rather than passing it.
    const unclassified: string[] = []
    for (const name of consoleInterfaces.keys()) {
      if (name in notOnTheWire) continue
      if (name in knownDrift) continue
      if (name in schemas) continue
      unclassified.push(name)
    }
    expect(unclassified).toEqual([])
  })

  it('agrees with the contract about every closed schema', () => {
    const disagreements: string[] = []
    for (const [name, fields] of consoleInterfaces) {
      const schema = schemas[name]
      if (!schema) continue
      const { fields: contractNames, closed } = contractFields(schema, schemas)
      if (!closed) continue
      const consoleNames = new Set(fields.map((f) => f.name))
      const missing = [...consoleNames].filter((f) => !contractNames.has(f))
      const extra = [...contractNames].filter((f) => !consoleNames.has(f))
      if (missing.length || extra.length) {
        disagreements.push(
          `${name}: console-only [${missing.join(', ')}] contract-only [${extra.join(', ')}]`,
        )
      }
    }
    // The known drifts are excluded here and asserted separately, below, so
    // that a drift which is FIXED does not silently become a pass: the
    // separate assertion is what fails when the fix lands.
    const real = disagreements.filter((d) => !(d.split(':')[0] in knownDrift))
    expect(real).toEqual([])
  })

  it('agrees with the contract about every open schema it declares', () => {
    const disagreements: string[] = []
    for (const [name, fields] of consoleInterfaces) {
      const schema = schemas[name]
      if (!schema) continue
      const { fields: contractNames, closed } = contractFields(schema, schemas)
      if (closed) continue
      const missing = fields.map((f) => f.name).filter((f) => !contractNames.has(f))
      if (missing.length) {
        disagreements.push(`${name}: the console declares [${missing.join(', ')}], which the contract does not`)
      }
    }
    const real = disagreements.filter((d) => !(d.split(':')[0] in knownDrift))
    expect(real).toEqual([])
  })

  it('agrees with the contract about the TYPES', () => {
    const disagreements: string[] = []
    for (const [name, fields] of consoleInterfaces) {
      const schema = schemas[name]
      if (!schema) continue
      const properties = schema.properties ?? {}
      for (const field of fields) {
        const property = properties[field.name]
        if (!property) continue
        const agrees = typeAgrees(field.ts, property)
        if (agrees === false) {
          disagreements.push(`${name}.${field.name}: the console says ${field.ts}, the contract says ${JSON.stringify(property)}`)
        }
      }
    }
    const real = disagreements.filter((d) => {
      const owner = d.split('.')[0]
      return !(owner in knownDrift)
    })
    expect(real).toEqual([])
  })

  it('still drifts exactly where the record says it does', () => {
    // The direction of the check matters. A known drift that is FIXED is a
    // FAILURE here, because the record has gone stale, and a record that has
    // gone stale is an exemption nobody will ever revisit. Closing a known
    // drift means deleting its entry AND the drift in the same commit, which
    // is what makes the two travel together.
    for (const [name, why] of Object.entries(knownDrift)) {
      expect(why.length, `${name} needs a reason`).toBeGreaterThan(40)
      const schema = schemas[name]
      expect(schema, `${name} is in the drift record and is not in the contract`).toBeDefined()
      const consoleFields = consoleInterfaces.get(name)
      expect(consoleFields, `${name} is in the drift record and the console does not declare it`).toBeDefined()
      if (!schema || !consoleFields) {
        // The two assertions above have already failed and named the problem.
        // Continuing would turn a clear message into a panic.
        continue
      }
      const { fields: contractNames } = contractFields(schema, schemas)
      const consoleNames = new Set(consoleFields.map((f) => f.name))
      const missing = [...consoleNames].filter((f) => !contractNames.has(f))
      const extra = [...contractNames].filter((f) => !consoleNames.has(f))
      expect(
        missing.length + extra.length,
        `${name} is in the drift record and no longer drifts: delete the entry and fix the ` +
          'other side in the same commit, or the record is a stale exemption',
      ).toBeGreaterThan(0)
    }
  })
})
