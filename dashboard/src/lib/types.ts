export interface Message {
  id: string;
  conversation_id?: string;
  customer_id: string;
  direction: "inbound" | "outbound";
  message_type: string;
  status: string;
  error_code?: number;
  error_message?: string;
  retry_count: number;
  next_retry_at?: string;
  created_at: string;
  updated_at: string;
}

export interface Conversation {
  id: string;
  customer_id: string;
  channel: string;
  conversation_type: string;
  last_inbound_at?: string;
  window_expires_at?: string;
  status: string;
  created_at: string;
  updated_at: string;
}

export interface Template {
  name: string;
  locale: string;
  category: string;
  status: string;
  body_text: string;
  variables?: unknown;
  created_at: string;
  updated_at: string;
}

export interface ComplianceEvent {
  id: string;
  action: string;
  details: {
    customer_id?: string;
    allowed?: boolean;
    reason_code?: string;
    [key: string]: unknown;
  };
  created_at: string;
}

export interface MetricsSummary {
  messages_sent_today: number;
  messages_delivered: number;
  messages_failed: number;
  active_conversations: number;
  compliance_checks_today: number;
  compliance_pass_rate: number;
  templates_approved: number;
  templates_pending: number;
  templates_rejected: number;
}

export interface WebhookConfig {
  webhook_url: string;
  verify_token: string;
  signature_validation: boolean;
}

export interface MetaConfig {
  phone_number_id: string;
  waba_id: string;
  api_url: string;
  tier: string;
}

export interface PaginatedResponse<T> {
  total: number;
  limit: number;
  offset: number;
  [key: string]: T[] | number;
}