"use client"

import { useEffect, useState } from "react"
import { AuthGuard } from "@/components/auth-guard"
import { Sidebar } from "@/components/sidebar"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Separator } from "@/components/ui/separator"
import { getWebhookConfig, getMetaConfig } from "@/lib/api"
import type { WebhookConfig, MetaConfig } from "@/lib/types"
import { RefreshCw } from "lucide-react"
import { Button } from "@/components/ui/button"

export default function SettingsPage() {
  const [webhook, setWebhook] = useState<WebhookConfig | null>(null)
  const [meta, setMeta] = useState<MetaConfig | null>(null)
  const [loading, setLoading] = useState(true)

  const fetchData = async () => {
    setLoading(true)
    try {
      const [w, m] = await Promise.all([getWebhookConfig(), getMetaConfig()])
      setWebhook(w)
      setMeta(m)
    } catch (err) {
      console.error("Failed to fetch config:", err)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { fetchData() }, [])

  return (
    <AuthGuard>
      <div className="flex h-screen">
        <Sidebar />
        <main className="flex-1 overflow-y-auto bg-muted/40 p-6">
          <div className="flex items-center justify-between mb-6">
            <div>
              <h1 className="text-3xl font-bold">Settings</h1>
              <p className="text-muted-foreground">Read-only configuration display</p>
            </div>
            <Button variant="outline" size="sm" onClick={fetchData}><RefreshCw className="h-4 w-4 mr-2" />Refresh</Button>
          </div>

          <div className="grid gap-6 max-w-2xl">
            <Card>
              <CardHeader>
                <CardTitle>Webhook Configuration</CardTitle>
              </CardHeader>
              <CardContent className="space-y-4">
                {loading ? (
                  <p className="text-muted-foreground">Loading...</p>
                ) : webhook ? (
                  <>
                    <div>
                      <p className="text-sm font-medium text-muted-foreground">Webhook URL</p>
                      <p className="font-mono text-sm">{webhook.webhook_url}</p>
                    </div>
                    <Separator />
                    <div>
                      <p className="text-sm font-medium text-muted-foreground">Verify Token</p>
                      <p className="font-mono text-sm">{webhook.verify_token}</p>
                    </div>
                    <Separator />
                    <div>
                      <p className="text-sm font-medium text-muted-foreground">Signature Validation</p>
                      <p className="text-sm">{webhook.signature_validation ? "Enabled" : "Disabled"}</p>
                    </div>
                  </>
                ) : (
                  <p className="text-destructive">Failed to load webhook config</p>
                )}
              </CardContent>
            </Card>

            <Card>
              <CardHeader>
                <CardTitle>Meta API Configuration</CardTitle>
              </CardHeader>
              <CardContent className="space-y-4">
                {loading ? (
                  <p className="text-muted-foreground">Loading...</p>
                ) : meta ? (
                  <>
                    <div>
                      <p className="text-sm font-medium text-muted-foreground">Phone Number ID</p>
                      <p className="font-mono text-sm">{meta.phone_number_id}</p>
                    </div>
                    <Separator />
                    <div>
                      <p className="text-sm font-medium text-muted-foreground">WABA ID</p>
                      <p className="font-mono text-sm">{meta.waba_id}</p>
                    </div>
                    <Separator />
                    <div>
                      <p className="text-sm font-medium text-muted-foreground">API URL</p>
                      <p className="font-mono text-sm">{meta.api_url}</p>
                    </div>
                    <Separator />
                    <div>
                      <p className="text-sm font-medium text-muted-foreground">Current Tier</p>
                      <p className="text-sm capitalize">{meta.tier}</p>
                    </div>
                  </>
                ) : (
                  <p className="text-destructive">Failed to load Meta config</p>
                )}
              </CardContent>
            </Card>
          </div>
        </main>
      </div>
    </AuthGuard>
  )
}
