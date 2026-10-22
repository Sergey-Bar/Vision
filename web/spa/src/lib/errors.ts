// The error, in a sentence.
//
// The console used to print the raw failure: a JSON body with an error
// field, a URL and a stack of internal names in it. That is a developer's
// message, on a screen an operator reads, and it teaches the reader to skip
// the error rather than read it.
//
// What the operator needs is a sentence and a way to act. The detail is
// still available - it is what the second line carries - but the first line
// is for a person.

/** The one-line reason, with the internal shape taken out. */
export function describeError(error: unknown): string {
  const raw = messageOf(error)

  // A proxy's body: {"error":"the http://127.0.0.1:8443 upstream did not answer..."}
  try {
    const parsed = JSON.parse(raw) as { error?: string; message?: string }
    const inner = parsed.error ?? parsed.message
    if (inner) return inner
  } catch {
    // Not JSON. The raw text is the message.
  }

  // The proxy's own wording, in a sentence.
  const upstream = upstreamRe.exec(raw)
  if (upstream) {
    return `The product at ${upstream[1]} did not answer. It may not be running.`
  }

  return raw
}

// upstreamRe is the proxy's own wording.
//
// It is a module-level expression because `String#match` compiles a new one on
// every call, and an error path is not where a console should be spending its
// allocations.
const upstreamRe = /the\s+(\S+)\s+upstream did not answer[^"]*/i

// messageOf is the message out of whatever was thrown.
//
// An Error and a string are the two things this console throws, and a string
// is the only other thing worth showing. Anything else is stringified to
// `[object Object]`, which is not a sentence and which the operator would
// read as a rendering bug.
function messageOf(error: unknown): string {
  if (error instanceof Error) return error.message
  if (typeof error === 'string') return error
  return ''
}
