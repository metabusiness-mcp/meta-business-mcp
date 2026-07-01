"use client"

import { useEffect, useState, useCallback } from "react"
import { AuthGuard } from "@/components/auth-guard"
import { Sidebar } from "@/components/sidebar"
import { MetricCard } from "@/components/metric-card"
import { StatusBadge } from "@/components/status-badge"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { getMetricsSummary, getMessages, getComplianceEvents } from "@/lib/api"
import { formatDate, formatPercent, formatNumber } from "@/lib/utils"
import type { MetricsSummary, Message, ComplianceEvent } from "@/lib/types"
import { Send, CheckCircle, MessageSquare, Shield } from "lucide-react"

const PIE_COLORS = ["#22c55e", "#ef4444", "#f59e0b", "#6b7280"]

export default function DashboardPage() {
  const [metrics, setMetrics] = useState<MetricsSummary | null>(null)
  const [recentMessages, setRecentMessages] = useState<Message[]>([])
  const [recentEvents, setRecentEvents] = useState<ComplianceEvent[]>([])
  const [loading, setLoading] = useState(true)

  const fetchData = useCallback(async () => {
    try {
      const [metricsData, messagesData, eventsData] = await Promise.all([
        getMetricsSummary().catch(() => null),
        getMessages({ limit: 10 }).catch(() => ({ messages: [], total: 0, limit: 10, offset: 0 })),
        getComplianceEvents({ limit: 5 }).catch(() => ({ events: [], total: 0, limit: 5, offset: 0 })),
      ])
      if (metricsData) setMetrics(metricsData)
      setRecentMessages(messagesData.messages)
      setRecentEvents(eventsData.events)
    } catch (err) {
      console.error("Failed to fetch dashboard data:", err)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    fetchData()
    const interval = setInterval(fetchData, 10000)
    return () => clearInterval(interval)
  }, [fetchData])

  const volumeData = [
    { day: "Mon", sent: 120, delivered: 115 },
    { day: "Tue", sent: 180, delivered: 170 },
    { day: "Wed", sent: 150, delivered: 145 },
    { day: "Thu", sent: 200, delivered: 192 },
    { day: "Fri", sent: 170, delivered: 165 },
    { day: "Sat", sent: 90, delivered: 88 },
    { day: "Sun", sent: 60, delivered: 58 },
  ]

  const pieData = metrics ? [
    { name: "Delivered", value: metrics.messages_delivered },
    { name: "Failed", value: metrics.messages_failed },
    { name: "Pending", value: Math.max(0, metrics.messages_sent_today - metrics.messages_delivered - metrics.messages_failed) },
  ].filter(d => d.value > 0) : []

  const deliveryRate = metrics && metrics.messages_sent_today > 0
    ? metrics.messages_delivered / metrics.messages_sent_today
    : 0

  return (
    <AuthGuard>
      <div className="flex h-screen">
        <Sidebar />
        <main className="flex-1 overflow-y-auto bg-muted/40 p-6">
          <div className="mb-6">
            <h1 className="text-3xl font-bold">Dashboard</h1>
            <p className="text-muted-foreground">Overview of your WhatsApp messaging platform</p>
          </div>

          {loading ? (
            <div className="flex items-center justify-center h-64">
              <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-primary"></div>
            </div>
          ) : (
            <>
              <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4 mb-6">
                <MetricCard title="Messages Sent Today" value={formatNumber(metrics?.messages_sent_today ?? 0)} icon={Send} subtitle="Outbound messages" />
                <MetricCard title="Delivery Rate" value={formatPercent(deliveryRate)} icon={CheckCircle} trend={deliveryRate > 0.95 ? "up" : "down"} subtitle={deliveryRate > 0.95 ? "Healthy" : "Below threshold"} />
                <MetricCard title="Active Conversations" value={formatNumber(metrics?.active_conversations ?? 0)} icon={MessageSquare} subtitle="Open windows" />
                <MetricCard title="Compliance Pass Rate" value={formatPercent(metrics?.compliance_pass_rate ?? 0)} icon={Shield} trend={(metrics?.compliance_pass_rate ?? 0) > 0.95 ? "up" : "down"} subtitle={`${formatNumber(metrics?.compliance_checks_today ?? 0)} checks today`} />
              </div>

              <div className="grid gap-4 md:grid-cols-2 mb-6">
                <Card>
                  <CardHeader><CardTitle>Message Volume (7 Days)</CardTitle></CardHeader>
                  <CardContent>
                    <div className="h-[250px] flex items-center justify-center text-muted-foreground text-sm">
                      Chart will render with live data
                    </div>
                  </CardContent>
                </Card>
                <Card>
                  <CardHeader><CardTitle>Message Status Breakdown</CardTitle></CardHeader>
                  <CardContent>
                    <div className="h-[250px] flex items-center justify-center text-muted-foreground text-sm">
                      Chart will render with live data
                    </div>
                  </CardContent>
                </Card>
              </div>

              <div className="grid gap-4 md:grid-cols-2">
                <Card>
                  <CardHeader><CardTitle>Recent Messages</CardTitle></CardHeader>
                  <CardContent>
                    <div className="space-y-3">
                      {recentMessages.length === 0 ? <p className="text-sm text-muted-foreground">No messages yet</p> : recentMessages.map((msg) => (
                        <div key={msg.id} className="flex items-center justify-between border-b pb-2 last:border-0">
                          <div className="flex-1 min-w-0">
                            <p className="text-sm font-medium truncate">{msg.customer_id}</p>
                            <p className="text-xs text-muted-foreground">{msg.direction} &middot; {msg.message_type}</p>
                          </div>
                          <div className="flex items-center gap-2">
                            <StatusBadge status={msg.status} type="message" />
                            <span className="text-xs text-muted-foreground whitespace-nowrap">{formatDate(msg.created_at)}</span>
                          </div>
                        </div>
                      ))}
                    </div>
                  </CardContent>
                </Card>
                <Card>
                  <CardHeader><CardTitle>Recent Compliance Events</CardTitle></CardHeader>
                  <CardContent>
                    <div className="space-y-3">
                      {recentEvents.length === 0 ? <p className="text-sm text-muted-foreground">No compliance events yet</p> : recentEvents.map((event) => (
                        <div key={event.id} className="flex items-center justify-between border-b pb-2 last:border-0">
                          <div className="flex-1 min-w-0">
                            <p className="text-sm font-medium">{event.details?.customer_id || "Unknown"}</p>
                            <p className="text-xs text-muted-foreground">{event.details?.reason_code || "compliance_check"}</p>
                          </div>
                          <div className="flex items-center gap-2">
                            <StatusBadge status={event.details?.allowed ? "allowed" : "blocked"} type="compliance" />
                            <span className="text-xs text-muted-foreground whitespace-nowrap">{formatDate(event.created_at)}</span>
                          </div>
                        </div>
                      ))}
                    </div>
                  </CardContent>
                </Card>
              </div>
            </>
          )}
        </main>
      </div>
    </AuthGuard>
  )
}
