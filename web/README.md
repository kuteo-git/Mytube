# Web client

Vite + React + TypeScript + Tailwind v4. No component library — every component is
hand-built against the tokens in `../design-system/local-youtube/MASTER.md`.

```bash
npm install
npm run dev      # http://localhost:5173 (also exposed on the LAN)
npm run build
npx tsc --noEmit -p tsconfig.app.json
```

## Layer rules

```
src/features/<feature>/
  domain/          pure entities — no React, no HTTP
  application/     use cases as hooks — depend on the repository port only
  infrastructure/  repository implementations (currently mock data)
  ui/              components — never call fetch directly
```

Phase 1 runs entirely on `infrastructure/mockData.ts`. When the Go gateway lands,
only `infrastructure/` is replaced; `ui/` and `application/` stay untouched. That is
also what makes the Phase 3 `/tv` interface cheap — it re-implements `ui/` alone.

## Deliberate deviations from youtube.com

Driven by the "no dead buttons" rule in `../CLAUDE.md` §5:

| youtube.com | here |
|---|---|
| Create (+) | **Add video** — the ingest entry point |
| Notification bell | ingest events (downloads finished / failed) |
| Downloads | **Storage** — disk budget and LRU eviction |
| Billing banner | storage-pressure banner |
| Ask (AI) block | removed — out of scope |
| Quality picker | absent until Phase 2 ships the HLS ladder |
| Your videos, YouTube Music/Kids, Shorts, Live, legal footer | removed |
