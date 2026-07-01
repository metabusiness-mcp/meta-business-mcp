import { Badge } from "@/components/ui/badge"
import { cn } from "@/lib/utils"

type StatusType = "message" | "template" | "compliance" | "conversation"

interface StatusBadgeProps {
  status: string
  type?: StatusType
  className?: string
}

const statusConfig: Record<StatusType, Record<string, { label: string; variant: "default" | "secondary" | "destructive" | "outline" | "success" | "warning" }>> = {
  message: {
    queued: { label: "Queued", variant: "secondary" },
    sent: { label: "Sent", variant: "default" },
    delivered: { label: "Delivered", variant: "success" },
    read: { label: "Read", variant: "success" },
    failed: { label: "Failed", variant: "destructive" },
    scheduled: { label: "Scheduled", variant: "warning" },
  },
  template: {
    approved: { label: "Approved", variant: "success" },
    PENDING: { label: "Pending", variant: "warning" },
    REJECTED: { label: "Rejected", variant: "destructive" },
    pending: { label: "Pending", variant: "warning" },
    rejected: { label: "Rejected", variant: "destructive" },
  },
  compliance: {
    allowed: { label: "Allowed", variant: "success" },
    blocked: { label: "Blocked", variant: "destructive" },
  },
  conversation: {
    active: { label: "Active", variant: "success" },
    archived: { label: "Archived", variant: "secondary" },
  },
}

export function StatusBadge({ status, type = "message", className }: StatusBadgeProps) {
  const config = statusConfig[type]?.[status]
  if (config) {
    return <Badge variant={config.variant} className={className}>{config.label}</Badge>
  }
  return <Badge variant="outline" className={className}>{status}</Badge>
}