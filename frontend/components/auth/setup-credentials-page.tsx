"use client"

import { useEffect, useState, type FormEvent } from "react"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Button } from "@/components/ui/button"
import { useAuth } from "@/lib/auth-context"
import { useAppVersion } from "@/lib/queries"
import type { ApiError } from "@/lib/api"

export function SetupCredentialsPage() {
  const { username: currentUsername, completeCredentialSetup, logout } = useAuth()
  const appVersion = useAppVersion()
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [confirmPassword, setConfirmPassword] = useState("")
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const appTitle = appVersion.data?.title?.trim() || "UpstreamOps"

  useEffect(() => {
    document.title = appTitle
  }, [appTitle])

  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    setError(null)

    const nextUsername = username.trim()
    if (!nextUsername) {
      setError("请输入新账号")
      return
    }
    if (nextUsername.length > 64) {
      setError("账号长度不能超过 64 位")
      return
    }
    if (password.trim().length === 0 || password.length < 6) {
      setError("新密码长度至少 6 位")
      return
    }
    if (password === "admin_ops") {
      setError("新密码不能继续使用默认密码")
      return
    }
    if (password !== confirmPassword) {
      setError("两次输入的密码不一致")
      return
    }

    setSubmitting(true)
    try {
      await completeCredentialSetup(nextUsername, password)
    } catch (err) {
      const apiErr = err as ApiError
      setError(apiErr.message || "保存失败，请重试")
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-background px-4">
      <Card className="w-full max-w-sm py-6">
        <CardHeader className="space-y-1.5">
          <CardTitle className="text-xl">{appTitle} · 首次登录初始化</CardTitle>
          <CardDescription>
            当前账号（{currentUsername || "admin"}）仍在使用默认初始密码，请重新设置管理员账号和密码后再进入系统。
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={handleSubmit} className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="setup-username">新账号</Label>
              <Input
                id="setup-username"
                name="username"
                autoComplete="username"
                placeholder="请输入新管理员账号（如 admin 或自定义用户名）"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                required
                disabled={submitting}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="setup-password">新密码</Label>
              <Input
                id="setup-password"
                name="password"
                type="password"
                autoComplete="new-password"
                placeholder="至少 6 位，不能使用默认密码"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
                disabled={submitting}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="setup-confirm-password">确认新密码</Label>
              <Input
                id="setup-confirm-password"
                name="confirmPassword"
                type="password"
                autoComplete="new-password"
                placeholder="再次输入新密码"
                value={confirmPassword}
                onChange={(e) => setConfirmPassword(e.target.value)}
                required
                disabled={submitting}
              />
            </div>
            {error ? (
              <p className="text-sm text-destructive" role="alert">
                {error}
              </p>
            ) : null}
            <div className="space-y-2">
              <Button type="submit" className="w-full" disabled={submitting}>
                {submitting ? "保存中…" : "保存并进入系统"}
              </Button>
              <Button
                type="button"
                variant="outline"
                className="w-full"
                disabled={submitting}
                onClick={logout}
              >
                返回登录
              </Button>
            </div>
          </form>
        </CardContent>
      </Card>
    </div>
  )
}
