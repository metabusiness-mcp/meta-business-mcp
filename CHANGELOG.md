# Changelog

## 2026-06-26 — README Rewrite (Sprint 2 Complete)

### README.md

Complete rewrite reflecting Sprint 2 completion (24 MCP tools, all tests passing).

**Added:**
- Hero section with badges (`Go 1.21+`, `License: Apache 2.0`, `Tests: Passing`, `Tools: 24`)
- "Who is this for?" target audience line
- "Why This Exists" section (3-sentence problem statement)
- All 24 MCP tools listed in compact table format, grouped into 5 categories:
  - Action Operations (7 tools)
  - Read-Only Intelligence (7 tools)
  - Scheduling & Campaign (4 tools)
  - Account & Cost Intelligence (3 tools)
  - Operational (3 tools)
- Tier badges `[All]` / `[Pro]` on every tool
- Campaign Management and Template Lifecycle rows in responsibilities table
- `TIER` and `SCHEDULER_POLL_INTERVAL` environment variables
- Business Model section with OSS / Pro / Enterprise pricing table
- Roadmap section with 6-phase milestone table from PRD §27
- Contributing section with tech stack reference
- License section with copyright notice
- Scheduler and Campaign Module in core services table
- `conversation_pricing` table mentioned in migrations section
- `conversations.status` and `messages.deliver_at` columns in ER diagram
- `scheduled` status in messages ER diagram
- Docs index entry for `walkthrough-4.md` and `mcp-tools-test-report.md`

**Updated:**
- Coverage numbers updated from Sprint 1 to Sprint 2 (13 packages)
- Test suites table expanded to include all handler groups (A–E), scheduler, integration, E2E, failure simulation
- ER diagram updated with `deliver_at`, `status` columns for Sprint 2 schema additions
- Deployment section: added `TIER`, `SCHEDULER_POLL_INTERVAL` to `.env` example
- Configuration section: added `tier` field to `config.yaml` example
- Quick Start reduced to 3 copy-pasteable commands + health check
- `pkg/mcp` description updated to note 6-file split (~2,000 lines)

**Removed:**
- Changelog section (moved to this `CHANGELOG.md`)
- "Potential Inconsistencies Noted" section (inconsistencies fixed)
- Supabase tunnel reference from webhook registration
- Outdated Sprint 1 coverage numbers
- Individual per-tool sections (replaced by compact table format)
- Directory structure section (replaced by documentation index)
- Runtime flow section (covered in architecture docs)
- Local Development "Option B" manual setup (Quick Start is Docker-only)

### config.yaml

- Fixed `http_port: 8082` → `http_port: 8080` (was inconsistent with all documentation)

### docker-compose.yaml

- Removed obsolete `version: '3.8'` attribute (Compose V2 does not require it)