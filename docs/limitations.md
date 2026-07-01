# Known Limitations & Roadmap

This document outlines the current limitations of the Meta Business MCP platform and the planned roadmap for future development.

## Known Limitations

1. **WhatsApp Only**: The platform is currently configured exclusively for WhatsApp Business Graph Cloud APIs. Other messaging channels (like Facebook Messenger, Instagram Direct, or SMS) are not integrated in the current version, though the database schema includes a `channel` column to support them.
2. **Single WABA per Instance**: The service is configured to bind to a single WhatsApp Business Account (WABA ID) and Phone Number ID per running instance. Multitenancy (supporting multiple WABAs and Phone IDs concurrently on a single instance) requires refactoring the orchestrator and worker configuration layers.
3. **No Dynamic Webhook Registration**: Webhooks are expected to be registered statically in the Meta App Developer Portal pointing to the receiver's `/webhook` address. There is no automated API registration or token rotation implemented.
4. **Campaign Engine Scope**: The campaign management module is seeded in the database schema (drafting, audience filters, templates association) but campaign scheduling execution is designed to be handled by an external job scheduling layer (like Kubernetes CronJobs or NATS Cron triggers).
5. **Webhook Timing & Status Latency**: In mock testing, status updates (`delivered`, `read`) are simulated with a fixed 1-2 second delay. In real production WABA environments, Meta triggers webhook status updates asynchronously depending on the recipient's actual network connection and device state. Systems must not assume status transitions occur instantaneously within a synchronous message dispatch timeline.
6. **Sandbox Allowed List Restriction**: In Sandbox/Test mode, Meta blocks any outbound messages sent to phone numbers that are not explicitly registered and verified in your Meta Developer Dashboard under the Sandbox/Test phone settings, returning error code `131030` ("Recipient phone number not in allowed list"). Verify recipients are added to this list before testing in sandbox environments.

## Roadmap

- **Multi-channel integration**: Incorporate Meta Messenger API and Instagram Direct API into the unified compliance check loop.
- **Multitenant Routing**: Extend the configuration system to query WABA access tokens and Phone Number IDs dynamically from the database based on account keys, enabling a single cluster to route for thousands of distinct businesses.
- **Automated Webhook Subscriptions**: Implement API endpoints to register, renew, and inspect webhook subscriptions programmatically via Meta Developer Graph APIs.
- **Dynamic Campaign Execution**: Integrate a NATS-backed scheduler to trigger campaign broadcasts dynamically based on database schedules.
