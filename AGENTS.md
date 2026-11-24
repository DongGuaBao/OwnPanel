# Repository Guidelines

## Project structure

OwnPanel manages Linux hosts through an outbound Agent or SSH. Build the Go executables from `cmd/server/` and `cmd/agent/`. Shared API handlers, SQLite persistence and host drivers live in `internal/panel/`. Keep platform-specific code in files ending in `_linux.go` or `_windows.go`.

Vue components and TypeScript helpers live in `web/src/`. Vite writes generated assets to `web/dist/`. Installation and packaging scripts live in `scripts/`, and API documentation lives in `docs/`. Initialize the pinned FileSearch dependency with `git submodule update --init --recursive`. When updating it, commit the FileSearch changes upstream before updating the submodule pointer here.

## Build and test

Follow the [README](README.md) to build and start the center. Use Go 1.26.11 or later and Node.js 22. Run these checks from the repository root.

- Run `npm ci --prefix web` to install locked dependencies. Use `npm run dev --prefix web` for development and `npm run build --prefix web` for type checking and production assets.
- Run `go test ./...` and `go vet ./...`. Put regression tests beside their implementations in `*_test.go` files, using names such as `TestFeatureBehavior`. Exercise both Agent and SSH paths for shared host operations.
- Run `node web/read-cache-test.cjs` for inventory caching and `node web/iptables-model-test.cjs` for firewall rule handling. Verify UI changes in a browser on desktop and narrow layouts.

## Code and UI conventions

Format Go with `gofmt`. Use PascalCase for Vue components and kebab-case for TypeScript helpers. Keep the Chinese UI concise. Use Element Plus forms, spacing and dialog slots. Keep layout and scrolling rules in `web/src/layout.css`. Use `PanelTable` with stable column keys, sort numeric and date columns by raw values, and keep range selection in display order. Keep toolbars visible as rows scroll. Dialog content should have a single scroll area.

## Contributions and deployment data

Use concise imperative commit subjects. In pull requests, explain the behavior change, link related issues, report checks, and include screenshots for visual changes. Review staged files before committing. Keep credentials, logs, databases and runtime data under private paths. Run privileged Agent and firewall integration checks on an isolated host.
