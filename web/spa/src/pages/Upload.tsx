import { useState } from 'react'
import { PageHeader, Page } from '@/components/Shell'
import { Upload, FileVideo, Play, Clock, Trash2, CheckCircle2, AlertCircle, Mic } from 'lucide-react'
import { cn } from '@/lib/utils'
import { useVideoYouTube } from '@/hooks/api'
import type { Camera } from '@/api'

function getUploadedVideos(): Camera[] {
  if (typeof window === 'undefined') return []
  try {
    const raw = localStorage.getItem('vision-uploaded-videos')
    if (!raw) return []
    return JSON.parse(raw)
  } catch {
    return []
  }
}

function getUploadedRules(): any[] {
  if (typeof window === 'undefined') return []
  try {
    const raw = localStorage.getItem('vision-uploaded-rules')
    if (!raw) return []
    return JSON.parse(raw)
  } catch {
    return []
  }
}

function saveUploadedRule(rule: any) {
  const existing = getUploadedRules()
  localStorage.setItem('vision-uploaded-rules', JSON.stringify([...existing, rule]))
}

export function UploadPage() {
  const [files, setFiles] = useState<{ file: File; status: 'pending' | 'uploading' | 'done' | 'error'; progress: number; result?: any; error?: string }[]>([])
  const [youtubeUrl, setYoutubeUrl] = useState('')
  const [youtubeStatus, setYoutubeStatus] = useState<'idle' | 'submitting' | 'done' | 'error'>('idle')
  const [youtubeResult, setYoutubeResult] = useState<any>(null)
  const [youtubeError, setYoutubeError] = useState('')
  const [isDragging, setIsDragging] = useState(false)

  const youtubeMutation = useVideoYouTube()
  const [ruleText, setRuleText] = useState('')
  const [ruleStatus, setRuleStatus] = useState<'idle' | 'compiling' | 'done' | 'error'>('idle')
  const [ruleResult, setRuleResult] = useState<string | null>(null)

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
    formData.append('siteId', 'site-default')

    try {
      const res = await fetch('/v1/video/upload', {
        method: 'POST',
        body: formData,
      })
      if (!res.ok) throw new Error('Upload failed')
      const result = await res.json()

      // Save to localStorage so Live page can show it as a tile
      const newCamera: Camera = {
        id: `upload-${result.videoId}`,
        name: item.file.name,
        siteId: 'site-default',
        status: 'online',
        rtsp: undefined,
      }
      const existing = getUploadedVideos()
      localStorage.setItem('vision-uploaded-videos', JSON.stringify([...existing, newCamera]))

      // Auto-compile detection rule from filename
      await compileRule(`person or vehicle in ${item.file.name}`)

      setFiles((prev) =>
        prev.map((f, i) => (i === index ? { ...f, status: 'done', progress: 100, result } : f)),
      )
    } catch (err: any) {
      setFiles((prev) =>
        prev.map((f, i) => (i === index ? { ...f, status: 'error', error: err.message ?? String(err) } : f)),
      )
    }
  }

  const handleYouTube = async () => {
    if (!youtubeUrl.trim()) return
    setYoutubeStatus('submitting')
    setYoutubeError('')
    try {
      const res = await youtubeMutation.mutateAsync({ url: youtubeUrl, siteId: 'site-default' })
      setYoutubeStatus('done')
      setYoutubeResult(res)

      // Save YouTube video as a tile source
      const newCamera: Camera = {
        id: `upload-${res.videoId}`,
        name: `YouTube: ${youtubeUrl}`,
        siteId: 'site-default',
        status: 'online',
        rtsp: undefined,
      }
      const existing = getUploadedVideos()
      localStorage.setItem('vision-uploaded-videos', JSON.stringify([...existing, newCamera]))

      // Auto-compile detection rule from URL
      await compileRule(`person or vehicle in ${youtubeUrl}`)
    } catch (err: any) {
      setYoutubeStatus('error')
      setYoutubeError(err.message ?? String(err))
    }
  }

  const compileRule = async (text: string) => {
    setRuleStatus('compiling')
    try {
      const res = await fetch('/v1/rules/from-text', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ text, siteId: 'site-default' }),
      })
      if (!res.ok) throw new Error('Rule compilation failed')
      const result = await res.json()
      setRuleStatus('done')
      setRuleResult(result.rule)
      saveUploadedRule({ text, rule: result.rule, createdAt: new Date().toISOString() })
    } catch (err: any) {
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
          'relative overflow-hidden border border-white/10 rounded-3xl p-10 bg-black/30 backdrop-blur-sm transition-all duration-300',
          isDragging ? 'border-apple-red bg-apple-red/10 shadow-xl shadow-apple-red/40' : 'hover:border-white/30 hover:bg-white/5',
        )}
        onDrop={handleDrop}
        onDragOver={handleDragOver}
        onDragLeave={handleDragLeave}
        >
          <div className="flex flex-col items-center gap-5">
            <div className={cn(
              'w-20 h-20 rounded-3xl bg-black/60 flex items-center justify-center text-text-dim transition-all duration-300',
              isDragging ? 'bg-apple-red text-white glow-accent' : '',
            )}>
              <Upload size={34} />
            </div>
            <p onClick={() => {
              const input = document.getElementById('file-input') as HTMLInputElement | null
              input?.click()
            }}
              className={cn(
                'flex flex-col items-center gap-3 cursor-pointer transition-all duration-300',
                isDragging ? 'text-apple-red font-black' : 'text-text',
              )}
            >
              {isDragging ? 'Drop files here' : 'Choose video file(s)'}
              <span className="text-xs text-text-dim font-black">MP4, MOV, AVI, WebM, MKV</span>
            </p>
            <input
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
                <div key={i} className="flex items-center justify-between gap-4 px-4 py-3 rounded-3xl border border-white/10 bg-black/40">
                  <div className="flex items-center gap-3">
                    <div className="w-10 h-10 rounded-2xl bg-black/40 flex items-center justify-center text-text-dim">
                      <FileVideo size={16} />
                    </div>
                    <div className="flex-1 min-w-0">
                      <div className="text-base font-black text-text truncate">{item.file.name}</div>
                      <div className="text-xs text-text-dim font-black">
                        {item.file.type} • {(item.file.size / 1024 / 1024).toFixed(2)} MB
                      </div>
                    </div>
                  </div>
                  <div className="flex items-center gap-3">
                    {item.status === 'uploading' && (
                      <div className="w-40 h-3 bg-white/10 rounded-full overflow-hidden">
                        <div
                          className="h-full bg-apple-red transition-all duration-300 shadow-lg shadow-apple-red/40"
                          style={{ width: `${item.progress}%` }}
                        />
                      </div>
                    )}
                    {item.status === 'done' && (
                      <>
                        <CheckCircle2 size={16} className="text-apple-green" />
                        <span className="text-xs text-apple-green font-black">Saved to Live grid</span>
                      </>
                    )}
                    {item.status === 'error' && (
                      <>
                        <AlertCircle size={16} className="text-apple-red" />
                        <span className="text-xs text-apple-red font-black">{item.error}</span>
                      </>
                    )}
                    {item.status === 'pending' && (
                      <button
                        onClick={() => startUpload(item, i)}
                        className="px-4 py-2 rounded-2xl border border-apple-red bg-apple-red text-black font-black hover:bg-apple-red-hover transition-all duration-200"
                      >
                        Start Upload
                      </button>
                    )}
                    <button
                      onClick={() => removeFile(i)}
                      className="p-2 text-text-dim hover:text-apple-red transition-colors duration-300"
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

        <section className="relative overflow-hidden border border-white/10 rounded-3xl p-10 bg-black/30 backdrop-blur-sm transition-all duration-300">
          <div className="flex flex-col items-center gap-5">
            <div className="w-20 h-20 rounded-3xl bg-black/60 flex items-center justify-center text-text-dim">
              <Play size={34} />
            </div>
            <p className="flex flex-col items-center gap-3 cursor-pointer text-text transition-all duration-300">
              Ingest YouTube URL
              <span className="text-xs text-text-dim font-black">Public YouTube videos only</span>
            </p>
            <div className="flex w-full max-w-2xl gap-4">
              <input
                type="text"
                placeholder="https://www.youtube.com/watch?v=..."
                value={youtubeUrl}
                onChange={(e) => setYoutubeUrl(e.target.value)}
                className="input flex-1"
              />
              <button
                onClick={handleYouTube}
                disabled={youtubeStatus === 'submitting'}
                className="btn-apple-red px-10"
              >
                {youtubeStatus === 'submitting' ? <Clock size={20} className="animate-spin mr-3" /> : <Play size={20} className="mr-3" />}
                Add
              </button>
            </div>
            {youtubeError && (
              <div className="mt-5 p-6 rounded-3xl bg-apple-red/10 border border-apple-red/30 text-apple-red text-sm font-medium shadow-lg shadow-apple-red/30">
                {youtubeError}
              </div>
            )}
            {youtubeResult && (
              <div className="mt-5 p-6 rounded-3xl bg-apple-green/10 border border-apple-green/30 text-apple-green text-sm font-medium shadow-lg shadow-apple-green/30">
                YouTube ingested: <code className="text-white font-mono">{youtubeResult.videoId}</code> ({youtubeResult.eventId})
              </div>
            )}
          </div>
        </section>

        <section className="relative overflow-hidden border border-white/10 rounded-3xl p-10 bg-black/30 backdrop-blur-sm transition-all duration-300">
          <div className="flex flex-col items-center gap-5">
            <div className="w-20 h-20 rounded-3xl bg-black/60 flex items-center justify-center text-text-dim">
              <Mic size={34} />
            </div>
            <p className="flex flex-col items-center gap-3 cursor-pointer text-text transition-all duration-300">
              Compile detection rule from text
              <span className="text-xs text-text-dim font-black">Natural language → AI rule</span>
            </p>
            <div className="flex w-full max-w-2xl gap-4">
              <input
                type="text"
                placeholder="e.g., 'person with bag at entrance' or 'vehicle in parking lot'"
                value={ruleText}
                onChange={(e) => setRuleText(e.target.value)}
                className="input flex-1"
              />
              <button
                onClick={() => ruleText.trim() && compileRule(ruleText)}
                disabled={ruleStatus === 'compiling'}
                className="btn-apple-red px-10"
              >
                {ruleStatus === 'compiling' ? <Clock size={20} className="animate-spin mr-3" /> : <Mic size={20} className="mr-3" />}
                Compile
              </button>
            </div>
            {ruleStatus === 'error' && (
              <div className="mt-4 p-4 rounded-3xl bg-apple-red/10 border border-apple-red/30 text-apple-red text-sm font-medium shadow-lg shadow-apple-red/30">
                Rule compilation failed
              </div>
            )}
            {ruleResult && (
              <div className="mt-4 p-6 rounded-3xl bg-apple-green/10 border border-apple-green/30 text-apple-green text-sm font-medium shadow-lg shadow-apple-green/30">
                <div className="flex items-center justify-between mb-2">
                  <span className="font-black">Compiled rule:</span>
                  <span className="text-xs text-text-dim">NLP → YAML</span>
                </div>
                <div className="bg-black/40 p-3 rounded-xl text-xs font-mono text-text-muted overflow-x-auto whitespace-pre-wrap">
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
