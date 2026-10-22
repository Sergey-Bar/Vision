import { Component, type ErrorInfo, type ReactNode } from 'react'

// The error boundary.
//
// There was none. A thrown render error in the console produced a blank
// white page, which is the worst possible outcome for a screen an operator
// is looking at while something is going wrong elsewhere.
//
// The boundary shows what failed in words, and it does not swallow the
// error: it re-throws in development so the console still shows the stack,
// and it renders a message in production, where a stack trace is a
// disclosure and a blank page is a dead end.
interface Props {
  children: ReactNode
}

interface State {
  error: Error | null
}

export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null }

  static getDerivedStateFromError(error: Error): State {
    return { error }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // The console has no telemetry, so the error is put on the console
    // rather than swallowed. An operator reading the browser's console is
    // the only place it goes.
    console.error('the console failed to render', error, info.componentStack)
  }

  render() {
    const { error } = this.state
    if (!error) return this.props.children

    return (
      <div className="mx-auto max-w-[60ch] px-6 py-20">
        <p className="font-mono text-[11px] font-medium uppercase tracking-[0.08em] text-bad-ink">
          <span aria-hidden="true">■ </span>
          This screen did not render
        </p>
        <h1 className="mt-3 text-[34px] font-semibold leading-[40px] tracking-[-0.025em] text-text">
          The console hit an error it could not recover from.
        </h1>
        <p className="mt-4 text-[15px] leading-[22px] text-text-muted">
          The product is unaffected: this is the console's own view failing, and the
          record it reads is still there. Reload the page to try again.
        </p>
        <pre className="mt-6 overflow-x-auto rounded border border-line bg-bg-panel p-4 font-mono text-[12px] leading-[18px] text-text-muted">
          {error.message}
        </pre>
      </div>
    )
  }
}
