import { createRoot } from 'react-dom/client'
import { QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter } from 'react-router-dom'
import { queryClient } from './hooks/api'
import { Shell } from './components/Shell'
import './index.css'

const container = document.getElementById('root')
if (!container) {
  throw new Error('root element not found')
}
const root = createRoot(container)
root.render(
  <QueryClientProvider client={queryClient}>
    <BrowserRouter>
      <Shell />
    </BrowserRouter>
  </QueryClientProvider>
)
