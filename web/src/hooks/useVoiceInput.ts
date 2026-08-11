import { useCallback, useEffect, useRef, useState } from 'react'
import { apiFetch } from '../context/AccountContext'

type SpeechRecognitionLike = {
  lang: string
  continuous: boolean
  interimResults: boolean
  maxAlternatives: number
  start: () => void
  stop: () => void
  abort: () => void
  onresult: ((ev: SpeechRecognitionEventLike) => void) | null
  onerror: ((ev: { error?: string }) => void) | null
  onend: (() => void) | null
}

type SpeechRecognitionEventLike = {
  resultIndex: number
  results: ArrayLike<{
    isFinal: boolean
    0: { transcript: string }
  }>
}

type SpeechRecognitionCtor = new () => SpeechRecognitionLike

function getSpeechRecognitionCtor(): SpeechRecognitionCtor | null {
  const w = window as Window & {
    SpeechRecognition?: SpeechRecognitionCtor
    webkitSpeechRecognition?: SpeechRecognitionCtor
  }
  return w.SpeechRecognition || w.webkitSpeechRecognition || null
}

export type VoiceEngine = 'browser' | 'server' | 'none'

export function detectVoiceEngine(): VoiceEngine {
  if (typeof window === 'undefined') return 'none'
  if (getSpeechRecognitionCtor()) return 'browser'
  if (typeof navigator !== 'undefined' && navigator.mediaDevices && typeof navigator.mediaDevices.getUserMedia === 'function') {
    return 'server'
  }
  return 'none'
}

export type UseVoiceInputOptions = {
  lang?: string
  accountId?: number
  /** Append recognized text into the composer */
  onTranscript: (text: string, meta: { final: boolean; engine: VoiceEngine }) => void
}

export function useVoiceInput(opts: UseVoiceInputOptions) {
  const [listening, setListening] = useState(false)
  const [supported, setSupported] = useState(true)
  const [engine, setEngine] = useState<VoiceEngine>('none')
  const [error, setError] = useState('')
  const [interim, setInterim] = useState('')
  const [serverEnabled, setServerEnabled] = useState(false)

  const recognitionRef = useRef<SpeechRecognitionLike | null>(null)
  const mediaRecorderRef = useRef<MediaRecorder | null>(null)
  const chunksRef = useRef<Blob[]>([])
  const streamRef = useRef<MediaStream | null>(null)
  const optsRef = useRef(opts)
  optsRef.current = opts

  useEffect(() => {
    const eng = detectVoiceEngine()
    setEngine(eng)
    setSupported(eng !== 'none')
  }, [])

  useEffect(() => {
    let cancelled = false
    ;(async () => {
      try {
        const res = await apiFetch('/v1/asr/status', {}, opts.accountId)
        if (!res.ok || cancelled) return
        const data = await res.json()
        if (!cancelled) setServerEnabled(!!data.enabled)
      } catch {
        // ignore — browser path still works
      }
    })()
    return () => {
      cancelled = true
    }
  }, [opts.accountId])

  const stopBrowser = useCallback(() => {
    const r = recognitionRef.current
    if (r) {
      try {
        r.onend = null
        r.stop()
      } catch {
        /* ignore */
      }
      recognitionRef.current = null
    }
    setListening(false)
    setInterim('')
  }, [])

  const stopServer = useCallback(async () => {
    const recorder = mediaRecorderRef.current
    if (recorder && recorder.state !== 'inactive') {
      await new Promise<void>((resolve) => {
        recorder.onstop = () => resolve()
        try {
          recorder.stop()
        } catch {
          resolve()
        }
      })
    }
    mediaRecorderRef.current = null
    streamRef.current?.getTracks().forEach((t) => t.stop())
    streamRef.current = null
    setListening(false)
  }, [])

  const stop = useCallback(async () => {
    stopBrowser()
    await stopServer()
  }, [stopBrowser, stopServer])

  const startBrowser = useCallback(() => {
    const Ctor = getSpeechRecognitionCtor()
    if (!Ctor) {
      setError('当前浏览器不支持语音识别，请使用 Chrome，或配置服务端 ASR')
      return false
    }
    setError('')
    const recognition = new Ctor()
    recognition.lang = optsRef.current.lang || 'zh-CN'
    recognition.continuous = true
    recognition.interimResults = true
    recognition.maxAlternatives = 1

    recognition.onresult = (event) => {
      let finalChunk = ''
      let interimChunk = ''
      for (let i = event.resultIndex; i < event.results.length; i++) {
        const piece = event.results[i][0]?.transcript || ''
        if (event.results[i].isFinal) finalChunk += piece
        else interimChunk += piece
      }
      setInterim(interimChunk)
      if (finalChunk.trim()) {
        optsRef.current.onTranscript(finalChunk.trim(), { final: true, engine: 'browser' })
      }
    }
    recognition.onerror = (ev) => {
      const code = ev.error || 'unknown'
      if (code === 'aborted' || code === 'no-speech') return
      setError(
        code === 'not-allowed'
          ? '麦克风权限被拒绝'
          : code === 'network'
            ? '浏览器语音服务需要网络（Chrome 会调用 Google）'
            : `语音识别错误: ${code}`,
      )
      setListening(false)
    }
    recognition.onend = () => {
      // Chrome often ends after a pause; keep listening if user hasn't stopped.
      if (recognitionRef.current === recognition) {
        try {
          recognition.start()
          return
        } catch {
          recognitionRef.current = null
          setListening(false)
          setInterim('')
        }
      }
    }

    recognitionRef.current = recognition
    recognition.start()
    setListening(true)
    setEngine('browser')
    return true
  }, [])

  const transcribeBlob = useCallback(
    async (blob: Blob) => {
      const form = new FormData()
      const ext = blob.type.includes('mp4') ? 'mp4' : blob.type.includes('ogg') ? 'ogg' : 'webm'
      form.append('file', blob, `speech.${ext}`)
      form.append('language', (optsRef.current.lang || 'zh-CN').split('-')[0] || 'zh')

      const res = await apiFetch(
        '/v1/asr',
        { method: 'POST', body: form },
        optsRef.current.accountId,
      )
      const data = await res.json().catch(() => ({}))
      if (!res.ok) {
        throw new Error(data.error || `ASR 失败 (${res.status})`)
      }
      const text = (data.text || '').trim()
      if (text) {
        optsRef.current.onTranscript(text, { final: true, engine: 'server' })
      }
    },
    [],
  )

  const startServer = useCallback(async () => {
    if (!serverEnabled) {
      setError('服务端 ASR 未启用。请用 Chrome 的浏览器语音，或在配置中开启 asr')
      return false
    }
    setError('')
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true })
      streamRef.current = stream
      chunksRef.current = []
      const mime = MediaRecorder.isTypeSupported('audio/webm;codecs=opus')
        ? 'audio/webm;codecs=opus'
        : MediaRecorder.isTypeSupported('audio/webm')
          ? 'audio/webm'
          : ''
      const recorder = mime ? new MediaRecorder(stream, { mimeType: mime }) : new MediaRecorder(stream)
      mediaRecorderRef.current = recorder
      recorder.ondataavailable = (e) => {
        if (e.data.size > 0) chunksRef.current.push(e.data)
      }
      recorder.onstop = async () => {
        const blob = new Blob(chunksRef.current, { type: recorder.mimeType || 'audio/webm' })
        chunksRef.current = []
        try {
          if (blob.size > 0) await transcribeBlob(blob)
        } catch (err) {
          setError(err instanceof Error ? err.message : 'ASR 转写失败')
        }
      }
      recorder.start(1000)
      setListening(true)
      setEngine('server')
      return true
    } catch {
      setError('无法访问麦克风')
      return false
    }
  }, [serverEnabled, transcribeBlob])

  const toggle = useCallback(async () => {
    if (listening) {
      await stop()
      return
    }
    // Prefer browser Web Speech; fall back to server recording.
    if (getSpeechRecognitionCtor()) {
      startBrowser()
      return
    }
    await startServer()
  }, [listening, stop, startBrowser, startServer])

  useEffect(() => {
    return () => {
      stopBrowser()
      streamRef.current?.getTracks().forEach((t) => t.stop())
    }
  }, [stopBrowser])

  return {
    listening,
    supported,
    engine,
    error,
    interim,
    serverEnabled,
    toggle,
    stop,
  }
}
