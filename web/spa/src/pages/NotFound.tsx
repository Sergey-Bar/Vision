import { Link } from 'react-router-dom'

export function NotFound() {
  return (
    <div className="flex flex-col items-center justify-center h-full text-center px-6">
      <div className="text-6xl text-text-muted mb-4">404</div>
      <h1 className="text-xl font-semibold text-text mb-2">Page not found</h1>
      <p className="text-text-muted mb-6">The route you requested doesn't exist in the console.</p>
      <Link to="/" className="px-4 py-2 rounded-md bg-accent text-[rgb(0,26,38)] font-semibold hover:bg-accent-hover transition-colors">
        Return to Live
      </Link>
    </div>
  )
}
