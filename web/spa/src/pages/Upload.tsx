import { useState } from 'react'
import { PageHeader, Page } from '@/components/Shell'
import { Upload, FileVideo, Play, Clock, Trash2, CheckCircle2, AlertCircle, Mic } from 'lucide-react'
import { cn } from '@/lib/utils'
import { Input } from '@/components/ui/Field'
import { Button } from '@/components/ui/Button'
import { useVideoYouTube } from '@/hooks/api'
import { describeError } from '@/lib/errors'
import type { RulesFromTextResponse, VideoUploadResponse, VideoYouTubeResponse } from '@/api'
import { usePrincipal } from '@/hooks/product'

// The upload page writes to the PRODUCT and to nothing else.
//
// It used to keep a list of "uploaded videos" and "uploaded rules" in the
// browser's localStorage, and the Live, Clips and Incidents pages read that
// list and drew tiles, clips and incidents from it. Every one of those was a
// thing that existed only in that browser: a camera with a name somebody typed
// and a status the browser chose, a clip with the time the page was opened,
// and an incident whose severity came out of a round-robin over a list.
//
// A console that keeps its own records beside the product's is a console with
// two truths, and the one in the browser is the one a reader cannot check.
export function UploadPage() {
  const [files, setFiles] = useState<{
    file: File
    status: 'pending' | 'uploading' | 'done' | 'error'
    progress: number
    result?: VideoUploadResponse
    error?: string
  }[]>([])
  const [youtubeUrl, setYoutubeUrl] = useState('')
  const [youtubeStatus, setYoutubeStatus] = useState<'idle' | 'submitting' | 'done' | 'error'>('idle')
  const [youtubeResult, setYoutubeResult] = useState<VideoYouTubeResponse | null>(null)
  const [youtubeError, setYoutubeError] = useState('')
  const [isDragging, setIsDragging] = useState(false)

  const youtubeMutation = useVideoYouTube()
  const [ruleText, setRuleText] = useState('')
  const [ruleStatus, setRuleStatus] = useState<'idle' | 'compiling' | 'done' | 'error'>('idle')
  const [ruleResult, setRuleResult] = useState<string | null>(null)

  // The site the ingest belongs to is the PRODUCT's site, read from the
  // principal the API is configured to act as. It used to be the literal
  // "site-default", which is a site id no install has, so every upload was
  // addressed to a place that does not exist.
  const { data: principal } = usePrincipal()
  const siteId = principal?.siteScope?.[0] ?? ''

  const handleFiles = (selected: FileList | null) => {
    if (!selected) return
    const newItems: typeof files = []
    for (let i = 0; i < selected.length; i++) {
      newItems.push({ file: selected.item(i)!, status: 'pending', progress: 0 })
    }
    setFiles((prev) => [...prev, ...newItems])
  }

  const handleDrop = (e: React.DragEvent) => {
    e.preventDefault()
    setIsDragging(false)
    handleFiles(e.dataTransfer.files)
  }

  const handleDragOver = (e: React.DragEvent) => {
    e.preventDefault()
    setIsDragging(true)
  }

  const handleDragLeave = () => {
    setIsDragging(false)
  }

  const startUpload = async (item: typeof files[number], index: number) => {
    setFiles((prev) =>
      prev.map((f, i) => (i === index ? { ...f, status: 'uploading', progress: 5 } : f)),
    )
    const formData = new FormData()
    formData.append('file', item.file)
    formData.append('siteId', siteId)

    try {
      const res = await fetch('/v1/video/upload', {
        method: 'POST',
        body: formData,
      })
      if (!res.ok) throw new Error('Upload failed')
      const result = (await res.json()) as VideoUploadResponse

      // The product has the file now. It becomes a camera when the VMS's
      // source registrar picks the ingest up, and it becomes a live view when
      // the VMS is playing it -- neither of which is the browser's to decide,
      // and neither of which is a tile this page may draw on its own.
      setFiles((prev) =>
        prev.map((f, i) => (i === index ? { ...f, status: 'done', progress: 100, result } : f)),
      )
    } catch (err: unknown) {
      setFiles((prev) =>
        prev.map((f, i) =>
          i === index ? { ...f, status: 'error', error: describeError(err) } : f,
        ),
      )
    }
  }

  const handleYouTube = async () => {
    if (!youtubeUrl.trim()) return
    setYoutubeStatus('submitting')
    setYoutubeError('')
    try {
      const res = await youtubeMutation.mutateAsync({ url: youtubeUrl, siteId })
      setYoutubeStatus('done')
      setYoutubeResult(res)
    } catch (err: unknown) {
      setYoutubeStatus('error')
      setYoutubeError(describeError(err))
    }
  }

  const compileRule = async (text: string) => {
    setRuleStatus('compiling')
    try {
      const res = await fetch('/v1/rules/from-text', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ text, siteId }),
      })
      if (!res.ok) throw new Error('Rule compilation failed')
      const result = (await res.json()) as RulesFromTextResponse
      setRuleStatus('done')
      setRuleResult(result.rule)
    } catch (err: unknown) {
      setRuleStatus('error')
      console.error('Rule compilation error:', err)
    }
  }

  const removeFile = (index: number) => {
    setFiles((prev) => prev.filter((_, i) => i !== index))
  }

  return (
    <Page>
      <PageHeader title="Upload" subtitle="Video ingest — local file or YouTube" />
      <div className="space-y-10">
        <section className={cn(
          'relative overflow-hidden border border-line rounded-lg p-10 bg-bg-panel  transition-colors duration-2',
          isDragging ? 'border-bad-ink bg-bad-ink/10  ' : 'hover:border-white/40 hover:bg-bg-elevated',
        )}
        onDrop={handleDrop}
        onDragOver={handleDragOver}
        onDragLeave={handleDragLeave}
        >
          <div className="flex flex-col items-center gap-5">
            <div className={cn(
              'w-20 h-20 rounded-lg bg-bg flex items-center justify-center text-text-dim transition-colors duration-2',
              isDragging ? 'bg-bad-ink text-text' : '',
            )}>
              <Upload size={34} />
            </div>
            {/* The chooser is a BUTTON and not a paragraph with a click
                handler. A paragraph is not reachable by keyboard, and the
                page's only way to pick a file would then be the mouse. */}
            <button
              type="button"
              onClick={() => {
                const input = document.getElementById('file-input') as HTMLInputElement | null
                input?.click()
              }}
              className={cn(
                'flex flex-col items-center gap-3 cursor-pointer transition-colors duration-2',
                isDragging ? 'text-bad-ink font-semibold' : 'text-text',
              )}
            >
              {isDragging ? 'Drop files here' : 'Choose video file(s)'}
              <span className="text-xs text-text-dim font-semibold">MP4, MOV, AVI, WebM, MKV</span>
            </button>
            <Input
              id="file-input"
              type="file"
              accept="video/*"
              multiple
              onChange={(e) => handleFiles(e.target.files)}
              className="sr-only"
            />
          </div>
          {files.length > 0 && (
            <div className="mt-6 space-y-3">
              {files.map((item, i) => (
                <div key={i} className="flex items-center justify-between gap-4 px-4 py-3 rounded-lg border border-line bg-bg-panel">
                  <div className="flex items-center gap-3">
                    <div className="w-10 h-10 rounded-lg bg-bg-panel flex items-center justify-center text-text-dim">
                      <FileVideo size={16} />
                    </div>
                    <div className="flex-1 min-w-0">
                    <div className="text-base font-semibold text-text truncate">{item.file.name}</div>
                    <div className="text-xs text-text-dim font-semibold">
                    {item.file.type} • {(item.file.size / 1024 / 1024).toFixed(2)} MB
                      </div>
                    </div>
                  </div>
                  <div className="flex items-center gap-3">
                    {item.status === 'uploading' && (
                      <div className="w-40 h-3 bg-bg-elevated rounded-full overflow-hidden">
                        <div
                          className="h-full bg-bad-ink transition-colors duration-2"
                          style={{ width: `${item.progress}%` }}
                        />
                      </div>
                    )}
                    {item.status === 'done' && (
                      <>
                        <CheckCircle2 size={16} className="text-ok-ink" />
                        <span className="text-xs text-ok-ink font-semibold">Accepted by the product</span>
                      </>
                    )}
                    {item.status === 'error' && (
                      <>
                        <AlertCircle size={16} className="text-bad-ink" />
                        <span className="text-xs text-bad-ink font-semibold">{item.error}</span>
                      </>
                    )}
                    {item.status === 'pending' && (
                      <button
                        onClick={() => void startUpload(item, i)}
                        className="px-4 py-2 rounded-lg border border-bad-ink bg-bad-ink text-text font-semibold hover:bg-bad-ink/80 transition-colors duration-1"
                      >
                        Start Upload
                      </button>
                    )}
                    <button
                      onClick={() => removeFile(i)}
                      className="p-2 text-text-dim hover:text-bad-ink transition-colors duration-2"
                      aria-label="Remove"
                    >
                      <Trash2 size={16} />
                    </button>
                  </div>
                </div>
              ))}
            </div>
          )}
        </section>

        <section className="relative overflow-hidden border border-line rounded-lg p-10 bg-bg-panel transition-colors duration-2">
          <div className="flex flex-col items-center gap-5">
            <div className="w-20 h-20 rounded-lg bg-bg flex items-center justify-center text-text-dim">
              <Play size={34} />
            </div>
            <p className="flex flex-col items-center gap-3 cursor-pointer text-text transition-colors duration-2">
              Ingest YouTube URL
              <span className="text-xs text-text-dim font-semibold">Public YouTube videos only</span>
            </p>
            <div className="flex w-full max-w-2xl gap-4">
              <Input
                type="text"
                placeholder="https://www.youtube.com/watch?v=..."
                value={youtubeUrl}
                onChange={(e) => setYoutubeUrl(e.target.value)}
                className="flex-1"
              />
              <Button
                onClick={() => void handleYouTube()}
                disabled={youtubeStatus === 'submitting'}
                variant="primary"
                className="px-10"
              >
                {youtubeStatus === 'submitting' ? <Clock size={20} className="animate-spin mr-3" /> : <Play size={20} className="mr-3" />}
                Add
              </Button>
            </div>
            {youtubeError && (
              <div className="mt-5 p-6 rounded-lg bg-bad-ink/10 border border-bad-ink/40 text-bad-ink text-sm font-medium">
                {youtubeError}
              </div>
            )}
            {youtubeResult && (
              <div className="mt-5 p-6 rounded-lg bg-ok-ink/10 border border-ok-ink/40 text-ok-ink text-sm font-medium">
                YouTube ingested: <code className="text-text font-mono">{youtubeResult.videoId}</code> ({youtubeResult.eventId})
              </div>
            )}
          </div>
        </section>

        <section className="relative overflow-hidden border border-line rounded-lg p-10 bg-bg-panel transition-colors duration-2">
          <div className="flex flex-col items-center gap-5">
            <div className="w-20 h-20 rounded-lg bg-bg flex items-center justify-center text-text-dim">
              <Mic size={34} />
            </div>
            <p className="flex flex-col items-center gap-3 cursor-pointer text-text transition-colors duration-2">
              Compile detection rule from text
              <span className="text-xs text-text-dim font-semibold">Natural language → AI rule</span>
            </p>
            <div className="flex w-full max-w-2xl gap-4">
              <Input
                type="text"
                placeholder="e.g., 'person with bag at entrance' or 'vehicle in parking lot'"
                value={ruleText}
                onChange={(e) => setRuleText(e.target.value)}
                className="flex-1"
              />
              <Button
                onClick={() => void (ruleText.trim() && compileRule(ruleText))}
                disabled={ruleStatus === 'compiling'}
                variant="primary"
                className="px-10"
              >
                {ruleStatus === 'compiling' ? <Clock size={20} className="animate-spin mr-3" /> : <Mic size={20} className="mr-3" />}
                Compile
              </Button>
            </div>
            {ruleStatus === 'error' && (
              <div className="mt-4 p-4 rounded-lg bg-bad-ink/10 border border-bad-ink/40 text-bad-ink text-sm font-medium">
                Rule compilation failed
              </div>
            )}
            {ruleResult && (
              <div className="mt-4 p-6 rounded-lg bg-ok-ink/10 border border-ok-ink/40 text-ok-ink text-sm font-medium">
                <div className="flex items-center justify-between mb-2">
                <span className="font-semibold">Compiled rule:</span>
                <span className="text-xs text-text-dim">NLP → YAML</span>
                </div>
                <div className="bg-bg-panel p-3 rounded-xl text-xs font-mono text-text-muted overflow-x-auto whitespace-pre-wrap">
                  {ruleResult}
                </div>
              </div>
            )}
          </div>
        </section>
      </div>
    </Page>
  )
}
