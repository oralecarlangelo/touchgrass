// API client. Field names match the Go JSON wire shape (snake_case).

export interface ContainerView {
  id: string;
  name: string;
  image: string;
  sha: string;
  state: string;
  status: string;
  ports: string[];
}

export interface ColorView {
  name: string;
  live: boolean;
  health: string;
  target: string;
  health_url: string;
}

export interface ServiceView {
  id: string;
  name: string;
  strategy: string;
  live_color: string;
  health: string;
  colors: ColorView[];
  containers: ContainerView[];
}

interface ServicesResponse {
  services: ServiceView[];
}

export interface Metric {
  id: number;
  service_id: string;
  container_name: string;
  sampled_at: string;
  cpu_percent: number;
  mem_bytes: number;
  mem_limit: number;
  disk_bytes: number;
  restarts: number;
  uptime_secs: number;
}

interface MetricsResponse {
  metrics: Metric[];
}

export interface AlertRule {
  id: number;
  service_id: string;
  metric: string;
  threshold: number;
  duration_secs: number;
  enabled: boolean;
  created_at: string;
}

interface RulesResponse {
  rules: AlertRule[];
}

export interface Deploy {
  id: number;
  service_id: string;
  sha: string;
  actor: string;
  type: string;
  outcome: string;
  started_at: string | null;
  finished_at: string | null;
  duration_secs: number | null;
  downtime_secs: number | null;
  notes: string;
  created_at: string;
}

interface DeploysResponse {
  deploys: Deploy[];
}

interface ErrorResponse {
  error: string;
  code: string;
}

class ApiError extends Error {
  status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
  }
}

export function isUnauthorized(error: unknown): boolean {
  return error instanceof ApiError && error.status === 401;
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  let res: Response;

  try {
    res = await fetch(path, { credentials: 'same-origin', ...init });
  } catch (err) {
    throw new Error(`API unreachable: ${err instanceof Error ? err.message : String(err)}`);
  }

  if (res.status === 204) {
    return undefined as T;
  }

  if (!res.ok) {
    let detail = `HTTP ${res.status}`;

    try {
      const body = (await res.json()) as ErrorResponse;
      if (body.error !== '') {
        detail = body.error;
      }
    } catch {
      // Fall through with the HTTP status.
    }

    throw new ApiError(res.status, detail);
  }

  return (await res.json()) as T;
}

function jsonBody(body: unknown): RequestInit {
  return {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  };
}

export async function fetchServices(): Promise<ServiceView[]> {
  const body = await request<ServicesResponse>('/api/services');

  return body.services ?? [];
}

export async function fetchMetrics(serviceId: string): Promise<Metric[]> {
  const body = await request<MetricsResponse>(
    `/api/services/${encodeURIComponent(serviceId)}/metrics?limit=100`,
  );

  return body.metrics ?? [];
}

export async function fetchRules(serviceId: string): Promise<AlertRule[]> {
  const body = await request<RulesResponse>(
    `/api/alerts/rules?service_id=${encodeURIComponent(serviceId)}`,
  );

  return body.rules ?? [];
}

export async function createRule(input: {
  service_id: string;
  metric: string;
  threshold: number;
  duration_secs: number;
}): Promise<AlertRule> {
  return request<AlertRule>('/api/alerts/rules', jsonBody(input));
}

export async function deleteRule(id: number): Promise<void> {
  await request<undefined>(`/api/alerts/rules/${id}`, { method: 'DELETE' });
}

export async function fetchDeploys(serviceId: string): Promise<Deploy[]> {
  const body = await request<DeploysResponse>(
    `/api/services/${encodeURIComponent(serviceId)}/deploys?limit=50`,
  );

  return body.deploys ?? [];
}

export async function recordDeploy(
  serviceId: string,
  input: { sha: string; actor: string; outcome: string; notes: string },
): Promise<{ id: number }> {
  return request<{ id: number }>(
    `/api/services/${encodeURIComponent(serviceId)}/deploys`,
    jsonBody(input),
  );
}

export async function fetchSession(): Promise<boolean> {
  try {
    const body = await request<{ authenticated: boolean }>('/api/auth/me');

    return body.authenticated;
  } catch (err) {
    if (isUnauthorized(err)) {
      return false;
    }

    throw err;
  }
}

export async function login(password: string): Promise<void> {
  await request<undefined>('/api/auth/login', jsonBody({ password }));
}

export async function logout(): Promise<void> {
  await request<undefined>('/api/auth/logout', { method: 'POST' });
}

export async function startCutover(
  serviceId: string,
  target: string,
): Promise<{ target: string }> {
  return request<{ target: string }>(
    `/api/services/${encodeURIComponent(serviceId)}/cutover`,
    jsonBody({ target }),
  );
}

export async function startRollback(serviceId: string): Promise<{ target: string }> {
  return request<{ target: string }>(
    `/api/services/${encodeURIComponent(serviceId)}/rollback`,
    { method: 'POST' },
  );
}

export async function startDeploy(serviceId: string): Promise<{ target: string }> {
  return request<{ target: string }>(`/api/services/${encodeURIComponent(serviceId)}/deploy`, {
    method: 'POST',
  });
}

export interface Notification {
  id: number;
  service_id: string;
  kind: string;
  title: string;
  body: string;
  created_at: string;
  read_at: string | null;
}

interface NotificationsResponse {
  notifications: Notification[];
  unread: number;
}

export async function fetchNotifications(
  serviceId: string,
  limit = 100,
): Promise<{ notifications: Notification[]; unread: number }> {
  const params = new URLSearchParams({ limit: String(limit) });

  if (serviceId !== '') {
    params.set('service_id', serviceId);
  }

  const body = await request<NotificationsResponse>(`/api/notifications?${params.toString()}`);

  return { notifications: body.notifications ?? [], unread: body.unread ?? 0 };
}

export async function markNotificationRead(id: number): Promise<void> {
  await request<undefined>(`/api/notifications/${id}/read`, { method: 'POST' });
}

export async function markNotificationsRead(serviceId: string): Promise<{ marked: number }> {
  const params = new URLSearchParams();

  if (serviceId !== '') {
    params.set('service_id', serviceId);
  }

  const query = params.toString();

  return request<{ marked: number }>(
    `/api/notifications/read${query === '' ? '' : `?${query}`}`,
    { method: 'POST' },
  );
}

export interface AuditEntry {
  id: number;
  service_id: string | null;
  actor: string;
  action: string;
  result: string;
  detail: string;
  created_at: string;
}

interface AuditResponse {
  audit: AuditEntry[];
}

export interface StackFrame {
  function: string;
  file: string;
  line: number;
  column: number;
}

export interface Breadcrumb {
  at: string;
  category: string;
  message: string;
}

export interface Occurrence {
  id: number;
  service_id: string;
  type: string;
  message: string;
  stack: StackFrame[];
  breadcrumbs: Breadcrumb[];
  release: string;
  issue_id: number | null;
  created_at: string;
}

export interface Issue {
  id: number;
  service_id: string;
  fingerprint: string;
  title: string;
  first_seen: string;
  last_seen: string;
  count: number;
  releases: string[];
  notified_at: string | null;
  created_at: string;
}

interface IssuesResponse {
  issues: Issue[];
}

interface IssueDetailResponse {
  issue: Issue;
  occurrences: Occurrence[];
}

export interface IssueRule {
  id: number;
  service_id: string;
  kind: string;
  threshold: number;
  window_secs: number;
  enabled: boolean;
  created_at: string;
}

interface IssueRulesResponse {
  rules: IssueRule[];
}

export async function fetchIssues(serviceId: string): Promise<Issue[]> {
  const body = await request<IssuesResponse>(
    `/api/issues?service_id=${encodeURIComponent(serviceId)}`,
  );

  return body.issues ?? [];
}

export async function fetchIssue(id: number, limit = 200): Promise<IssueDetailResponse> {
  const body = await request<IssueDetailResponse>(`/api/issues/${id}?limit=${limit}`);

  return { issue: body.issue, occurrences: body.occurrences ?? [] };
}

interface IssueLogsResponse {
  logs: LogLine[];
}

export async function fetchIssueLogs(
  id: number,
  windowSecs = 60,
  limit = 100,
): Promise<LogLine[]> {
  const params = new URLSearchParams({
    window_secs: String(windowSecs),
    limit: String(limit),
  });
  const body = await request<IssueLogsResponse>(`/api/issues/${id}/logs?${params.toString()}`);

  return body.logs ?? [];
}

export async function fetchIssueRules(serviceId: string): Promise<IssueRule[]> {
  const body = await request<IssueRulesResponse>(
    `/api/issues/rules?service_id=${encodeURIComponent(serviceId)}`,
  );

  return body.rules ?? [];
}

export async function createIssueRule(input: {
  service_id: string;
  kind: string;
  threshold: number;
  window_secs: number;
}): Promise<IssueRule> {
  return request<IssueRule>('/api/issues/rules', jsonBody(input));
}

export async function deleteIssueRule(id: number): Promise<void> {
  await request<undefined>(`/api/issues/rules/${id}`, { method: 'DELETE' });
}

export interface LogLine {
  id: number;
  service_id: string;
  container: string;
  stream: string;
  line: string;
  ts: string;
  created_at: string;
}

export interface LogContext {
  anchor: LogLine;
  before: LogLine[];
  after: LogLine[];
}

interface LogsResponse {
  lines: LogLine[];
}

export interface LogSearch {
  q?: string;
  after?: string;
  before?: string;
  limit?: number;
}

export async function fetchLogs(serviceId: string, search: LogSearch = {}): Promise<LogLine[]> {
  const params = new URLSearchParams({ service_id: serviceId });

  if (search.q !== undefined && search.q !== '') {
    params.set('q', search.q);
  }

  if (search.after !== undefined && search.after !== '') {
    params.set('after', search.after);
  }

  if (search.before !== undefined && search.before !== '') {
    params.set('before', search.before);
  }

  if (search.limit !== undefined) {
    params.set('limit', String(search.limit));
  }

  const body = await request<LogsResponse>(`/api/logs?${params.toString()}`);

  return body.lines ?? [];
}

export async function fetchLogContext(
  id: number,
  before = 20,
  after = 20,
): Promise<LogContext> {
  return request<LogContext>(`/api/logs/${id}?before=${before}&after=${after}`);
}

export async function fetchAudit(serviceId: string, limit = 100): Promise<AuditEntry[]> {
  const params = new URLSearchParams({ limit: String(limit) });

  if (serviceId !== '') {
    params.set('service_id', serviceId);
  }

  const body = await request<AuditResponse>(`/api/audit?${params.toString()}`);

  return body.audit ?? [];
}
