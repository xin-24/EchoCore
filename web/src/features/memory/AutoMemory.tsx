import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, errorText } from '../../api/client'
import styles from './Memories.module.css'

type Scope = { platform: string; self_id: string; type: string; conversation_id: string }
type Status = { enabled: boolean; available: boolean; authorized: boolean; paused: boolean; running: boolean; queued: number; last_status: string; mode: string }
type Candidate = { id: number; subject_user_id: string; kind: string; topic: string; content: string; evidence: string; reason: string; candidate_revision?: number; revision: number }
type Suppression = { id: number; subject_user_id: string; topic: string; revision: number }

export function AutoMemory({ scope }: { scope: Scope }) {
  const client = useQueryClient()
  const [page, setPage] = useState(1), [suppressionPage, setSuppressionPage] = useState(1)
  const [error, setError] = useState(''), [pending, setPending] = useState(false)
  const [review, setReview] = useState<Candidate | null>(null), [content, setContent] = useState(''), [replace, setReplace] = useState('')
  const [release, setRelease] = useState<Suppression | null>(null), [confirmation, setConfirmation] = useState('')
  const q = new URLSearchParams(scope).toString()
  const status = useQuery({ queryKey: ['memory-auto', scope], queryFn: ({ signal }) => api<Status>(`/memories/auto/status?${q}`, { signal }), refetchInterval: 5000 })
  const candidates = useQuery({ queryKey: ['memory-candidates', scope, page], queryFn: ({ signal }) => api<{ items: Candidate[]; total: number }>(`/memories/candidates?${q}&page=${page}`, { signal }) })
  const suppressions = useQuery({ queryKey: ['memory-suppressions', scope, suppressionPage], queryFn: ({ signal }) => api<{ items: Suppression[]; total: number }>(`/memories/suppressions?${q}&page=${suppressionPage}`, { signal }) })
  async function act(path: string, extra: object = {}) {
    setPending(true); setError('')
    try {
      await api(path, { method: 'POST', body: JSON.stringify({ scope, ...extra }) })
      setReview(null); setRelease(null); setConfirmation('')
      await Promise.all(['memory-auto', 'memory-candidates', 'memory-suppressions', 'memories'].map(key => client.invalidateQueries({ queryKey: [key] })))
    } catch (e) { setError(errorText(e)) } finally { setPending(false) }
  }
  function approve() {
    if (!review) return
    const revisions: Record<string, number> = {}
    for (const item of replace.split(',').map(s => s.trim()).filter(Boolean)) {
      if (!/^[1-9]\d*:[1-9]\d*$/.test(item)) { setError('替换格式为旧记忆 ID:版本，多个用英文逗号分隔。'); return }
      const [id, revision] = item.split(':'); revisions[id] = Number(revision)
    }
    void act(`/memories/candidates/${review.id}/approve`, { candidate_revision: review.revision, content, replacement_revisions: revisions })
  }
  const s = status.data
  return <section className="panel" aria-label="自动记忆管理">
    <h2>自动提取与候选审核</h2>
    <p className="muted">仅收录 YAML 授权者的原始文字；不收录图片识别、工具结果和机器人发言。候选不参与召回。暂停会清理待处理原文和候选，已保存记忆仍可读取。</p>
    {status.error && <p role="alert">{errorText(status.error)}</p>}
    {s && <><p>自动提取：{s.enabled ? '已配置开启' : '关闭'} · 模型：{s.available ? '可用' : '未就绪'} · 范围：{s.authorized ? '已授权' : '未授权'} · {s.paused ? '已暂停' : '未暂停'}</p>
      <p>排队 {s.queued} 条 · {s.running ? '正在提取' : '空闲'} · 最近结果：{s.last_status || '暂无'} · 模式：{s.mode || '未配置'}</p>
      <div className={styles.actions}><button disabled={pending || s.paused} onClick={() => void act('/memories/auto/pause')}>暂停自动提取</button><button disabled={pending || !s.paused || !s.authorized || !s.available} onClick={() => void act('/memories/auto/resume')}>恢复自动提取</button><button disabled={pending || !s.queued || s.paused || !s.available} onClick={() => void act('/memories/auto/run')}>立即处理队列</button><button disabled={pending} onClick={() => { void candidates.refetch(); void suppressions.refetch() }}>刷新候选</button></div>
    </>}
    <h3>待审核候选（{candidates.data?.total ?? 0}）</h3>
    {candidates.error && <p role="alert">{errorText(candidates.error)}</p>}
    {candidates.data?.items.map(x => <article key={x.id} className="panel"><h4>候选 #{x.id} · {x.topic} · {x.reason}</h4><p className={styles.content}>{x.content}</p><p>归属：{x.subject_user_id} · 版本 {x.revision}</p><details><summary>查看来源证据（含原话）</summary><pre className={styles.content}>{x.evidence}</pre></details><button disabled={pending || !s?.available || s.paused} onClick={() => { setReview(x); setContent(x.content); setReplace('') }}>审核候选 #{x.id}</button>{' '}<button disabled={pending} onClick={() => void act(`/memories/candidates/${x.id}/reject`, { candidate_revision: x.revision })}>拒绝并抑制该主题</button></article>)}
    <div className={styles.actions}><button disabled={page <= 1} onClick={() => setPage(page - 1)}>候选上一页</button><button disabled={page * 20 >= (candidates.data?.total ?? 0)} onClick={() => setPage(page + 1)}>候选下一页</button></div>
    {review && <section role="dialog" aria-label="批准候选确认"><h3>批准候选 #{review.id}</h3><label>确认后的正文<textarea maxLength={300} value={content} onChange={e => setContent(e.target.value)} /></label><label>需明确替换的旧记忆 ID:版本<input placeholder="例如 12:1,15:2；无冲突留空" value={replace} onChange={e => setReplace(e.target.value)} /></label><p>批准后可被当前会话使用。请先核对下方已保存记忆；替换会删除指定旧条目。群关闭时不能批准。</p><button disabled={pending || !content.trim()} onClick={approve}>确认批准</button>{' '}<button onClick={() => setReview(null)}>取消审核</button></section>}
    <h3>已抑制主题（{suppressions.data?.total ?? 0}）</h3><p className="muted">删除或拒绝的主题不会自动重新学习。解除只允许未来新消息重新参与提取，不恢复旧资料。</p>
    {suppressions.error && <p role="alert">{errorText(suppressions.error)}</p>}
    {suppressions.data?.items.map(x => <p key={x.id}>{x.subject_user_id} · {x.topic} <button disabled={pending} onClick={() => { setRelease(x); setConfirmation('') }}>解除抑制 #{x.id}</button></p>)}
    <div className={styles.actions}><button disabled={suppressionPage <= 1} onClick={() => setSuppressionPage(suppressionPage - 1)}>抑制上一页</button><button disabled={suppressionPage * 20 >= (suppressions.data?.total ?? 0)} onClick={() => setSuppressionPage(suppressionPage + 1)}>抑制下一页</button></div>
    {release && <section role="dialog" aria-label="解除抑制确认"><label>输入 {scope.self_id}/{scope.type}/{scope.conversation_id} 确认<input value={confirmation} onChange={e => setConfirmation(e.target.value)} /></label><button disabled={pending || confirmation !== `${scope.self_id}/${scope.type}/${scope.conversation_id}`} onClick={() => void act(`/memories/suppressions/${release.id}/release`, { revision: release.revision, confirmation })}>确认解除</button>{' '}<button onClick={() => setRelease(null)}>取消解除</button></section>}
    {error && <p role="alert" className="error">{error}</p>}
  </section>
}
