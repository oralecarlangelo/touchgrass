# touchgrass demo stack

Two containers: touchgrass itself plus a toy `demo-api` service to
watch. Ten minutes, no TicketNation infrastructure.

## Run it

```bash
export TOUCHGRASS_ADMIN_PASSWORD='demo-password'
export COMPOSE_PROJECT_NAME=demo
docker compose -f demo/compose.yml up --build -d
curl -s http://127.0.0.1:8080/api/health
# {"status":"ok","version":"..."}
```

## Onboard the toy service

Services are data (`services` table, ADR-0006): one row points
touchgrass at the demo compose project. The seed migration ships
TicketNation rows, so add the demo row with a throwaway sqlite client
against the named volume:

```bash
docker run --rm -v demo_demo-data:/data nouchka/sqlite3 /data/touchgrass.db \
  "INSERT INTO services (id, name, strategy, compose_project, compose_dir, config) VALUES
   ('demo-api', 'demo-api', 'recreate', 'demo', '/tmp/demo',
    '{\"service\":\"demo-api\",\"health_url\":\"http://demo-api:5678/\"}');"
```

Then generate traffic and open the dashboard:

```bash
curl -s http://127.0.0.1:5678/ # → demo-ok (http-echo logs the hit)
open http://127.0.0.1:8080 # log in with the demo password
```

`demo-api` shows live health plus metrics history, and the Logs view
collects the http-echo request lines within one 5s poll.

> `health_url` uses the compose-network name (`demo-api:5678`).
> touchgrass in this stack shares that network, so the name resolves.
> `compose_dir` is unused by the recreate strategy's read paths; the
> placeholder keeps the row valid.

## Tear down

```bash
docker compose -f demo/compose.yml down -v
```

The `-v` drops the demo SQLite volume. Nothing on the host changes.
