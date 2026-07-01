# Developer Guide

This guide describes how to extend the Meta Business MCP platform, including adding new policies, creating new MCP tools, and registering new message queues.

---

## 1. Adding New Policies

The Policy Engine (`pkg/policy/policy.go`) evaluates business rules dynamically seeded from the database. 

To add a new policy type:
1. **Define the Policy Schema**:
   Add the policy rules configuration to `policies.yaml` (or database directly).
   For example, adding a maximum daily token limit policy:
   ```yaml
   - id: "daily_user_token_limit"
     name: "Enforce daily token budget per user"
     type: "token_limit"
     channel: "whatsapp"
     message_type: "marketing"
     is_enabled: true
     rules:
       max_tokens: 1000
   ```
2. **Implement the Evaluation Logic**:
   In `pkg/policy/policy.go`, implement a handler for the new policy type in `EvaluatePolicies`:
   ```go
   case "token_limit":
       // Parse rules: { "max_tokens": 1000 }
       // Query usage stats from DB and verify against rules
       // Return allowed = false if exceeded
   ```

---

## 2. Registering Custom MCP Tools

The Model Context Protocol Server exposes tools via standard input/output. All tools are registered in `pkg/mcp/server.go`.

To add a new tool (e.g., `get_customer_profile`):
1. **Register the Tool Schema** in `registerTools()`:
   ```go
   s.mcpServer.AddTool(mcp.NewTool("get_customer_profile",
       mcp.WithDescription("Retrieve customer profiles and metadata tags"),
       mcp.WithString("customer_id", mcp.Required(), mcp.Description("Recipient phone number")),
   ), s.handleGetCustomerProfile)
   ```
2. **Implement the Tool Handler**:
   Create a handler function in `pkg/mcp/server.go`:
   ```go
   func (s *Server) handleGetCustomerProfile(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
       customerID, err := req.RequireString("customer_id")
       if err != nil {
           return mcp.NewToolResultError("missing customer_id"), nil
       }
       profile, err := s.userManager.GetOrCreateCustomer(ctx, customerID, "whatsapp")
       if err != nil {
           return mcp.NewToolResultError(err.Error()), nil
       }
       resJSON, _ := json.Marshal(profile)
       return mcp.NewToolResultText(string(resJSON)), nil
   }
   ```

---

## 3. Extending Message Queues (NATS)

The system uses NATS JetStream streams to organize asynchronous worker tasks.

To add a new queue (e.g. `META_MCP_ANALYTICS` for asynchronous analytics streaming):
1. **Declare the Stream** in `pkg/delivery/orchestrator.go`:
   ```go
   _, err = o.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
       Name:      "META_MCP_ANALYTICS",
       Subjects:  []string{"analytics.*"},
       Retention: jetstream.WorkQueuePolicy,
   })
   ```
2. **Add Publisher Method** to `Orchestrator`:
   ```go
   func (o *Orchestrator) PublishAnalytics(ctx context.Context, event string, data []byte) error {
       _, err := o.js.Publish(ctx, "analytics."+event, data)
       return err
   }
   ```
3. **Write a Worker Consumer**:
   Similar to `Worker` in `pkg/delivery/worker.go`, create a new worker file under `pkg/delivery` that creates a consumer for the `META_MCP_ANALYTICS` stream and consumes its messages in a background loop.
