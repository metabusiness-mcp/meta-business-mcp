"use client"

import { useEffect, useState, useCallback } from "react"
import { AuthGuard } from "@/components/auth-guard"
import { Sidebar } from "@/components/sidebar"
import { Card, CardContent } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { getComplianceEvents } from "@/lib/api"
import { formatDate } from "@/lib/utils"
import type { ComplianceEvent } from "@/lib/types"
import { ChevronLeft, ChevronRight, RefreshCw } from "lucide-react"

const PAGE_SIZE = 100

export default function CompliancePage() {
  const [events, setEvents] = useState<ComplianceEvent[]>([])
  const [total, setTotal] = useState(0)
  const [offset, setOffset] = useState(0)
  const [loading, setLoading] = useState(true)

  const fetchData = useCallback(async () => {
    setLoading(true)
    try {
      const data = await getComplianceEvents({ limit: PAGE_SIZE, offset })
      setEvents(data.events)
      setTotal(data.total)
    } catch (err) {
      console.error("Failed to fetch compliance events:", err)
    } finally {
      setLoading(false)
    }
  }, [offset])

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
              <h1 className="text-3xl font-bold">Compliance</h1>
              <p className="text-muted-foreground">{total} compliance events</p>
            </div>
            <Button variant="outline" size="sm" onClick={fetchData}><RefreshCw className="h-4 w-4 mr-2" />Refresh</Button>
          </div>

          <Card>
            <CardContent className="p-0">
              <div className="overflow-x-auto">
                <table className="w-full text-sm">
                  <thead>
                    <tr className="border-b bg-muted/50">
                      <th className="px-4 py-3 text-left font-medium">Action</th>
                      <th className="px-4 py-3 text-left font-medium">Customer ID</th>
                      <th className="px-4 py-3 text-left font-medium">Result</th>
                      <th className="px-4 py-3 text-left font-medium">Reason Code</th>
                      <th className="px-4 py-3 text-left font-medium">Timestamp</th>
                    </tr>
                  </thead>
                  <tbody>
                    {loading ? (
                      <tr><td colSpan={5} className="px-4 py-8 text-center text-muted-foreground">Loading...</td></tr>
                    ) : events.length === 0 ? (
                      <tr><td colSpan={5} className="px-4 py-8 text-center text-muted-foreground">No compliance events found</td></tr>
                    ) : (
                      events.map((event) => (
                        <tr key={event.id} className="border-b hover:bg-muted/50">
                          <td className="px-4 py-3 font-mono text-xs">{event.action}</td>
                          <td className="px-4 py-3">{event.details?.customer_id || "—"}</td>
                          <td className="px-4 py-3">
                            {event.details?.allowed === true ? (
                              <span className="text-green-600 font-medium">Allowed</span>
                            ) : event.details?.allowed === false ? (
                              <span className="text-red-600 font-medium">Blocked</span>
                            ) : (
                              <span className="text-muted-foreground">—</span>
                            )}
                          </td>
                          <td className="px-4 py-3 text-xs">{event.details?.reason_code || "—"}</td>
                          <td className="px-4 py-3 text-xs text-muted-foreground">{formatDate(event.created_at)}</td>
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
