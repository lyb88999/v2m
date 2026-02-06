import { useEffect, useMemo, useState } from "react"
import {
  ArrowRight,
  CloudDownload,
  Loader2,
  Sparkles,
  Wand2,
} from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Progress } from "@/components/ui/progress"
import { Separator } from "@/components/ui/separator"

type JobStatus =
  | "queued"
  | "downloading"
  | "transcoding"
  | "ready"
  | "failed"
  | "expired"

type Job = {
  job_id: string
  source_url: string
  platform: string
  status: JobStatus
  error?: string
  mp3_url?: string
  created_at: string
  updated_at: string
}

type JobListResponse = {
  jobs: Job[]
}

const statusLabel: Record<JobStatus, string> = {
  queued: "已进入队列",
  downloading: "正在下载",
  transcoding: "正在转码",
  ready: "已完成",
  failed: "失败",
  expired: "已过期",
}

const statusProgress: Record<JobStatus, number> = {
  queued: 15,
  downloading: 45,
  transcoding: 75,
  ready: 100,
  failed: 100,
  expired: 100,
}

const statusTone: Record<JobStatus, string> = {
  queued: "bg-amber-100 text-amber-800",
  downloading: "bg-sky-100 text-sky-800",
  transcoding: "bg-orange-100 text-orange-800",
  ready: "bg-emerald-100 text-emerald-800",
  failed: "bg-rose-100 text-rose-800",
  expired: "bg-neutral-200 text-neutral-700",
}

const statusHint: Record<JobStatus, string> = {
  queued: "任务处理中",
  downloading: "任务处理中",
  transcoding: "任务处理中",
  ready: "任务已完成，可下载",
  failed: "任务失败，可重试或换链接",
  expired: "任务已过期",
}

const apiBase = (import.meta.env.VITE_API_BASE || "").replace(/\/$/, "")
const apiToken = import.meta.env.VITE_API_TOKEN || ""

type APIError = {
  error?: string
  retry_after?: number
}

class RateLimitError extends Error {
  retryAfter?: number
  constructor(message: string, retryAfter?: number) {
    super(message)
    this.retryAfter = retryAfter
  }
}

async function parseError(res: Response, fallback: string) {
  const data = (await res.json().catch(() => ({}))) as APIError
  if (res.status === 429) {
    const retry =
      typeof data.retry_after === "number" ? data.retry_after : undefined
    const message =
      retry && retry > 0
        ? `请求太频繁，请 ${retry} 秒后再试`
        : "请求太频繁，请稍后再试"
    return { message, retryAfter: retry }
  }
  return { message: data.error || fallback }
}

async function createJob(url: string) {
  const res = await fetch(`${apiBase}/jobs`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      ...(apiToken ? { Authorization: `Bearer ${apiToken}` } : {}),
    },
    body: JSON.stringify({ url }),
  })
  if (!res.ok) {
    const parsed = await parseError(res, "创建任务失败")
    if (res.status === 429) {
      throw new RateLimitError(parsed.message, parsed.retryAfter)
    }
    throw new Error(parsed.message)
  }
  return res.json() as Promise<{ job_id: string; status: JobStatus }>
}

async function fetchJob(jobId: string) {
  const res = await fetch(`${apiBase}/jobs/${jobId}`, {
    headers: apiToken ? { Authorization: `Bearer ${apiToken}` } : {},
  })
  if (!res.ok) {
    const parsed = await parseError(res, "获取任务状态失败")
    if (res.status === 429) {
      throw new RateLimitError(parsed.message, parsed.retryAfter)
    }
    throw new Error(parsed.message)
  }
  return res.json() as Promise<Job>
}

async function fetchJobs(limit = 10) {
  const res = await fetch(`${apiBase}/jobs?limit=${limit}`, {
    headers: apiToken ? { Authorization: `Bearer ${apiToken}` } : {},
  })
  if (!res.ok) {
    const parsed = await parseError(res, "获取任务列表失败")
    if (res.status === 429) {
      throw new RateLimitError(parsed.message, parsed.retryAfter)
    }
    throw new Error(parsed.message)
  }
  const data = (await res.json()) as JobListResponse
  return data.jobs
}

function App() {
  const [input, setInput] = useState("")
  const [job, setJob] = useState<Job | null>(null)
  const [jobs, setJobs] = useState<Job[]>([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [listError, setListError] = useState<string | null>(null)
  const [hint, setHint] = useState("支持抖音 / 快手 / B 站 / 小红书 / 好看视频")
  const defaultPollInterval = 3500

  const progress = useMemo(() => {
    if (!job) return 0
    return statusProgress[job.status]
  }, [job])

  useEffect(() => {
    if (
      !job ||
      job.status === "ready" ||
      job.status === "failed" ||
      job.status === "expired"
    ) {
      return
    }

    let active = true
    let timer: number | undefined
    let source: EventSource | null = null
    let currentStatus: JobStatus = job.status
    let closedByClient = false
    const tokenQuery = apiToken ? `?token=${encodeURIComponent(apiToken)}` : ""

    const updateJob = (next: Job) => {
      currentStatus = next.status
      setHint(statusHint[next.status] ?? "任务处理中")
      setJob(next)
      setJobs((prev) => {
        const exists = prev.some((item) => item.job_id === next.job_id)
        if (!exists) {
          return [next, ...prev]
        }
        return prev.map((item) => (item.job_id === next.job_id ? next : item))
      })
    }

    const schedule = (delay: number) => {
      if (!active) return
      timer = window.setTimeout(tick, delay)
    }

    const tick = async () => {
      if (!active || !job) return
      try {
        const next = await fetchJob(job.job_id)
        updateJob(next)
        schedule(defaultPollInterval)
      } catch (err) {
        if (err instanceof RateLimitError) {
          const retry = err.retryAfter && err.retryAfter > 0 ? err.retryAfter : 2
          setHint(`请求过快，${retry} 秒后自动重试`)
          schedule(Math.max(defaultPollInterval, retry * 1000))
          return
        }
        setError(err instanceof Error ? err.message : "刷新失败")
        schedule(defaultPollInterval)
      }
    }

    const openStream = () => {
      if (typeof EventSource === "undefined") {
        schedule(defaultPollInterval)
        return
      }
      try {
        source = new EventSource(
          `${apiBase}/jobs/${job.job_id}/events${tokenQuery}`
        )
      } catch {
        schedule(defaultPollInterval)
        return
      }

      const handleEvent = (data: string) => {
        if (!data) return
        try {
          const next = JSON.parse(data) as Job
          updateJob(next)
          if (
            next.status === "ready" ||
            next.status === "failed" ||
            next.status === "expired"
          ) {
            closedByClient = true
            source?.close()
            source = null
          }
        } catch {
          return
        }
      }

      source.addEventListener("job", (event) => {
        const payload = (event as MessageEvent).data
        if (typeof payload === "string") {
          handleEvent(payload)
        }
      })

      source.onmessage = (event) => {
        if (typeof event.data === "string") {
          handleEvent(event.data)
        }
      }

      source.onerror = () => {
        if (!active) return
        if (
          closedByClient ||
          currentStatus === "ready" ||
          currentStatus === "failed" ||
          currentStatus === "expired"
        ) {
          return
        }
        source?.close()
        source = null
        setHint("连接中断，自动回退轮询")
        schedule(defaultPollInterval)
      }
    }

    openStream()
    return () => {
      active = false
      if (timer) {
        clearTimeout(timer)
      }
      if (source) {
        source.close()
      }
    }
  }, [job, defaultPollInterval])

  useEffect(() => {
    let active = true
    const load = async () => {
      try {
        const items = await fetchJobs(8)
        if (active) {
          setJobs(items)
          setListError(null)
        }
      } catch (err) {
        if (active) {
          setListError(err instanceof Error ? err.message : "加载失败")
        }
      }
    }
    load()
    return () => {
      active = false
    }
  }, [])

  const handleSubmit = async () => {
    setError(null)
    const trimmed = input.trim()
    if (!trimmed) {
      setError("请输入视频链接")
      return
    }
    setBusy(true)
    try {
      const created = await createJob(trimmed)
      setJob({
        job_id: created.job_id,
        source_url: trimmed,
        platform: "detecting",
        status: created.status,
        created_at: new Date().toISOString(),
        updated_at: new Date().toISOString(),
      })
      setHint("任务已创建，正在解析与下载")
      try {
        const items = await fetchJobs(8)
        setJobs(items)
        setListError(null)
      } catch (err) {
        setListError(err instanceof Error ? err.message : "加载失败")
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : "创建失败")
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="min-h-screen bg-[radial-gradient(circle_at_top,rgba(255,237,213,0.9),rgba(255,245,235,0.6),rgba(255,255,255,0.9))] text-foreground">
      <div className="pointer-events-none absolute inset-0 overflow-hidden">
        <div className="absolute left-[-10%] top-[10%] h-64 w-64 rounded-full bg-[radial-gradient(circle,rgba(251,146,60,0.35),transparent_70%)] blur-2xl" />
        <div className="absolute right-[-5%] top-[20%] h-72 w-72 rounded-full bg-[radial-gradient(circle,rgba(45,212,191,0.3),transparent_70%)] blur-2xl" />
        <div className="absolute bottom-[-10%] left-[30%] h-72 w-72 rounded-full bg-[radial-gradient(circle,rgba(251,191,36,0.35),transparent_70%)] blur-2xl" />
      </div>

      <main className="relative mx-auto flex w-full max-w-6xl flex-col gap-10 px-6 pb-20 pt-12">
        <header className="flex flex-wrap items-center justify-between gap-4">
          <div className="flex items-center gap-3">
            <div className="flex h-12 w-12 items-center justify-center rounded-2xl bg-primary text-primary-foreground shadow-lg shadow-orange-200/60">
              <Sparkles className="h-6 w-6" />
            </div>
            <div>
              <p className="text-sm uppercase tracking-[0.3em] text-muted-foreground">
                声浪工坊
              </p>
              <h1 className="text-2xl font-semibold">视频转音频工作台</h1>
            </div>
          </div>
          <div className="flex flex-wrap items-center gap-2 text-xs">
            <Badge className="bg-white/70 text-foreground shadow-sm">
              异步任务队列
            </Badge>
            <Badge className="bg-white/70 text-foreground shadow-sm">
              私有对象存储
            </Badge>
            <Badge className="bg-white/70 text-foreground shadow-sm">
              签名下载链接
            </Badge>
          </div>
        </header>

        <section className="grid gap-8 lg:grid-cols-[1.05fr_0.95fr]">
          <div className="flex flex-col gap-6">
            <div className="animate-in fade-in slide-in-from-bottom-3 duration-700">
              <h2 className="text-4xl font-semibold leading-tight">
                把任何短视频
                <span className="block text-primary">变成可下载的 MP3</span>
              </h2>
              <p className="mt-4 max-w-xl text-base text-muted-foreground">
                提供链接即可自动解析、下载并转码。完成后生成短时效签名链接，
                无需公开桶权限。
              </p>
            </div>

            <div className="grid gap-4 md:grid-cols-2">
              {[
                {
                  title: "智能解析",
                  desc: "自动识别平台并获取最佳音频源",
                },
                {
                  title: "私有存储",
                  desc: "桶保持私有，下载链接可控可撤销",
                },
                {
                  title: "高质量转码",
                  desc: "ffmpeg 提取音轨，支持稳定输出",
                },
                {
                  title: "状态追踪",
                  desc: "队列、下载、转码全程可视化",
                },
              ].map((item, index) => (
                <Card
                  key={item.title}
                  className="border border-white/70 bg-white/80 shadow-sm backdrop-blur"
                  style={{ animationDelay: `${120 * index}ms` }}
                >
                  <CardHeader className="pb-2">
                    <CardTitle className="text-base">{item.title}</CardTitle>
                    <CardDescription>{item.desc}</CardDescription>
                  </CardHeader>
                </Card>
              ))}
            </div>
          </div>

          <Card className="animate-in fade-in slide-in-from-bottom-4 border border-white/70 bg-white/90 shadow-xl backdrop-blur">
            <CardHeader>
              <CardTitle className="flex items-center gap-2 text-xl">
                <Wand2 className="h-5 w-5 text-primary" />
                新建转码任务
              </CardTitle>
              <CardDescription>
                粘贴分享链接后立即开始处理
              </CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
              <div className="space-y-2">
                <label className="text-sm font-medium text-muted-foreground">
                  视频链接
                </label>
                <Input
                  value={input}
                  placeholder="https://www.douyin.com/..."
                  onChange={(event) => setInput(event.target.value)}
                />
                <p className="text-xs text-muted-foreground">{hint}</p>
              </div>

              <Button
                className="w-full"
                onClick={handleSubmit}
                disabled={busy}
              >
                {busy ? (
                  <>
                    <Loader2 className="h-4 w-4 animate-spin" />
                    创建中…
                  </>
                ) : (
                  <>
                    立即转换
                    <ArrowRight className="h-4 w-4" />
                  </>
                )}
              </Button>

              {error && (
                <div className="rounded-lg border border-rose-200 bg-rose-50 px-3 py-2 text-sm text-rose-700">
                  {error}
                </div>
              )}
              {job?.status === "failed" && job.error && (
                <div className="rounded-lg border border-rose-200 bg-rose-50 px-3 py-2 text-sm text-rose-700">
                  {job.error}
                </div>
              )}

              <Separator />

              <div className="space-y-3">
                <div className="flex items-center justify-between text-sm">
                  <span className="text-muted-foreground">任务状态</span>
                  {job ? (
                    <Badge className={statusTone[job.status]}>
                      {statusLabel[job.status]}
                    </Badge>
                  ) : (
                    <Badge className="bg-neutral-200 text-neutral-600">
                      等待创建
                    </Badge>
                  )}
                </div>
                <Progress value={progress} />

                {job && (
                  <div className="rounded-xl border border-dashed border-muted-foreground/30 px-3 py-3 text-xs text-muted-foreground">
                    <div>Job ID: {job.job_id}</div>
                    <div>平台: {job.platform}</div>
                    <div>最近更新: {job.updated_at}</div>
                  </div>
                )}
              </div>
            </CardContent>
            <CardFooter className="flex flex-col gap-2">
              {job?.status === "ready" ? (
                <Button asChild className="w-full">
                  <a
                    href={`${apiBase}/jobs/${job.job_id}/download${apiToken ? `?token=${encodeURIComponent(apiToken)}` : ""}`}
                    download={`video2mp3-${job.job_id}.mp3`}
                  >
                    <CloudDownload className="h-4 w-4" />
                    下载 MP3
                  </a>
                </Button>
              ) : (
                <div className="flex w-full items-center justify-center gap-2 rounded-lg border border-dashed border-muted-foreground/30 py-3 text-xs text-muted-foreground">
                  {job ? (
                    <Loader2 className="h-4 w-4 animate-spin" />
                  ) : (
                    <Sparkles className="h-4 w-4" />
                  )}
                  {job ? "任务进行中，请稍候…" : "还没有任务"}
                </div>
              )}
              <p className="text-xs text-muted-foreground">
                下载链接为短时效签名 URL，每次访问都会自动刷新。
              </p>
            </CardFooter>
          </Card>
        </section>

        <section className="grid gap-6">
          <div className="flex items-center justify-between">
            <h3 className="text-xl font-semibold">最近任务</h3>
            <Button
              variant="outline"
              size="sm"
              onClick={async () => {
                try {
                  const items = await fetchJobs(8)
                  setJobs(items)
                  setListError(null)
                } catch (err) {
                  setListError(err instanceof Error ? err.message : "加载失败")
                }
              }}
            >
              刷新列表
            </Button>
          </div>
          <Card className="border border-white/70 bg-white/80 shadow-sm backdrop-blur">
            <CardContent className="space-y-3 pt-6">
              {listError && (
                <div className="rounded-lg border border-rose-200 bg-rose-50 px-3 py-2 text-sm text-rose-700">
                  {listError}
                </div>
              )}
              {!listError && jobs.length === 0 && (
                <div className="rounded-lg border border-dashed border-muted-foreground/30 px-3 py-6 text-center text-sm text-muted-foreground">
                  还没有任务，先去上方创建一个吧。
                </div>
              )}
              {jobs.map((item, index) => (
                <div key={item.job_id}>
                  <div className="flex flex-wrap items-center justify-between gap-3">
                    <div className="min-w-[220px]">
                      <div className="text-sm font-medium text-foreground">
                        {item.platform || "未识别"} · {item.job_id.slice(0, 8)}
                      </div>
                      <div className="text-xs text-muted-foreground">
                        {new Date(item.updated_at).toLocaleString()}
                      </div>
                    </div>
                    <div className="flex flex-wrap items-center gap-2">
                      <Badge className={statusTone[item.status]}>
                        {statusLabel[item.status]}
                      </Badge>
                      <Button
                        variant="outline"
                        size="xs"
                        onClick={() => setJob(item)}
                      >
                        查看
                      </Button>
                      {item.status === "ready" && (
                        <Button asChild size="xs">
                          <a
                            href={`${apiBase}/jobs/${item.job_id}/download${apiToken ? `?token=${encodeURIComponent(apiToken)}` : ""}`}
                            download={`video2mp3-${item.job_id}.mp3`}
                          >
                            下载
                          </a>
                        </Button>
                      )}
                      {item.status === "failed" && (
                        <Button
                          variant="outline"
                          size="xs"
                          onClick={async () => {
                            try {
                              const res = await fetch(
                                `${apiBase}/jobs/${item.job_id}/retry`,
                                {
                                  method: "POST",
                                  headers: apiToken
                                    ? { Authorization: `Bearer ${apiToken}` }
                                    : {},
                                }
                              )
                              if (!res.ok) {
                                throw new Error("重试失败")
                              }
                              const items = await fetchJobs(8)
                              setJobs(items)
                              setJob(item)
                            } catch (err) {
                              setListError(
                                err instanceof Error ? err.message : "重试失败"
                              )
                            }
                          }}
                        >
                          重试
                        </Button>
                      )}
                    </div>
                  </div>
                  {index < jobs.length - 1 && <Separator className="mt-4" />}
                </div>
              ))}
            </CardContent>
          </Card>
        </section>

        <footer className="flex flex-wrap items-center justify-between gap-3 text-xs text-muted-foreground">
          <span>Powered by video-parser + ffmpeg + MinIO</span>
          <span>MP3 链接默认 15 分钟有效，可在配置中调整</span>
        </footer>
      </main>
    </div>
  )
}

export default App
