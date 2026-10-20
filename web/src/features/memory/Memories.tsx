import { useState, type FormEvent } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, errorText } from '../../api/client'
import styles from './Memories.module.css'
import { AutoMemory } from './AutoMemory'

type Scope = { platform: string; self_id: string; type: string; conversation_id: string }
type Entry = { id: number; scope: Scope; subject_user_id: string; kind: string; content: string; source_type: string; source_message_id: string; updated_at: number; expires_at: number; revision: number }
type MemoryPage = { items: Entry[]; total: number; allowed: boolean }
const kinds: Record<string, string> = { fact: '事实（90天）', preference: '偏好（长期）', temporary: '临时（7天）', agreement: '群约定（90天）' }
const label = (s: Scope) => `${s.self_id}/${s.type}/${s.conversation_id}`
const date = (n: number) => n ? new Date(n * 1000).toLocaleString() : '长期'

export function Memories() {
  const client = useQueryClient()
  const scopes = useQuery({ queryKey: ['memory-scopes'], queryFn: ({ signal }) => api<{ enabled: boolean; scopes: Scope[] }>('/memories/scopes', { signal }) })
  const [draft, setDraft] = useState<Scope>({ platform: 'qq', self_id: '', type: 'private', conversation_id: '' })
  const [scope, setScope] = useState<Scope | null>(null), [page, setPage] = useState(1)
  const [kind, setKind] = useState(''), [subject, setSubject] = useState(''), [status, setStatus] = useState('')
  const [error, setError] = useState(''), [notice, setNotice] = useState(''), [pending, setPending] = useState(false)
  const [editing, setEditing] = useState<Entry | null>(null), [content, setContent] = useState(''), [addKind, setAddKind] = useState('fact'), [addSubject, setAddSubject] = useState('')
  const [deleting, setDeleting] = useState<Entry | 'scope' | null>(null), [confirmation, setConfirmation] = useState('')
  const query = new URLSearchParams({ ...(scope ?? {}), page: String(page), kind, subject, status })
  const entries = useQuery({ queryKey: ['memories', scope, page, kind, subject, status], enabled: scope !== null && scopes.data?.enabled === true, queryFn: ({ signal }) => api<MemoryPage>(`/memories?${query}`, { signal }) })

  function select(s: Scope) { setScope(s); setDraft(s); setPage(1); setEditing(null); setContent(''); setAddKind('fact'); setAddSubject(''); setDeleting(null); setConfirmation(''); setError(''); setNotice('') }
  async function mutate(path: string, method: string, body: unknown) {
    setPending(true); setError(''); setNotice('')
    try {
      await api(path, { method, body: JSON.stringify(body) })
      setNotice('操作完成。修改或删除会清理对应短期上下文，并取消使用旧记忆的回复。')
      setEditing(null); setContent(''); setDeleting(null); setConfirmation('')
      await Promise.all([client.invalidateQueries({ queryKey: ['memories'] }), client.invalidateQueries({ queryKey: ['memory-scopes'] })])
    } catch (e) { setError(errorText(e)); await client.invalidateQueries({ queryKey: ['memories'] }) }
    finally { setPending(false) }
  }
  function save(e: FormEvent) {
    e.preventDefault()
    if (!scope) return
    if (editing) void mutate(`/memories/${editing.id}`, 'PATCH', { scope, revision: editing.revision, content })
    else void mutate('/memories', 'POST', { scope, kind: addKind, subject_user_id: scope.type === 'private' ? scope.conversation_id : addSubject, content })
  }
  if (scopes.isPending) return <p role="status">正在加载记忆配置…</p>
  if (scopes.error) return <p role="alert" className="error">{errorText(scopes.error)}</p>
  return <section className={styles.page}>
    <header><p className="eyebrow">MEMORY / 受控保存</p><h1>长期记忆</h1><p className="muted">支持明确保存与独立授权的后台自动提取。低风险资料可自动保存，冲突进入候选审核；当前使用关键词召回，不依赖 Embedding。</p></header>
    <aside className="panel">本机管理员可查看和清理数据库内全部会话的记忆，包括私聊。群内记忆可被本群使用；不会跨会话共享。启用后，相关资料可能发送给聊天模型。请勿保存密钥或敏感隐私。</aside>
    {!scopes.data?.enabled ? <p role="status">长期记忆未启用。请在 config.yaml 配置 memory.enabled 和授权范围后重启 Go。</p> : <>
      <form className="panel" onSubmit={e => { e.preventDefault(); select({ ...draft }) }}>
        <fieldset disabled={pending} className={styles.fields}><legend>选择明确范围</legend>
          <label>已有会话<select value="" onChange={e => { const item = scopes.data?.scopes[Number(e.target.value)]; if (item) select(item) }}><option value="">选择已有会话，或手动填写</option>{scopes.data.scopes.map((s, i) => <option key={label(s)} value={i}>{label(s)}</option>)}</select></label>
          <label>机器人 QQ<input required pattern="[1-9][0-9]*" maxLength={19} value={draft.self_id} onChange={e => setDraft({ ...draft, self_id: e.target.value })} /></label>
          <label>会话类型<select value={draft.type} onChange={e => setDraft({ ...draft, type: e.target.value })}><option value="private">私聊</option><option value="group">群聊</option></select></label>
          <label>对方 QQ / 群号<input required pattern="[1-9][0-9]*" maxLength={19} value={draft.conversation_id} onChange={e => setDraft({ ...draft, conversation_id: e.target.value })} /></label>
          <button className="primary">查看范围</button>
        </fieldset>
      </form>
      {scope && <><h2>当前范围：{label(scope)}</h2>
        <AutoMemory key={label(scope)} scope={scope} />
        <fieldset disabled={pending} className={styles.fields}><legend>筛选</legend>
          <label>类别<select aria-label="类别" value={kind} onChange={e => { setKind(e.target.value); setPage(1) }}><option value="">全部类别</option>{Object.entries(kinds).map(([k, v]) => <option key={k} value={k}>{v}</option>)}</select></label>
          <label>归属 QQ<input maxLength={19} value={subject} onChange={e => { setSubject(e.target.value); setPage(1) }} /></label>
          <label>状态<select value={status} onChange={e => { setStatus(e.target.value); setPage(1) }}><option value="">全部状态</option><option value="active">有效</option><option value="expired">已过期</option></select></label>
        </fieldset>
        {entries.isPending && <p role="status">正在读取…</p>}{entries.error && <p role="alert" className="error">{errorText(entries.error)}</p>}
        {entries.data && <><p>共 {entries.data.total} 条 · 第 {page} 页 · {entries.data.allowed ? '已授权模型读取' : '当前未授权，仅供管理员整理'}</p>
          <div className={styles.cards}>{entries.data.items.map(item => <article className="panel" key={item.id}>
            <h3>#{item.id} · {kinds[item.kind]} · v{item.revision}</h3><p className={styles.content}>{item.content}</p>
            <p className="muted">归属：{item.subject_user_id || '本群约定'} · 来源：{item.source_type} {item.source_message_id && ` / 消息 ${item.source_message_id}`}</p>
            <p className="muted">更新：{date(item.updated_at)} · 有效期：{date(item.expires_at)} {item.expires_at > 0 && item.expires_at * 1000 <= Date.now() && '（已过期，不召回）'}</p>
            <button disabled={pending} onClick={() => { setEditing(item); setContent(item.content) }}>编辑 #{item.id}</button>{' '}<button disabled={pending} onClick={() => setDeleting(item)}>删除 #{item.id}</button>
          </article>)}</div>
          {!entries.data.items.length && <p>本页没有记忆。</p>}
          <div className={styles.actions}><button disabled={pending || page <= 1} onClick={() => setPage(page - 1)}>上一页</button><button disabled={pending || page * 20 >= entries.data.total} onClick={() => setPage(page + 1)}>下一页</button><button disabled={pending} onClick={() => { setDeleting('scope'); setConfirmation('') }}>清空当前范围</button></div>
          {(editing || entries.data.allowed) && <form className="panel" onSubmit={save}><fieldset disabled={pending} className={styles.editor}><legend>{editing ? `修改 #${editing.id}（有效期不变）` : '明确保存新记忆'}</legend>
            {!editing && <><label>保存类别<select value={addKind} onChange={e => setAddKind(e.target.value)}>{Object.entries(kinds).filter(([k]) => scope.type === 'group' || k !== 'agreement').map(([k,v]) => <option key={k} value={k}>{v}</option>)}</select></label>{scope.type === 'group' && addKind !== 'agreement' && <label>归属用户 QQ<input required pattern="[1-9][0-9]*" maxLength={19} value={addSubject} onChange={e => setAddSubject(e.target.value)} /></label>}</>}
            <label>记忆正文<textarea required maxLength={300} rows={4} value={content} onChange={e => setContent(e.target.value)} /></label>
            <button className="primary" disabled={!content.trim()}>保存记忆</button>{editing && <button type="button" onClick={() => { setEditing(null); setContent('') }}>取消编辑</button>}
          </fieldset></form>}
        </>}
        {deleting && <section className="panel" role="dialog" aria-label="删除记忆确认"><h3>确认删除{deleting === 'scope' ? `范围 ${label(scope)} 的全部记忆` : `记忆 #${deleting.id}`}？</h3><p>正文将删除，旧回复任务会取消；已发送给模型的内容、外部备份无法撤回。不会影响其他范围。</p>
          {deleting === 'scope' && <label>输入 {label(scope)} 确认<input value={confirmation} onChange={e => setConfirmation(e.target.value)} /></label>}
          <button disabled={pending || (deleting === 'scope' && confirmation !== label(scope))} onClick={() => deleting === 'scope' ? void mutate('/memories/clear', 'POST', { scope, confirmation }) : void mutate(`/memories/${deleting.id}`, 'DELETE', { scope, revision: deleting.revision })}>确认删除</button>{' '}<button disabled={pending} onClick={() => setDeleting(null)}>取消</button>
        </section>}
      </>}
    </>}
    {error && <p role="alert" className="error">{error}</p>}{notice && <p role="status">{notice}</p>}
  </section>
}
