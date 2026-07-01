const API_BASE = "/api";

async function fetchAPI<T>(path: string, options?: RequestInit): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`, {
    credentials: "include",
    headers: {
      "Content-Type": "application/json",
    },
    ...options,
  });

  if (res.status === 401) {
    // Don't redirect here — let AuthGuard handle it via React Router
    throw new Error("Unauthorized");
  }

  if (!res.ok) {
    const error = await res.json().catch(() => ({ error: "Request failed" }));
    throw new Error(error.error || "Request failed");
  }

  return res.json();
}

// Auth
export async function login(username: string, password: string) {
  return fetchAPI<{ token: string }>("/auth/login", {
    method: "POST",
    body: JSON.stringify({ username, password }),
  });
}

export async function logout() {
  return fetchAPI<{ status: string }>("/auth/logout", { method: "POST" });
}

export async function checkAuth() {
  return fetchAPI<{ authenticated: boolean }>("/auth/check");
}

// Messages
export async function getMessages(params?: {
  limit?: number;
  offset?: number;
  status?: string;
  direction?: string;
  customer_id?: string;
}) {
  const query = new URLSearchParams();
  if (params?.limit) query.set("limit", String(params.limit));
  if (params?.offset) query.set("offset", String(params.offset));
  if (params?.status) query.set("status", params.status);
  if (params?.direction) query.set("direction", params.direction);
  if (params?.customer_id) query.set("customer_id", params.customer_id);
  return fetchAPI<{
    messages: import("./types").Message[];
    total: number;
    limit: number;
    offset: number;
  }>(`/messages?${query.toString()}`);
}

// Conversations
export async function getConversations(params?: {
  limit?: number;
  offset?: number;
  status?: string;
  expiring_soon?: boolean;
}) {
  const query = new URLSearchParams();
  if (params?.limit) query.set("limit", String(params.limit));
  if (params?.offset) query.set("offset", String(params.offset));
  if (params?.status) query.set("status", params.status);
  if (params?.expiring_soon) query.set("expiring_soon", "true");
  return fetchAPI<{
    conversations: import("./types").Conversation[];
    total: number;
    limit: number;
    offset: number;
  }>(`/conversations?${query.toString()}`);
}

// Templates
export async function getTemplates(params?: {
  status?: string;
  category?: string;
}) {
  const query = new URLSearchParams();
  if (params?.status) query.set("status", params.status);
  if (params?.category) query.set("category", params.category);
  return fetchAPI<{
    templates: import("./types").Template[];
    total: number;
  }>(`/templates?${query.toString()}`);
}

// Compliance Events
export async function getComplianceEvents(params?: {
  limit?: number;
  offset?: number;
}) {
  const query = new URLSearchParams();
  if (params?.limit) query.set("limit", String(params.limit));
  if (params?.offset) query.set("offset", String(params.offset));
  return fetchAPI<{
    events: import("./types").ComplianceEvent[];
    total: number;
    limit: number;
    offset: number;
  }>(`/compliance/events?${query.toString()}`);
}

// Metrics Summary
export async function getMetricsSummary() {
  return fetchAPI<import("./types").MetricsSummary>("/metrics/summary");
}

// Config
export async function getWebhookConfig() {
  return fetchAPI<import("./types").WebhookConfig>("/config/webhook");
}

export async function getMetaConfig() {
  return fetchAPI<import("./types").MetaConfig>("/config/meta");
}
