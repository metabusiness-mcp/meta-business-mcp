"use client"

import { useEffect, useState, useCallback } from "react"
import { AuthGuard } from "@/components/auth-guard"
import { Sidebar } from "@/components/sidebar"
import { StatusBadge } from "@/components/status-badge"
import { Card, CardContent } from "@/components/ui/card"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Button } from "@/components/ui/button"
import { getConversations } from "@/lib/api"
import { formatDate } from "@/lib/utils"
import type { Conversation } from "@/lib/types"
import { ChevronLeft, ChevronRight, RefreshCw } from "lucide-react"

const PAGE_SIZE = 50

function windowColor(expiresAt?: string): string {
  if (!expiresAt) return "text-muted-foreground"
  const now = new Date()
  const exp = new Date(expiresAt)
  if (exp <= now) return "text-red-600"
  if (exp.getTime() - now.getTime() < 3600000) return "text-yellow-600"
  return "text-green-600"
}

export default function ConversationsPage() {
  const [conversations, setConversations] = useState<Conversation[]>([])
  const [total, setTotal] = useState(0)
  const [offset, setOffset] = useState(0)
  const [statusFilter, setStatusFilter] = useState("")
  const [expiringSoon, setExpiringSoon] = useState(false)
  const [loading, setLoading] = useState(true)

  const fetchData = useCallback(async () => {
    setLoading(true)
    try {
      const data = await getConversations({
        limit: PAGE_SIZE,
        offset,
        status: statusFilter || undefined,
        expiring_soon: expiringSoon || undefined,
      })
      setConversations(data.conversations)
      setTotal(data.total)
    } catch (err) {
      console.error("Failed to fetch conversations:", err)
    } finally {
      setLoading(false)
    }
  }, [offset, statusFilter, expiringSoon])

  useEffect(() => { fetchData() }, [fetchData])

  const totalPages = Math.ceil(total / PAGE_SIZE)

  return (
    <AuthGuard>
      <div className="flex h-screen">
        <Sidebar />
        <main className="flex-1 overflow-y-auto bg-muted/40 p-6">
          <div className="flex items-center justify-between mb-6">
            <div>
              <h1 className="text-3xl font-bold">Conversations</h1>
              <p className="text-muted-foreground">{total} conversations</p>
            </div>
            <Button variant="outline" size="sm" onClick={fetchData}><RefreshCw className="h-4 w-4 mr-2" />Refresh</Button>
          </div>

          <div className="flex gap-4 mb-4">
            <Select value={statusFilter} onValueChange={(v) => { setStatusFilter(v === "all" ? "" : v); setOffset(0) }}>
              <SelectTrigger className="w-40"><SelectValue placeholder="All Statuses" /></SelectTrigger>
              <SelectContent>
                <SelectItem value="all">All Statuses</SelectItem>
                <SelectItem value="active">Active</SelectItem>
                <SelectItem value="archived">Archived</SelectItem>
              </SelectContent>
            </Select>
            <Button variant={expiringSoon ? "default" : "outline"} size="sm" onClick={() => { setExpiringSoon(!expiringSoon); setOffset(0) }}>
              Expiring Soon
            </Button>
          </div>

          <Card>
            <CardContent className="p-0">
              <div className="overflow-x-auto">
                <table className="w-full text-sm">
                  <thead>
                    <tr className="border-b bg-muted/50">
                      <th className="px-4 py-3 text-left font-medium">Customer ID</th>
                      <th className="px-4 py-3 text-left font-medium">Channel</th>
                      <th className="px-4 py-3 text-left font-medium">Type</th>
                      <th className="px-4 py-3 text-left font-medium">Last Inbound</th>
                      <th className="px-4 py-3 text-left font-medium">Window Expires</th>
                      <th className="px-4 py-3 text-left font-medium">Status</th>
                    </tr>
                  </thead>
                  <tbody>
                    {loading ? (
                      <tr><td colSpan={6} className="px-4 py-8 text-center text-muted-foreground">Loading...</td></tr>
                    ) : conversations.length === 0 ? (
                      <tr><td colSpan={6} className="px-4 py-8 text-center text-muted-foreground">No conversations found</td></tr>
                    ) : (
                      conversations.map((conv) => (
                        <tr key={conv.id} className="border-b hover:bg-muted/50">
                          <td className="px-4 py-3">{conv.customer_id}</td>
                          <td className="px-4 py-3">{conv.channel}</td>
                          <td className="px-4 py-3">{conv.conversation_type}</td>
                          <td className="px-4 py-3 text-xs">{formatDate(conv.last_inbound_at)}</td>
                          <td className={`px-4 py-3 text-xs font-medium ${windowColor(conv.window_expires_at)}`}>
                            {formatDate(conv.window_expires_at)}
                          </td>
                          <td className="px-4 py-3"><StatusBadge status={conv.status} type="conversation" /></td>
                        </tr>
                      ))
                    )}
                  </tbody>
                </table>
              </div>
            </CardContent>
          </Card>

          {totalPages > 1 && (
            <div className="flex items-center justify-between mt-4">
              <p className="text-sm text-muted-foreground">Page {Math.floor(offset / PAGE_SIZE) + 1} of {totalPages}</p>
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
