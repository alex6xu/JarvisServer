# OpenClaw-inspired frontend presentation

## Reference and scope

Reference inspected locally: OpenClaw build `2026.9.2-release-3928bad9badf`, `docs/web/control-ui.md`, `docs/web/dashboard-architecture.md`, `dist/control-ui/index.html`, and `assets/control-ui-core-ClxHIvNM.css` / `control-ui-boot-chat-C2G5RI6S.css`.

The source UI uses a 258px navigation rail, compact 32px navigation rows, 48px rail headers, grouped destinations and session history, restrained borders, and Claw charcoal / warm-white surfaces with a red accent. This implementation translates those principles to existing React/Tailwind components; it does not copy Lit, load OpenClaw assets, or introduce dashboard widgets, plugins, commands, or unsupported actions. System fonts remain local; no new font or package dependency.

- Shared application shell and route-aware breadcrumb for both existing layout choices.
- Existing workbench sidebar reused, with labelled workspace/management groups, projects, recent conversations, admin-only account selection, logout and search.
- `classic` stays dark and `workbench` stays light. Existing API-backed appearance storage and authorization are unchanged. No additional theme preference is introduced.
- `control-ui.css` is intentionally loaded after legacy CSS/Tailwind. Explicit hooks scope pane, message, composer and management page changes; shared color tokens also cover existing stock, settings and nested components.
- Chat/code sending, stream parsing, tool rendering, queueing, stop, upload, project/session/model association and local persistence remain unchanged. The existing reduced-motion preference now also controls transcript auto-scroll.
- Mobile navigation supports Escape, Tab wrap and focus restoration; the shell includes a skip link. Login/register labels are associated with inputs. Narrow session details stack; wide management tables scroll.
- Removed the two nonfunctional Dashboard chart placeholders; retained real summary cards and their existing request.
- No backend, endpoint, route definition, dependency or lockfile changes.

## Verification

- `cd web && npm test`: 10 files / 42 tests passed, including 6 new server-rendered UI/navigation cases (both layouts, aliases and nested routes, account permission visibility, loading state, message content and escaping).
- `cd web && npm run build`: TypeScript and Vite production build passed. Vite warns that the main JS chunk exceeds 500kB; code splitting was not added as part of presentation work.
- `go test ./...`: passed.
- `git diff --check`: passed.
- Manual source/diff review covered cascade order, existing route/permission boundaries, no new request contracts, small-screen layout and dark/light overrides. Corrected small-screen session header wrapping and light-theme semantic status contrast during review.

## Not verified

Playwright Core exists in the local OpenClaw installation, but Chromium launch fails because the browser executable is absent; no system Chromium/Chrome was found. No browser screenshots, visual acceptance, keyboard interaction E2E, real-device checks or live authenticated backend flows were performed. Component tests are server-rendered assertions, not browser interaction tests. No separate coding-review CLI was available; review was manual.

Manual follow-up checklist: desktop and 390px mobile in both layouts; sidebar Escape/Tab/return-focus; long session and project names; account switching; Chat/Code streamed output, queue and stop; session restore; document picker and model selection; tables and dialogs on management pages; login/register validation; stock charts.
