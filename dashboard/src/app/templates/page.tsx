"use client"

import { useEffect, useState, useCallback } from "react"
import { AuthGuard } from "@/components/auth-guard"
import { Sidebar } from "@/components/sidebar"
import { StatusBadge } from "@/components/status-badge"
import { Card, CardContent } from "@/components/ui/card"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Button } from "@/components/ui/button"
import { getTemplates } from "@/lib/api"
import type { Template } from "@/lib/types"
import { RefreshCw } from "lucide-react"

export default function TemplatesPage() {
  const [templates, setTemplates] = useState<Template[]>([])
  const [total, setTotal] = useState(0)
  const [statusFilter, setStatusFilter] = useState("")
  const [categoryFilter, setCategoryFilter] = useState("")
  const [loading, setLoading] = useState(true)
  const [expandedRow, setExpandedRow] = useState<string | null>(null)

  const fetchData = useCallback(async () => {
    setLoading(true)
    try {
      const data = await getTemplates({
        status: statusFilter || undefined,
        category: categoryFilter || undefined,
      })
      setTemplates(data.templates)
      setTotal(data.total)
    } catch (err) {
      console.error("Failed to fetch templates:", err)
    } finally {
      setLoading(false)
    }
  }, [statusFilter, categoryFilter])

  useEffect(() => { fetchData() }, [fetchData])

  return (
    <AuthGuard>
      <div className="flex h-screen">
        <Sidebar />
        <main className="flex-1 overflow-y-auto bg-muted/40 p-6">
          <div className="flex items-center justify-between mb-6">
            <div>
              <h1 className="text-3xl font-bold">Templates</h1>
              <p className="text-muted-foreground">{total} templates</p>
            </div>
            <Button variant="outline" size="sm" onClick={fetchData}><RefreshCw className="h-4 w-4 mr-2" />Refresh</Button>
          </div>

          <div className="flex gap-4 mb-4">
            <Select value={statusFilter} onValueChange={(v) => { setStatusFilter(v === "all" ? "" : v) }}>
              <SelectTrigger className="w-40"><SelectValue placeholder="All Statuses" /></SelectTrigger>
              <SelectContent>
                <SelectItem value="all">All Statuses</SelectItem>
                <SelectItem value="approved">Approved</SelectItem>
                <SelectItem value="pending">Pending</SelectItem>
                <SelectItem value="rejected">Rejected</SelectItem>
              </SelectContent>
            </Select>
            <Select value={categoryFilter} onValueChange={(v) => { setCategoryFilter(v === "all" ? "" : v) }}>
              <SelectTrigger className="w-40"><SelectValue placeholder="All Categories" /></SelectTrigger>
              <SelectContent>
                <SelectItem value="all">All Categories</SelectItem>
                <SelectItem value="marketing">Marketing</SelectItem>
                <SelectItem value="utility">Utility</SelectItem>
                <SelectItem value="authentication">Authentication</SelectItem>
              </SelectContent>
            </Select>
          </div>

          <Card>
            <CardContent className="p-0">
              <div className="overflow-x-auto">
                <table className="w-full text-sm">
                  <thead>
                    <tr className="border-b bg-muted/50">
                      <th className="px-4 py-3 text-left font-medium">Name</th>
                      <th className="px-4 py-3 text-left font-medium">Locale</th>
                      <th className="px-4 py-3 text-left font-medium">Category</th>
                      <th className="px-4 py-3 text-left font-medium">Status</th>
                      <th className="px-4 py-3 text-left font-medium">Body Preview</th>
                    </tr>
                  </thead>
                  <tbody>
                    {loading ? (
                      <tr><td colSpan={5} className="px-4 py-8 text-center text-muted-foreground">Loading...</td></tr>
                    ) : templates.length === 0 ? (
                      <tr><td colSpan={5} className="px-4 py-8 text-center text-muted-foreground">No templates found</td></tr>
                    ) : (
                      templates.map((tmpl) => (
                        <>
                          <tr key={`${tmpl.name}-${tmpl.locale}`} className="border-b hover:bg-muted/50 cursor-pointer" onClick={() => setExpandedRow(expandedRow === `${tmpl.name}-${tmpl.locale}` ? null : `${tmpl.name}-${tmpl.locale}`)}>
                            <td className="px-4 py-3 font-mono text-xs">{tmpl.name}</td>
                            <td className="px-4 py-3">{tmpl.locale}</td>
                            <td className="px-4 py-3">
                              <span className="capitalize">{tmpl.category}</span>
                            </td>
                            <td className="px-4 py-3"><StatusBadge status={tmpl.status} type="template" /></td>
                            <td className="px-4 py-3 text-xs text-muted-foreground max-w-xs truncate">{tmpl.body_text?.slice(0, 80)}...</td>
                          </tr>
                          {expandedRow === `${tmpl.name}-${tmpl.locale}` && (
                            <tr key={`${tmpl.name}-${tmpl.locale}-detail`} className="bg-muted/30">
                              <td colSpan={5} className="px-4 py-3">
                                <div className="space-y-2">
                                  <div><strong>Full Body:</strong></div>
                                  <pre className="text-xs bg-background p-3 rounded border whitespace-pre-wrap">{tmpl.body_text}</pre>
                                  {tmpl.variables ? (
                                    <div className="text-xs"><strong>Variables:</strong> {JSON.stringify(tmpl.variables)}</div>
                                  ) : null}
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
        </main>
      </div>
    </AuthGuard>
  )
}
