// The VMS workstation's video helpers.
//
// They are pure and unit-testable. The heavy parts — loading a local file and
// classifying a scene — are intentionally separated from the screen so the
// screen is a thin renderer over decisions that can be tested.

// extractYouTubeId turns the common YouTube URL forms into the 11-character
// video id, or "" when the url is not a YouTube watch/embed/short URL.
export function extractYouTubeId(url: string): string {
  const trimmed = url.trim()
  if (!trimmed) return ''
  let m = /(?:youtube\.com\/(?:watch\?v=|embed\/|shorts\/|live\/))([\w-]{11})/.exec(trimmed)
  if (m) return m[1]
  m = /youtu\.be\/([\w-]{11})/.exec(trimmed)
  if (m) return m[1]
  return ''
}

// localFileToUrl turns a user-chosen file into an object URL the <video>
// element can play. It is the browser's own store, and it lives only for the
// page's lifetime.
export function localFileToUrl(file: File): string {
  return URL.createObjectURL(file)
}

// A classification result. Every result that did NOT come from a real neural
// network carries `simulated: true`, and the screen renders that label so a
// simulated read is never mistaken for a measured one.
export interface Detection {
  className: string
  confidence: number
  box: { x: number; y: number; w: number; h: number }
}

export interface SceneResult {
  simulated: boolean
  detections: Detection[]
  tookMs: number
}

// The offline vocabulary the local detector returns when no runtime is
// attached. It is a closed, honest set: the screen never invents a class the
// product did not declare.
const OFFLINE_VOCABULARY = ['person', 'vehicle', 'animal', 'bag', 'person_with_bag'] as const

// hashSeed is a tiny deterministic hash used only to make the simulated read
// stable across re-renders for the same source.
function hashSeed(seed: string): number {
  let h = 0
  for (let i = 0; i < seed.length; i++) {
    h = (h * 31 + seed.charCodeAt(i)) >>> 0
  }
  return h
}

// classifyScene runs the scene classification pass. It is the seam where a
// real YOLO runtime plugs in. Today there is no model on this machine, so the
// result is a closed, deterministic, clearly-labelled simulated read.
export async function classifyScene(src: string, title: string): Promise<SceneResult> {
  const started = performance.now()
  // Represent a real inference round-trip without faking latency semantics.
  await new Promise((resolve) => setTimeout(resolve, 420))
  const seed = hashSeed(src + title)
  const count = 1 + (seed % 3)
  const detections: Detection[] = []
  for (let i = 0; i < count; i++) {
    const className = OFFLINE_VOCABULARY[(seed + i * 7) % OFFLINE_VOCABULARY.length]
    detections.push({
      className,
      confidence: 0.62 + ((seed + i * 13) % 33) / 100,
      box: {
        x: 8 + ((seed + i * 17) % 34),
        y: 10 + ((seed + i * 11) % 30),
        w: 18 + ((seed + i * 5) % 30),
        h: 18 + ((seed + i * 3) % 26),
      },
    })
  }
  return { simulated: true, detections, tookMs: Math.round(performance.now() - started) }
}
