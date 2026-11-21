"use client"

import { useAuth } from "@/lib/auth-context"
import { LoginPage } from "@/components/auth/login-page"
import type { ReactNode } from "react"

/**
 * AuthGate 把根渲染分成四态：
 *   loading       还在探测 /auth/me
 *   unreachable   后端没连上（进程退出、代理 5xx、网络失败）— 不是登录
 *   anonymous     鉴权开启且未登录 — 显示登录页
 *   authenticated 已登录，或后端关闭了鉴权
 */
export function AuthGate({ children }: { children: ReactNode }) {
  const { status, retry } = useAuth()

  if (status === "loading") {
    return (
      <div className="flex min-h-screen items-center justify-center text-sm text-muted-foreground">
        加载中…
      </div>
    )
  }
  if (status === "unreachable") {
    return (
      <div className="flex min-h-screen items-center justify-center bg-background px-4">
        <div className="w-full max-w-sm space-y-3 text-center">
          <p className="text-base font-medium text-foreground">后端没有连上</p>
          <p className="text-sm text-muted-foreground">
            管理台暂时访问不到接口。这不是登录失败，当前认证默认关闭，没有需要填写的账号密码。
          </p>
          <button
            type="button"
            className="inline-flex h-9 items-center justify-center rounded-md bg-primary px-4 text-sm font-medium text-primary-foreground"
            onClick={retry}
          >
            重试
          </button>
        </div>
      </div>
    )
  }
  if (status === "anonymous") {
    return <LoginPage />
  }
  return <>{children}</>
}
