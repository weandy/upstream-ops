"use client"

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react"
import {
  apiFetch,
  setToken,
  setUnauthorizedHandler,
  type ApiError,
} from "@/lib/api"

type AuthStatus = "loading" | "anonymous" | "authenticated" | "unreachable"

interface AuthContextValue {
  status: AuthStatus
  username: string | null
  /** 后端关闭了鉴权（AUTH_ENABLED=false），整套 UI 当作"已登录"渲染。 */
  authDisabled: boolean
  /** 重新探测 /auth/me。后端暂时连不上时用，不要把它当成登录。 */
  retry: () => void
  login: (username: string, password: string) => Promise<void>
  logout: () => void
}

const AuthContext = createContext<AuthContextValue | null>(null)

interface LoginResponse {
  token?: string
  expires_at?: number
  username: string
  auth_disabled?: boolean
}

interface MeResponse {
  username: string
  auth_disabled?: boolean
}

export function AuthProvider({ children }: { children: ReactNode }) {
  // 启动时无论有没有 token 都先 /auth/me 探测一次，因为后端可能开了"无鉴权模式"。
  const [status, setStatus] = useState<AuthStatus>("loading")
  const [username, setUsername] = useState<string | null>(null)
  const [authDisabled, setAuthDisabled] = useState(false)
  const [probeGeneration, setProbeGeneration] = useState(0)

  useEffect(() => {
    let cancelled = false
    apiFetch<MeResponse>("/auth/me", { skipAuthErrorHandler: true })
      .then((me) => {
        if (cancelled) return
        if (me.auth_disabled) {
          // 后端关了鉴权：清掉本地任何遗留 token，避免下次开启时困惑
          setToken(null)
          setAuthDisabled(true)
          setUsername(me.username)
          setStatus("authenticated")
          return
        }
        // 后端开启鉴权：me 成功说明现有 token 仍有效
        setAuthDisabled(false)
        setUsername(me.username)
        setStatus("authenticated")
      })
      .catch((err: unknown) => {
        if (cancelled) return
        // 只有 401 才表示鉴权开启且未登录。后端进程挂了、代理 5xx、网络失败都不是登录问题。
        const statusCode = (err as ApiError | undefined)?.status
        if (statusCode === 401) {
          setToken(null)
          setAuthDisabled(false)
          setUsername(null)
          setStatus("anonymous")
          return
        }
        setStatus("unreachable")
      })
    return () => {
      cancelled = true
    }
  }, [probeGeneration])

  // 注册全局 401 回调：让 apiFetch 在任何业务请求 401 时把我们打回登录页。
  // 鉴权关闭时不可能拿到 401，这里也无害。
  useEffect(() => {
    setUnauthorizedHandler(() => {
      setUsername(null)
      setStatus("anonymous")
    })
    return () => setUnauthorizedHandler(null)
  }, [])

  const retry = useCallback(() => {
    setStatus("loading")
    setProbeGeneration((n) => n + 1)
  }, [])

  const login = useCallback(async (u: string, p: string) => {
    const res = await apiFetch<LoginResponse>("/auth/login", {
      method: "POST",
      body: JSON.stringify({ username: u, password: p }),
      skipAuthErrorHandler: true,
    })
    if (res.token) {
      setToken(res.token)
    }
    if (res.auth_disabled) {
      setAuthDisabled(true)
    }
    setUsername(res.username)
    setStatus("authenticated")
  }, [])

  const logout = useCallback(() => {
    // 鉴权关闭时 logout 按钮在 UI 上不会展示，这里仍保留兜底逻辑
    apiFetch("/auth/logout", { method: "POST" }).catch(() => {})
    setToken(null)
    setUsername(null)
    setStatus("anonymous")
  }, [])

  const value = useMemo(
    () => ({ status, username, authDisabled, retry, login, logout }),
    [status, username, authDisabled, retry, login, logout],
  )
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext)
  if (!ctx) {
    throw new Error("useAuth must be used within AuthProvider")
  }
  return ctx
}
