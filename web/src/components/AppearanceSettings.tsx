import { useEffect, useState } from 'react'
import { Check, Loader2, Save } from 'lucide-react'
import { useAppearance, type HomeLayout } from '../context/AppearanceContext'

export default function AppearanceSettings() {
  const { homeLayout, loading, error, refresh, save } = useAppearance()
  const [selected, setSelected] = useState<HomeLayout>(homeLayout)
  const [saving, setSaving] = useState(false)
  const [message, setMessage] = useState('')
  const [saveError, setSaveError] = useState('')
  useEffect(() => setSelected(homeLayout), [homeLayout])

  const submit = async () => {
    setSaving(true)
    setMessage('')
    setSaveError('')
    try {
      await save(selected)
      setMessage('主页样式已保存，对所有账号生效')
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : '保存失败')
    } finally { setSaving(false) }
  }

  return (
    <section className="border-b border-border pb-6" aria-labelledby="appearance-title">
      <h3 id="appearance-title" className="text-sm font-semibold mb-4">主页样式</h3>
      <fieldset disabled={loading || saving || Boolean(error)} className="grid grid-cols-1 sm:grid-cols-2 gap-3">
        <legend className="sr-only">全站主页样式</legend>
        {([{ id: 'classic', name: '经典深色' }, { id: 'workbench', name: '浅色工作台' }] as const).map((item) => (
          <label key={item.id} className={`appearance-option ${selected === item.id ? 'is-selected' : ''}`}>
            <div className={`appearance-preview preview-${item.id}`} aria-hidden="true">
              <div className="preview-sidebar"><i /><i /><i /><i /></div>
              <div className="preview-content"><b /><div><i /><i /><i /></div><span /></div>
            </div>
            <span className="flex items-center gap-2 p-3 text-[13px]">
              <input type="radio" name="home-layout" value={item.id} checked={selected === item.id} onChange={() => { setSelected(item.id); setMessage('') }} />
              {item.name}
              {homeLayout === item.id && <Check className="ml-auto h-4 w-4 text-success" aria-label="当前样式" />}
            </span>
          </label>
        ))}
      </fieldset>
      <div className="mt-4 flex items-center gap-3 flex-wrap">
        <button type="button" disabled={loading || saving || Boolean(error) || selected === homeLayout} onClick={() => void submit()} className="inline-flex h-9 items-center gap-2 rounded-md bg-primary px-3 text-[13px] text-primary-foreground disabled:opacity-50">
          {saving ? <Loader2 className="h-4 w-4 animate-spin" /> : <Save className="h-4 w-4" />}保存样式
        </button>
        {message && <p role="status" className="text-[12px] text-success">{message}</p>}
        {(saveError || error) && <p role="alert" className="text-[12px] text-destructive">{saveError || error}</p>}
        {error && <button type="button" onClick={() => void refresh()} className="text-[12px] text-primary">重新加载</button>}
      </div>
    </section>
  )
}
