import { useState, useEffect } from 'react'
import { Link } from 'react-router-dom'
import { apiFetch, useAccount } from '../context/AccountContext'

interface TagRow {
  id: string
  slug: string
  name: string
  kind: string
  use_count: number
  updated_at: string
}

interface TagHit {
  slug: string
  name: string
  kind: string
  confidence: number
}

interface TaggedMessage {
  message_id: string
  session_id: string
  content: string
  preview: string
  platform: string
  session_title: string
  created_at: string
  tags: TagHit[]
}

interface TagGroup {
  tag: TagRow
  messages: TaggedMessage[]
}

export default function TagsPage() {
  const { currentAccount } = useAccount()
  const [tags, setTags] = useState<TagRow[]>([])
  const [groups, setGroups] = useState<TagGroup[]>([])
  const [selected, setSelected] = useState<string | null>(null)
  const [detail, setDetail] = useState<TagGroup | null>(null)
  const [loading, setLoading] = useState(false)
  const [retaging, setRetaging] = useState(false)
  const [error, setError] = useState('')
  const [kindFilter, setKindFilter] = useState<'all' | 'category' | 'topic'>('all')

  useEffect(() => {
    if (!currentAccount) return
    setSelected(null)
    setDetail(null)
    void loadAll()
  }, [currentAccount?.id])

  const loadAll = async () => {
    setLoading(true)
    setError('')
    try {
      const [tagsRes, overviewRes] = await Promise.all([
        apiFetch('/v1/agent/tags?limit=80', {}, currentAccount?.id),
        apiFetch('/v1/agent/tags/overview?top=12&per_tag=5', {}, currentAccount?.id),
      ])
      if (tagsRes.ok) {
        const data = await tagsRes.json()
        setTags(data.tags || [])
      }
      if (overviewRes.ok) {
        const data = await overviewRes.json()
        setGroups(data.groups || [])
      }
    } catch (e) {
      console.error(e)
      setError('加载标签失败')
    } finally {
      setLoading(false)
    }
  }

  const openTag = async (slug: string) => {
    setSelected(slug)
    setLoading(true)
    setError('')
    try {
      const res = await apiFetch(`/v1/agent/tags/${encodeURIComponent(slug)}?limit=80`, {}, currentAccount?.id)
      const data = await res.json()
      if (!res.ok) {
        setError(data.error || '加载失败')
        setDetail(null)
        return
      }
      setDetail(data)
    } catch {
      setError('加载失败')
    } finally {
      setLoading(false)
    }
  }

  const retag = async () => {
    setRetaging(true)
    setError('')
    try {
      const res = await apiFetch('/v1/agent/tags/retag?limit=200', { method: 'POST' }, currentAccount?.id)
      const data = await res.json()
      if (!res.ok) {
        setError(data.error || '回填失败')
        return
      }
      await loadAll()
      if (selected) await openTag(selected)
    } catch {
      setError('回填失败')
    } finally {
      setRetaging(false)
    }
  }

  const filteredTags = tags.filter((t) => kindFilter === 'all' || t.kind === kindFilter)
  const formatDate = (s: string) => {
    const d = new Date(s)
    return d.toLocaleDateString() + ' ' + d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  }

  return (
    <div className="p-6 h-full flex flex-col min-h-0">
      <div className="mb-5 flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="text-base font-semibold text-foreground">问题标签</h2>
          <p className="text-[13px] text-muted-foreground mt-0.5">
            自动给用户问题分类打标签，并按标签整合展示
          </p>
        </div>
        <div className="flex items-center gap-2">
          <select
            value={kindFilter}
            onChange={(e) => setKindFilter(e.target.value as typeof kindFilter)}
            className="h-8 px-2 bg-card border border-border rounded-md text-[12px]"
          >
            <option value="all">全部类型</option>
            <option value="category">分类</option>
            <option value="topic">主题</option>
          </select>
          <button
            onClick={() => void retag()}
            disabled={retaging}
            className="h-8 px-3 text-[12px] border border-border rounded-md hover:bg-accent disabled:opacity-50"
          >
            {retaging ? '回填中…' : '回填历史问题'}
          </button>
          <button
            onClick={() => void loadAll()}
            className="h-8 px-3 text-[12px] border border-border rounded-md hover:bg-accent"
          >
            刷新
          </button>
        </div>
      </div>

      {error && <p className="mb-3 text-[12px] text-red-500">{error}</p>}

      <div className="flex gap-2 flex-wrap mb-5">
        {filteredTags.length === 0 && !loading && (
          <p className="text-[13px] text-muted-foreground">
            暂无标签。在 Chat / Code 提问后会自动生成；也可点「回填历史问题」。
          </p>
        )}
        {filteredTags.map((t) => (
          <button
            key={t.id}
            onClick={() => void openTag(t.slug)}
            className={`h-8 px-3 rounded-md text-[12px] border transition-colors ${
              selected === t.slug
                ? 'bg-primary text-primary-foreground border-primary'
                : 'bg-card border-border text-foreground hover:bg-accent'
            }`}
          >
            <span className="font-medium">{t.name}</span>
            <span className="ml-1.5 opacity-70">{t.use_count}</span>
            <span className="ml-1.5 text-[10px] opacity-60">{t.kind === 'category' ? '分类' : '主题'}</span>
          </button>
        ))}
      </div>

      <div className="flex-1 min-h-0 grid grid-cols-1 lg:grid-cols-2 gap-4">
        <div className="bg-card border border-border rounded-xl overflow-hidden flex flex-col min-h-0">
          <div className="px-4 py-3 border-b border-border">
            <h3 className="text-sm font-semibold text-foreground">
              {detail ? `「${detail.tag.name}」下的问题` : '标签整合预览'}
            </h3>
            <p className="text-[11px] text-muted-foreground mt-0.5">
              {detail
                ? `共关联 ${detail.tag.use_count} 次 · 展示最近问题`
                : '按热门标签汇总最近提问，点击标签查看全部'}
            </p>
          </div>
          <div className="flex-1 overflow-auto divide-y divide-border">
            {loading && <div className="p-4 text-[13px] text-muted-foreground">加载中…</div>}
            {!loading && detail && detail.messages.length === 0 && (
              <div className="p-4 text-[13px] text-muted-foreground">该标签下暂无问题</div>
            )}
            {!loading &&
              detail?.messages.map((m) => (
                <div key={m.message_id} className="px-4 py-3">
                  <p className="text-[13px] text-foreground leading-relaxed">{m.preview || m.content}</p>
                  <div className="mt-2 flex flex-wrap items-center gap-2 text-[11px] text-muted-foreground">
                    <span>{formatDate(m.created_at)}</span>
                    {m.session_title && <span>· {m.session_title}</span>}
                    <Link className="text-primary hover:underline" to="/sessions">
                      会话
                    </Link>
                    <div className="flex flex-wrap gap-1 w-full mt-1">
                      {(m.tags || []).map((tg) => (
                        <button
                          key={tg.slug}
                          onClick={() => void openTag(tg.slug)}
                          className="px-1.5 py-0.5 rounded bg-accent text-[10px] text-muted-foreground hover:text-foreground"
                        >
                          {tg.name}
                        </button>
                      ))}
                    </div>
                  </div>
                </div>
              ))}
            {!loading && !detail &&
              groups.map((g) => (
                <div key={g.tag.id} className="px-4 py-3">
                  <button
                    onClick={() => void openTag(g.tag.slug)}
                    className="text-sm font-medium text-foreground hover:text-primary"
                  >
                    {g.tag.name}
                    <span className="ml-2 text-[11px] font-normal text-muted-foreground">{g.tag.use_count}</span>
                  </button>
                  <ul className="mt-2 space-y-1.5">
                    {g.messages.map((m) => (
                      <li key={m.message_id} className="text-[12px] text-muted-foreground leading-snug">
                        · {m.preview}
                      </li>
                    ))}
                  </ul>
                </div>
              ))}
          </div>
        </div>

        <div className="bg-card border border-border rounded-xl overflow-hidden flex flex-col min-h-0">
          <div className="px-4 py-3 border-b border-border">
            <h3 className="text-sm font-semibold text-foreground">分类说明</h3>
            <p className="text-[11px] text-muted-foreground mt-0.5">
              基于关键词规则自动分类（不额外消耗模型 token）。可按标签回顾同类问题。
            </p>
          </div>
          <div className="flex-1 overflow-auto p-4 space-y-3 text-[13px] text-muted-foreground leading-relaxed">
            <p>
              <span className="text-foreground font-medium">分类标签</span>
              ：编程开发、调试排错、重构、审查、测试、认证、数据库、前后端、运维、AI、GitHub、工作区、文档等。
            </p>
            <p>
              <span className="text-foreground font-medium">主题标签</span>
              ：从问题中识别 Go / React / Docker / 语音 / 缓存 等技术主题。
            </p>
            <p>
              新提问会在写入消息时自动打标；历史消息可用右上角「回填历史问题」批量生成。
            </p>
            {selected && (
              <button
                onClick={() => {
                  setSelected(null)
                  setDetail(null)
                }}
                className="h-8 px-3 text-[12px] border border-border rounded-md hover:bg-accent text-foreground"
              >
                返回整合预览
              </button>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}
