# Users, roles & access

Every account has a **role** and a **scope**. The role decides what the
user may do. The scope decides on which services.

| Role        | What it may do                                                    |
| ----------- | ----------------------------------------------------------------- |
| `admin`     | Everything: deploy, manage env and secrets, manage users. Always every service |
| `developer` | Read, plus operate: deploy, cut over, roll back, record a manual deploy, manage alert and issue rules, mark notifications read, propose non-secret env changes. On every service, or only on the services you select |
| `viewer`    | Read only. On every service, or only on the services you select   |

| Scope                 | What it covers                                                          |
| --------------------- | ----------------------------------------------------------------------- |
| **All services**      | Every service, services added later, and the host screens               |
| **Selected services** | Only the listed services. No host screens, and nothing of other services |

The host screens are Fleet, Security, Databases, Images, API keys,
fleet-wide Performance, and the audit rows that belong to no service
(logins, user management). They describe the whole machine, including
containers of other stacks, so they cannot be cut down to one service.
A user limited to selected services never sees them.

Accounts that existed before scopes were introduced keep their role and
have all services, so they work as they always did. No account becomes a
developer on its own: an admin has to give it that role.

## Give a developer limited access

1. Open **Users** and choose **New user**.
2. Enter a username and a **temporary password** (12 characters or more).
3. Choose the role: **viewer** to let them look, **developer** to let
   them also deploy the services you select.
4. Under **Services**, choose **Selected services** and tick the services
   they work on.
5. Create the user and pass them the temporary password directly.
   touchgrass sends no email.

At their first login they must choose their own password before they can
do anything else. After that they see their services and nothing more.

To change someone's access later, use **Access** on their row. The
change applies to their next request, and pages they have open follow
within a few seconds. Limiting a user who had every service asks you to
confirm, because it removes the host screens and every other service at
once.

## What a user limited to selected services sees

For each of their services: the service card and health, metrics and
performance, deploy history, logs and issues, alert and issue rules,
notifications, env key names with non-secret values, and the audit rows
of that service.

They do not see, and the API refuses:

- any other service, on any route, list, or live event stream;
- the host screens listed above;
- logins and user management in the audit trail;
- for a viewer, anything that changes state: deploys, env changes,
  rules, keys, users.

## What a developer can do

A developer reads everything a viewer reads. On the services in their
scope, and only there, they can also:

- **deploy, cut over and roll back**. This runs the deploy script an
  admin configured for that service, with nothing the developer can add
  to it: the only choice is the service and, for a cutover, the target
  colour;
- **record a manual deploy** in the history. The entry always carries
  their own username; an `actor` sent in the request is ignored;
- **create and delete alert rules and issue rules**;
- **mark notifications read**, one at a time or all at once. The read
  flag is shared, so a notification they mark read is read for everyone;
- **propose env changes**: add a new key, or change or remove a key that
  is not secret. The change is staged under their name and waits for an
  admin to apply it. See [Environment & secrets](env.md#proposals-from-developers).

Each of these writes an audit row under the developer's name against the
service it concerned, so anyone who can read that service can see who
deployed it, who changed its rules and who dismissed its alerts.

A developer cannot, on any service:

- apply, import, discard or reveal env entries, or roll an env revision
  back;
- stage, remove or re-flag a secret env key, or mark any key secret;
- replace or withdraw another user's pending env change;
- mint or revoke SDK keys;
- create, change or delete a service, its deploy script, its validate
  or restart command;
- manage users, databases, security findings, images or containers.

Give the role to people you would let run that service's deploy script
on the host. If someone should only look, leave them a viewer.

A user who has been granted no service yet sees a single message telling
them to ask an admin.

## Before you grant a service

- **Granting a service grants what its logs contain.** touchgrass does
  not redact log lines or error reports. If a service logs secrets or
  personal data, whoever can read that service can read them.
- **Check the secret flags first.** A user who can read a service sees
  the values of env entries that are not flagged secret. The env import
  guesses which keys are secret and can guess wrong. Open the service's
  Environment tab and review the flags before granting it.
- **New services are not granted automatically.** A user with selected
  services does not see a service you add later until you tick it. Only
  all-services users see new services at once.
- **Listing every service is not "all services".** It gives no host
  screens and does not include future services.
- **Deleting a service removes its grants.** A service created again
  under the same id starts with none.

## Passwords and sessions

- A new account, and an account whose password an admin reset in
  **Users**, holds a temporary password: it must be replaced at the next
  login. An admin's reset also ends that user's sessions at once.
- Everyone can change their own password from the account menu. That
  ends their other sessions.
- Disabling or deleting an account stops its next request.
- The last enabled admin cannot be demoted, limited, disabled or deleted.

## The access matrix

Each thing a user can do is a **capability**. `service` capabilities
need the service to be in the user's scope. `fleet` capabilities need
the all-services scope. `self` applies to the user's own account.

| Capability | Covers | Scope | viewer | developer | admin |
| --- | --- | --- | --- | --- | --- |
| `session.self` | Session, logout, live event stream, own password | self | yes | yes | yes |
| `service.read` | Service list entry, health, containers | service | yes | yes | yes |
| `metrics.read` | Service metrics and per-service performance | service | yes | yes | yes |
| `deploy.read` | Deploy history | service | yes | yes | yes |
| `deploy.trigger` | Deploy, cutover, rollback | service | no | yes | yes |
| `deploy.record` | Record a manual deploy | service | no | yes | yes |
| `logs.read` | Log search, stats, line context, issue logs | service | yes | yes | yes |
| `issues.read` | Issues, occurrences, issue detail | service | yes | yes | yes |
| `alerts.read` | Alert rules, issue rules, notifications | service | yes | yes | yes |
| `alerts.manage` | Create and delete alert and issue rules | service | no | yes | yes |
| `notifications.ack` | Mark notifications read | service | no | yes | yes |
| `env.read_keys` | Env key names, flags, non-secret values, diff, revisions, runs | service | yes | yes | yes |
| `env.stage` | Stage or remove an env entry. A developer proposes: new keys and non-secret keys only | service | no | yes | yes |
| `env.discard` | Drop all pending env changes | service | no | no | yes |
| `env.import` | Import the env file from the host | service | no | no | yes |
| `env.apply` | Apply env changes, roll back to a revision | service | no | no | yes |
| `env.reveal` | Decrypt one secret value | service | no | no | yes |
| `keys.read` | List SDK keys (prefix only) | service | yes | yes | yes |
| `keys.manage` | Mint and revoke SDK keys | service | no | no | yes |
| `audit.read` | Audit rows of a service | service | yes | yes | yes |
| `audit.read_all` | Audit rows of no service: logins, user management, host actions | fleet | yes | yes | yes |
| `host.read` | System, host history, container inventory, images, collector status, fleet-wide performance | fleet | yes | yes | yes |
| `host.manage` | Image prune, container approve and ignore | fleet | no | no | yes |
| `security.read` | Security summary, findings, policies | fleet | yes | yes | yes |
| `security.manage` | Finding workflow and policy changes | fleet | no | no | yes |
| `databases.read` | Database health, backup list, jobs | fleet | yes | yes | yes |
| `databases.backup` | Start and verify a backup | fleet | no | no | yes |
| `databases.restore` | Restore a backup | fleet | no | no | yes |
| `services.manage` | Create and delete a service, onboarding suggestion | fleet | no | no | yes |
| `users.manage` | All user and access management, and the access matrix | fleet | no | no | yes |

Reading the table for a user limited to selected services: every `fleet`
row is "no", whatever the role. The API key screen is a host screen in
the console, although the API lets a scoped user list the key prefixes of
their own services.

A developer holds the viewer's rows plus `deploy.trigger`,
`deploy.record`, `alerts.manage`, `notifications.ack` and `env.stage`.
All five are `service` capabilities, so the role adds no host-level power even with
the all-services scope.

`env.stage` is part of the role too, and for a developer it is narrower
than the name suggests. The capability opens the two stage routes; past
them a user who does not also hold `env.apply` may only add new keys and
change or remove keys that are not secret. Everything touching a secret
answers 403 `env_secret_restricted`.

## How the API answers

The console hides what a user cannot use, but the API is what enforces
it. A raw HTTP client gains nothing.

| Request | Answer |
| --- | --- |
| The role lacks the capability | 403 `forbidden` |
| A service outside the scope, or one that does not exist (scoped user) | 403 `forbidden`, same body for both |
| A resource id (issue, log line, rule, notification) owned by a service outside the scope, or missing (scoped user) | 404 `not_found`, same body for both |
| `POST /api/notifications/read` without `service_id` (scoped developer) | Marks only the user's services |
| `POST /api/services/{id}/deploys` with an `actor` in the body (not an admin) | The `actor` is ignored; the entry carries the caller's username |
| `PUT` or `DELETE …/env/entries/{KEY}` on a secret key, or with `is_secret: true` (developer) | 403 `env_secret_restricted`, the same body whatever value was sent, and an `env.stage` failure audit row |
| `PUT` or `DELETE …/env/entries/{KEY}` on a key with another user's pending change (developer) | 409 `env_pending_by_other` |
| `GET …/env/diff`, `…/env/revisions` (not an admin) | The same lists without `rendered_sha256` |
| A host-level route, for a user limited to selected services | 403 `forbidden` |
| `GET /api/services`, `/api/notifications`, `/api/audit` | Only the rows of the user's services |
| Any route but session, password and logout, on a temporary password | 403 `password_change_required` |

`GET /api/auth/me` returns `all_services`, `services` and
`capabilities` for the signed-in user. In the [REST API](/api/)
reference every operation states its capability and scope as
`x-touchgrass-capability` and `x-touchgrass-scope`.

Refused requests are written to the server log as `access denied` with
the user, role, capability, service, route and the reason. They are not
audit rows. [Why was I denied?](/access-denied) explains how to read
them.

## Review who can do what

At the bottom of **Users**, **Access review** answers the question from
both sides.

- **By service** lists every service with the accounts that can reach
  it and their role. An account marked "all" has every service, so it
  also reaches services added later; the others were granted that
  service by name. A last row shows who sees the host screens, and
  accounts that were granted nothing are listed below the table.
  Disabled accounts are shown struck through: they keep their access on
  paper and cannot sign in.
- **What each role can do** is the matrix above as the server holds it,
  grouped by where a capability applies. "Limited" marks a grant that
  is narrower than its name, with the limit spelled out (a developer's
  `env.stage` is a proposal).

The matrix is served by `GET /api/access/matrix` (admin only) from the
same table the server enforces, so the screen cannot disagree with what
a request is actually allowed to do.

```bash
curl -b cookies https://touchgrass.example.com/api/access/matrix
```

```json
{
  "roles": ["viewer", "developer", "admin"],
  "capabilities": [
    {
      "capability": "env.stage",
      "scope": "service",
      "summary": "Stage or remove an env entry",
      "roles": { "viewer": false, "developer": true, "admin": true },
      "limits": { "developer": "Proposes only: may add a key and change or remove a non-secret key. May not touch a secret key or another user's pending change. An admin applies." }
    }
  ]
}
```

## Signing in: the attempt limits

Failed logins are limited in two ways, and both answer
`429 rate_limited` ("too many attempts, try again later"):

- **5 failures a minute from one client address**, whoever the
  usernames were.
- **10 failures in 15 minutes for one username**, from any address.

The limits count failures only, and an attempt refused with 429 is not
counted, so waiting is always enough: 15 minutes after the last failed
attempt the username is open again.

A browser that has signed in as a user before (in the last 30 days) is
counted separately from everyone else. If someone else is guessing at
your username and has used up its limit, the browser you normally use
still lets you in. A browser you have never used shares the limit with
the guesser until it clears.

Wrong passwords, unknown usernames and disabled accounts all get the
same `401` in the same time, and reach the limit the same way. See
[ADR-0017](https://github.com/oralecarlangelo/touchgrass/blob/develop/docs/adr/0017-per-username-login-throttle.md)
for the reasoning.

## From the API

```bash
# Create a viewer limited to one service
curl -b cookies -X POST https://touchgrass.example.com/api/users \
  -H 'Content-Type: application/json' \
  -d '{"username":"dana","password":"a-temporary-pass","role":"viewer",
       "all_services":false,"services":["billing-worker"]}'

# Replace a user's role and scope in one step
curl -b cookies -X PUT https://touchgrass.example.com/api/users/7/access \
  -H 'Content-Type: application/json' \
  -d '{"role":"viewer","all_services":false,"services":["billing-worker","docs-site"]}'

# Create a developer who may deploy one service
curl -b cookies -X POST https://touchgrass.example.com/api/users \
  -H 'Content-Type: application/json' \
  -d '{"username":"sam","password":"a-temporary-pass","role":"developer",
       "all_services":false,"services":["billing-worker"]}'
```

An unknown role or service answers 400. Limiting an admin to a list, or
taking the admin role from the last enabled admin, answers 409.
