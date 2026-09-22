---
layout: home

hero:
  name: touchgrass
  text: Deploys, metrics, and errors in one self-hosted binary
  tagline: Blue-green cutovers with proven downtime, container metrics with alerts, and Sentry-style error tracking unified with log search.
  actions:
    - theme: brand
      text: Quickstart
      link: /quickstart
    - theme: alt
      text: REST API
      link: /api/

features:
  - title: Fail-open Node SDK
    details: Zero dependencies, async everything, PII scrubbing client-side. If touchgrass is down, your app never notices.
  - title: Keyed ingestion
    details: Per-service tg_ keys with sampling rates, revocable in one call. 1MB cap, 202 in milliseconds.
  - title: Grouped issues
    details: Fingerprinted by type, message shape, and top frames. Releases tracked, spikes alerted.
  - title: Errors meet logs
    details: Every issue opens with ±60s of surrounding container logs. No more correlating by timestamp.
---
