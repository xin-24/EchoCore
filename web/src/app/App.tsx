import { Component, useEffect, useState, type FormEvent, type ReactNode } from 'react'
import { Navigate, NavLink, Route, Routes, useNavigate } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import { api, errorText, setCSRF } from '../api/client'
import { Stickers } from '../features/stickers/Stickers'
import styles from './App.module.css'

class Boundary extends Component<{ children: ReactNode }, { failed: boolean }> {
  state = { failed: false }
  static getDerivedStateFromError() { return { failed: true } }
  render() { return this.state.failed ? <section className="panel"><h2>页面加载异常</h2><button onClick={() => location.reload()}>重新加载</button></section> : this.props.children }
}

export function App() {
  const [state, setState] = useState<'checking' | 'guest' | 'ready'>('checking')
  const [key, setKey] = useState(''), [error, setError] = useState(''), [pending, setPending] = useState(false)
  const [theme, setTheme] = useState(() => localStorage.getItem('echocore-theme') ?? (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'))
  const queryClient = useQueryClient(), navigate = useNavigate()
  useEffect(() => { document.documentElement.dataset.theme = theme; localStorage.setItem('echocore-theme', theme) }, [theme])
  useEffect(() => {
    const controller = new AbortController()
    const expire = () => { void queryClient.cancelQueries(); queryClient.clear(); setCSRF(''); setState('guest'); setKey('') }
    window.addEventListener('admin-expired', expire)
    api<{ csrf_token: string }>('/session', { signal: controller.signal }).then(data => { setCSRF(data.csrf_token); setState('ready') }).catch(() => { if (!controller.signal.aborted) setState('guest') })
    return () => { controller.abort(); window.removeEventListener('admin-expired', expire) }
  }, [queryClient])
  async function login(event: FormEvent) {
    event.preventDefault(); setPending(true); setError('')
    try { const data = await api<{ csrf_token: string }>('/login', { method: 'POST', body: JSON.stringify({ key }) }); setCSRF(data.csrf_token); setKey(''); setState('ready'); navigate('/stickers') } catch (e) { setError(errorText(e)) } finally { setPending(false) }
  }
  async function logout() {
    setPending(true); setError('')
    try { await api('/logout', { method: 'POST' }); await queryClient.cancelQueries(); queryClient.clear(); setCSRF(''); setState('guest'); navigate('/login') } catch (e) { setError(errorText(e)) } finally { setPending(false) }
  }
  const themeButton = <button className="quiet" onClick={() => setTheme(theme === 'dark' ? 'light' : 'dark')}>{theme === 'dark' ? '☀ 浅色模式' : '☾ 深色模式'}</button>
  if (state !== 'ready') return <main className={styles.login}><div className={styles.theme}>{themeButton}</div><div className={styles.loginCard}><div className={styles.logo}>E<span>↗</span></div><p className="eyebrow">ECHOCORE / LOCAL CONSOLE</p><h1>让管理回归简单。</h1><p className="muted">收藏、审核、整理。你的机器人素材工作台。</p>{state === 'checking' ? <p role="status">正在验证管理会话…</p> : <form onSubmit={login}><label>管理密钥<input type="password" autoComplete="current-password" value={key} onChange={e => setKey(e.target.value)} required maxLength={256} placeholder="输入 ECHOCORE_ADMIN_KEY" /></label><p className="hint">使用后端环境变量中的管理密钥，不是 NapCat Token。</p>{error && <p className="error" role="alert">{error}</p>}<button className="primary" disabled={pending || !key}>{pending ? '正在验证…' : '进入管理后台 →'}</button></form>}<p className={styles.localNote}>仅本机访问 · 密钥不会存入浏览器</p></div></main>
  return <div className={styles.layout}><aside className={styles.sidebar}><div className={styles.brand}><div className={styles.logo}>E<span>↗</span></div><div><strong>EchoCore</strong><small>机器人管理台</small></div></div><p className="eyebrow">日常管理</p><nav><NavLink to="/stickers">▧　表情库</NavLink></nav><div className={styles.roadmap}><p>先把表情整理好</p><small>运行概览、群控制与系统设置将按后续里程碑开放。</small></div><div className={styles.bottom}>{themeButton}<button className="quiet" disabled={pending} onClick={logout}>退出登录</button></div></aside><div className={styles.main}><header className={styles.top}><span>工作空间 <span className="muted"> / 素材管理</span></span><span className={styles.pill}>● 已建立管理会话</span></header>{error && <p className="error" role="alert">{error}</p>}<Boundary><Routes><Route path="/stickers" element={<Stickers />} /><Route path="/login" element={<Navigate to="/stickers" replace />} /><Route path="/" element={<Navigate to="/stickers" replace />} /><Route path="*" element={<section className="panel"><h1>页面不存在</h1><NavLink to="/stickers">返回表情库</NavLink></section>} /></Routes></Boundary><footer className={styles.footer}>EchoCore · 本机管理 <span>React 前端 / Go API</span></footer></div></div>
}
