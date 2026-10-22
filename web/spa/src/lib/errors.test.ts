import { describe, expect, it } from 'vitest'
import { describeError } from './errors'

// The error, in a sentence.
//
// The console prints this to an operator, not to a developer, so the two
// things that matter are that it is a sentence and that it is true. Every
// branch here exists because a real failure arrived in that shape: a JSON
// body from the product, the proxy's own wording for a product that did not
// answer, a body that was HTML, and a value that was not an error at all.

describe('describeError', () => {
  it('gives an Error its own message', () => {
    expect(describeError(new Error('the rule bundle is not active'))).toBe('the rule bundle is not active')
  })

  it('gives a string back unchanged', () => {
    expect(describeError('site_not_assigned')).toBe('site_not_assigned')
  })

  it('says nothing at all about a value that is neither an Error nor a string', () => {
    // `[object Object]` on the screen would read as a rendering bug, and a
    // number would read as a status code. Neither is a reason, so there is
    // no reason shown.
    expect(describeError(503)).toBe('')
    expect(describeError({ error: 'no_active_bundle' })).toBe('')
    expect(describeError(null)).toBe('')
    expect(describeError(undefined)).toBe('')
  })

  it('unwraps a JSON body that carries an error', () => {
    expect(describeError('{"error":"no_active_bundle"}')).toBe('no_active_bundle')
  })

  it('unwraps a JSON body that carries a message', () => {
    expect(describeError('{"message":"the site is not assigned"}')).toBe('the site is not assigned')
  })

  it('falls through to the raw text when the JSON body carries neither', () => {
    // A body with an `error` of a shape nobody expected is still text the
    // operator can be shown, and hiding it behind a blank would be worse.
    expect(describeError('{"code":"E_UNKNOWN"}')).toBe('{"code":"E_UNKNOWN"}')
  })

  it('gives the raw text when the body is not JSON at all', () => {
    expect(describeError('<html>502 Bad Gateway</html>')).toBe('<html>502 Bad Gateway</html>')
  })

  it('turns the proxy\'s own wording into a sentence about the product', () => {
    // The proxy says what IT could not reach. The operator needs to be told
    // that the PRODUCT did not answer, because that is the thing they can
    // act on.
    expect(describeError('the http://127.0.0.1:8443 upstream did not answer')).toBe(
      'The product at http://127.0.0.1:8443 did not answer. It may not be running.',
    )
  })

  it('matches the proxy\'s wording whatever case it arrives in', () => {
    expect(describeError('THE HTTP://127.0.0.1:8443 UPSTREAM DID NOT ANSWER')).toBe(
      'The product at HTTP://127.0.0.1:8443 did not answer. It may not be running.',
    )
  })

  it('matches the proxy\'s wording with text after it', () => {
    // The proxy adds its own timing to the sentence. The trailing words are
    // the proxy's, and they are not the operator's reason.
    expect(describeError('the http://127.0.0.1:8443 upstream did not answer within 5s')).toBe(
      'The product at http://127.0.0.1:8443 did not answer. It may not be running.',
    )
  })

  it('does not answer an empty error with an empty sentence', () => {
    // `{"error":""}` is a body with nothing in it. Returning the empty
    // string would show the operator a blank line that looks like a
    // rendering failure rather than a refusal that said nothing.
    expect(describeError('{"error":""}')).toBe('{"error":""}')
  })
})
