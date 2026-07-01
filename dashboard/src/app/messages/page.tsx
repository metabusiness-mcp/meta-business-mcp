"use client"

import { useEffect, useState, useCallback } from "react"
import { AuthGuard } from "@/components/auth-guard"
import { Sidebar } from "@/components/sidebar"
import { StatusBadge } from "@/components/status-badge"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Button } from "@/components/ui/button"
import { getMessages } from "@/lib/api"
import { formatDate } from "@/lib/utils"
import type { Message } from "@/lib/types"
import { ChevronLeft, ChevronRight, RefreshCw } from "lucide-react"

const PAGE_SIZE = 50

export default function MessagesPage() {
  const [messages, setMessages] = useState<Message[]>([])
  const [total, setTotal] = useState(0)
  const [offset, setOffset] = useState(0)
  const [statusFilter, setStatusFilter] = useState("")
  const [directionFilter, setDirectionFilter] = useState("")
  const [loading, setLoading] = useState(true)
  const [expandedRow, setExpandedRow] = useState<string | null>(null)

  const fetchData = useCallback(async () => {
    setLoading(true)
    try {
      const data = await getMessages({
        limit: PAGE_SIZE,
        offset,
        status: statusFilter || undefined,
        direction: directionFilter || undefined,
      })
      setMessages(data.messages)
      setTotal(data.total)
    } catch (err) {
      console.error("Failed to fetch messages:", err)
    } finally {
      setLoading(false)
    }
  }, [offset, statusFilter, directionFilter])

  useEffect(() => { fetchData() }, [fetchData])

  const totalPages = Math.ceil(total / PAGE_SIZE)
  const currentPage = Math.floor(offset / PAGE_SIZE) + 1

  return (
    <AuthGuard>
      <div className="flex h-screen">
        <Sidebar />
        <main className="flex-1 overflow-y-auto bg-muted/40 p-6">
          <div className="flex items-center justify-between mb-6">
            <div>
              <h1 className="text-3xl font-bold">Messages</h1>
              <p className="text-muted-foreground">{total} total messages</p>
            </div>
            <Button variant="outline" size="sm" onClick={fetchData}><RefreshCw className="h-4 w-4 mr-2" />Refresh</Button>
          </div>

          <div className="flex gap-4 mb-4">
            <Select value={statusFilter} onValueChange={(v) => { setStatusFilter(v === "all" ? "" : v); setOffset(0) }}>
              <SelectTrigger className="w-40"><SelectValue placeholder="All Statuses" /></SelectTrigger>
              <SelectContent>
                <SelectItem value="all">All Statuses</SelectItem>
                <SelectItem value="queued">Queued</SelectItem>
                <SelectItem value="sent">Sent</SelectItem>
                <SelectItem value="delivered">Delivered</SelectItem>
                <SelectItem value="read">Read</SelectItem>
                <SelectItem value="failed">Failed</SelectItem>
              </SelectContent>
            </Select>
            <Select value={directionFilter} onValueChange={(v) => { setDirectionFilter(v === "all" ? "" : v); setOffset(0) }}>
              <SelectTrigger className="w-40"><SelectValue placeholder="All Directions" /></SelectTrigger>
              <SelectContent>
                <SelectItem value="all">All Directions</SelectItem>
                <SelectItem value="inbound">Inbound</SelectItem>
                <SelectItem value="outbound">Outbound</SelectItem>
              </SelectContent>
            </Select>
          </div>

          <Card>
            <CardContent className="p-0">
              <div className="overflow-x-auto">
                <table className="w-full text-sm">
                  <thead>
                    <tr className="border-b bg-muted/50">
                      <th className="px-4 py-3 text-left font-medium">ID</th>
                      <th className="px-4 py-3 text-left font-medium">Customer</th>
                      <th className="px-4 py-3 text-left font-medium">Direction</th>
                      <th className="px-4 py-3 text-left font-medium">Type</th>
                      <th className="px-4 py-3 text-left font-medium">Status</th>
                      <th className="px-4 py-3 text-left font-medium">Error</th>
                      <th className="px-4 py-3 text-left font-medium">Timestamp</th>
                    </tr>
                  </thead>
                  <tbody>
                    {loading ? (
                      <tr><td colSpan={7} className="px-4 py-8 text-center text-muted-foreground">Loading...</td></tr>
                    ) : messages.length === 0 ? (
                      <tr><td colSpan={7} className="px-4 py-8 text-center text-muted-foreground">No messages found</td></tr>
                    ) : (
                      messages.map((msg) => (
                        <>
                          <tr key={msg.id} className="border-b hover:bg-muted/50 cursor-pointer" onClick={() => setExpandedRow(expandedRow === msg.id ? null : msg.id)}>
                            <td className="px-4 py-3 font-mono text-xs">{msg.id.slice(0, 16)}...</td>
                            <td className="px-4 py-3">{msg.customer_id}</td>
                            <td className="px-4 py-3">
                              <span className={msg.direction === "inbound" ? "text-blue-600" : "text-green-600"}>{msg.direction}</span>
                            </td>
                            <td className="px-4 py-3">{msg.message_type}</td>
                            <td className="px-4 py-3"><StatusBadge status={msg.status} type="message" /></td>
                            <td className="px-4 py-3 text-destructive text-xs">{msg.error_code ? `${msg.error_code}` : "—"}</td>
                            <td className="px-4 py-3 text-xs text-muted-foreground">{formatDate(msg.created_at)}</td>
                          </tr>
                          {expandedRow === msg.id && (
                            <tr key={`${msg.id}-detail`} className="bg-muted/30">
                              <td colSpan={7} className="px-4 py-3">
                                <div className="grid grid-cols-2 gap-2 text-xs">
                                  <div><strong>Full ID:</strong> {msg.id}</div>
                                  <div><strong>Conversation ID:</strong> {msg.conversation_id || "—"}</div>
                                  <div><strong>Error Message:</strong> {msg.error_message || "—"}</div>
                                  <div><strong>Retry Count:</strong> {msg.retry_count}</div>
                                  <div><strong>Next Retry:</strong> {msg.next_retry_at ? formatDate(msg.next_retry_at) : "—"}</div>
                                  <div><strong>Updated:</strong> {formatDate(msg.updated_at)}</div>
                                </div>
                              </td>
                            </tr>
                          )}
                        </>
                      ))
                    )}
                  </tbody>
                </table>
              </div>
            </CardContent>
          </Card>

          {totalPages > 1 && (
            <div className="flex items-center justify-between mt-4">
              <p className="text-sm text-muted-foreground">Page {currentPage} of {totalPages}</p>
              <div className="flex gap-2">
                <Button variant="outline" size="sm" disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))}>
                  <ChevronLeft className="h-4 w-4" /> Previous
                </Button>
                <Button variant="outline" size="sm" disabled={offset + PAGE_SIZE >= total} onClick={() => setOffset(offset + PAGE_SIZE)}>
                  Next <ChevronRight className="h-4 w-4" />
                </Button>
              </div>
            </div>
          )}
        </main>
      </div>
    </AuthGuard>
  )
}
