# touchgrass docs site

VitePress guides + a Redoc API reference, served from the touchgrass
binary at `/docs/`. Both tools are open-source (MIT) and emit static
files — no runtime services, no CDN calls.

```bash
npm install
npm run dev     # vitepress dev server (guides only; API page needs a build)
npm run build   # guides + redoc API page -> dist/
npm run verify  # lint openapi.yaml
```

`dist/` is embedded by `docssite/embed.go` and served by the
`GET /docs/` route (registered ahead of the SPA fallback). The Go
build embeds whatever `dist/` holds, so release pipelines must run
`npm run build` here first (Makefile `build-docs`, CI, goreleaser).

Conventions:

- Guides are task-oriented; reference material lives in `openapi.yaml`.
- Every code sample is copy-pasteable against a default install.
- `openapi.yaml` mirrors the real routes in `internal/http/server.go` —
  update it in the same change that adds a route.
