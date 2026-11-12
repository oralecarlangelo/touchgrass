# Why was I denied?

touchgrass refuses a request in a handful of ways. The response says as
little as possible on purpose, so that it cannot be used to find out
which services or records exist. The **server log** says exactly what
happened. This page maps one to the other.

## Start from what you saw

| You saw | It means | Go to |
| --- | --- | --- |
| "You don't have access" in the console, or `403 forbidden` from the API | Your role or your services do not cover the request | [403 forbidden](#_403-forbidden) |
| `404 not_found` for an issue, log line, rule, notification or key you know exists | The record belongs to a service you were not granted | [404 for a record](#_404-for-a-record-that-exists) |
| A screen is missing from the navigation | The console hides what the API would refuse | [A missing screen](#a-missing-screen) |
| `403 password_change_required` | The account is on a temporary password | [Temporary password](#temporary-password) |
| `429 rate_limited` when signing in | Too many failed attempts | [Too many attempts](#_429-when-signing-in) |
| `401 unauthorized` | No session, or it ended | [401](#_401-unauthorized) |
| `403 env_secret_restricted`, `409 env_pending_by_other` | An env proposal rule, not an access refusal | [Environment & secrets](/env) |

Your role and scope are in the header next to the touchgrass name, for
example `developer · 2 services`. `GET /api/auth/me` returns the same,
with the list of services and capabilities.

## 403 forbidden

Every API route needs one **capability**, and a capability applies in
one of three places (see the [matrix](/access#the-access-matrix)):

- **Own account** capabilities work for every signed-in user.
- **Per-service** capabilities work on the services in your scope.
- **Whole-host** capabilities work only for an account with **all
  services**.

A 403 is one of three reasons. The body is the same for all three, and
the same whether or not the service exists:

| Reason in the log | What happened | What fixes it |
| --- | --- | --- |
| `role` | Your role does not include the capability on any service. For example a viewer starting a deploy, or a developer applying env changes | An admin gives the account a role that includes it (**Users → Access**) |
| `service_out_of_scope` | Your role includes it, but the service you named is not one of yours, does not exist, or you have no services at all | An admin adds the service to the account. Services created after the account was set up are not added automatically |
| `all_services_required` | Your role includes it, but it is a whole-host capability and your account is limited to selected services. Fleet, Security, Databases, Images, the API keys screen and the full audit trail are in this group | An admin changes the scope to **All services**. There is no way to grant one host screen on its own |

## 404 for a record that exists

Issues, log lines, alert rules, issue rules, notifications and SDK keys
are addressed by an id, not by a service. For an account limited to
selected services, an id that belongs to another service answers the
same `404 not_found` as an id that does not exist. Otherwise ids could
be probed to learn what other services contain.

In the log the two cases are distinct: `resource_out_of_scope` (with the
owning service) and `resource_not_found`. An admin can tell which one a
user hit.

## A missing screen

The console hides every screen and button the API would refuse. A
missing **Fleet, Security, Databases, Images, API keys or Users** entry
is normal for an account limited to selected services, and **Users** is
for admins only. Nothing is broken; see
[Users, roles & access](/access) for what each role and scope includes.

## Temporary password

A new account, and one whose password an admin has reset, starts on a
temporary password. Until it is replaced, every route except the session
check, the password change and logout answers
`403 password_change_required`. Sign in to the console and choose a
password, or call `POST /api/auth/password`.

## 429 when signing in

There are two limits on failed logins, with the same answer:

- 5 failures a minute from one client address;
- 10 failures in 15 minutes for one username, from any address.

Wait and try again: attempts refused with 429 are not counted, so the
limit clears by itself, at the latest 15 minutes after the last failed
attempt. If the failures are not yours (someone is guessing at your
username), sign in from a browser you have used before: it is counted
separately and is not held up. An admin sees the attempts in **Audit**
as failed `login` rows with the client address.

Behind a reverse proxy every user has the proxy's address, so the first
limit is shared. Rate-limit `/api/auth/login` at the proxy as well; the
reference nginx config does.

## 401 unauthorized

There is no session, or it ended: sessions last 24 hours, end when the
account is disabled or deleted or its password is reset, and do not
survive a restart of touchgrass. Sign in again.

## For admins: reading the log

Each refusal by an access check writes one line to touchgrass's standard
output, at level `WARN`, with the message `access denied`:

```json
{"time":"2026-11-13T09:14:02Z","level":"WARN","msg":"access denied",
 "user":"dana","role":"developer","capability":"env.apply",
 "service":"billing-worker","method":"POST",
 "route":"/api/services/{id}/env/apply","status":403,"reason":"role"}
```

| Field | Meaning |
| --- | --- |
| `user`, `role` | The signed-in account and its role at the time |
| `capability` | What the route needs. Look it up in the [matrix](/access#the-access-matrix) |
| `service` | The service the request named or the record belongs to. Absent when the request named none, and when the role was refused before the service was looked at (routes that name it in the query or the body, or reach it through a record) |
| `method`, `route` | The route as declared, with placeholders such as `{id}`. Never the actual path or query string |
| `status` | 403 or 404 |
| `reason` | `role`, `service_out_of_scope`, `all_services_required`, `resource_out_of_scope` or `resource_not_found`, as above |

The line never contains a request body, a query string, a header, a
cookie or an env value. Find a user's refusals with whatever reads the
container's output, for example:

```bash
docker logs touchgrass 2>&1 | grep '"msg":"access denied"' | grep '"user":"dana"'
```

Refusals are not in the **Audit** screen. The audit trail records what
changed, and a refused request changed nothing. Two things that look
like refusals are audited, because they are attempts worth seeing:
failed and rate-limited logins, and a developer's attempt to stage a
secret env key.

To check what a role can do without trying it, open **Users → Access
review → What each role can do**, or call `GET /api/access/matrix`.
