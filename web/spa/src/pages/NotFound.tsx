import { Link } from 'react-router-dom'

export function NotFound() {
  return (
    <div className="flex flex-col items-center justify-center h-full text-center px-10">
    <div className="text-9xl font-semibold text-text-muted mb-8">404</div>
    <h1 className="text-4xl font-semibold text-text tracking-tight mb-4">Page not found</h1>
      <p className="text-xl text-text-muted mb-12 max-w-xl">The route you requested doesn't exist in the console.</p>
      <Link to="/" className="px-10 py-5 rounded-lg bg-accent text-text font-semibold hover:bg-accent-strong transition-colors duration-2">
        Return to Live
      </Link>
    </div>
  )
}
