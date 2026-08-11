type VoiceInputButtonProps = {
  listening: boolean
  supported: boolean
  disabled?: boolean
  title?: string
  onClick: () => void
}

export default function VoiceInputButton({
  listening,
  supported,
  disabled,
  title,
  onClick,
}: VoiceInputButtonProps) {
  if (!supported) return null

  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      title={title || (listening ? '停止语音输入' : '语音输入')}
      aria-pressed={listening}
      className={`h-10 w-10 flex items-center justify-center rounded-lg border transition-colors disabled:opacity-50 ${
        listening
          ? 'border-red-500/60 bg-red-500/10 text-red-500'
          : 'border-border bg-card text-muted-foreground hover:bg-accent hover:text-foreground'
      }`}
    >
      {listening ? (
        <span className="relative flex h-3 w-3">
          <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-red-400 opacity-60" />
          <span className="relative inline-flex rounded-full h-3 w-3 bg-red-500" />
        </span>
      ) : (
        <svg
          width="16"
          height="16"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
          aria-hidden
        >
          <path d="M12 1a3 3 0 0 0-3 3v8a3 3 0 0 0 6 0V4a3 3 0 0 0-3-3z" />
          <path d="M19 10v2a7 7 0 0 1-14 0v-2" />
          <line x1="12" y1="19" x2="12" y2="23" />
          <line x1="8" y1="23" x2="16" y2="23" />
        </svg>
      )}
    </button>
  )
}
