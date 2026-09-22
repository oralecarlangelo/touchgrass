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

export async function fetchOccurrences(serviceId: string, limit = 100): Promise<Occurrence[]> {
  const body = await request<OccurrencesResponse>(
    `/api/issues/occurrences?service_id=${encodeURIComponent(serviceId)}&limit=${limit}`,
  );

  return body.occurrences ?? [];
}

interface OccurrencesResponse {
  occurrences: Occurrence[];
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
  level: string;
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
  stream?: string;
  level?: string;
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

  if (search.stream !== undefined && search.stream !== '') {
    params.set('stream', search.stream);
  }

  if (search.level !== undefined && search.level !== '') {
    params.set('level', search.level);
  }

  if (search.limit !== undefined) {
    params.set('limit', String(search.limit));
  }

  const body = await request<LogsResponse>(`/api/logs?${params.toString()}`);

  return body.lines ?? [];
}

export interface LogStats {
  service_id: string;
  lines: number;
  drops: number;
  truncations: number;
}

export async function fetchLogStats(serviceId: string): Promise<LogStats> {
  return request<LogStats>(`/api/logs/stats?service_id=${encodeURIComponent(serviceId)}`);
}

export async function fetchLogContext(
  id: number,
  before = 20,
  after = 20,
): Promise<LogContext> {
  return request<LogContext>(`/api/logs/${id}?before=${before}&after=${after}`);
}

export interface SdkLogAttribute {
  value: string | number | boolean;
  type: string;
}

export interface SdkLogEntry {
  id: number;
  ts: string;
  level: string;
  message: string;
  attributes: Record<string, SdkLogAttribute>;
  trace_id: string;
  span_id: string;
  release: string;
}

interface SdkLogsResponse {
  lines: unknown[];
}

export interface SdkLogSearch {
  q?: string;
  after?: string;
  before?: string;
  level?: string;
  trace_id?: string;
  limit?: number;
}

function pickString(raw: Record<string, unknown>, keys: string[]): string {
  for (const key of keys) {
    const value = raw[key];

    if (typeof value === 'string') {
      return value;
    }
  }

  return '';
}

function pickNumber(raw: Record<string, unknown>, keys: string[]): number {
  for (const key of keys) {
    const value = raw[key];

    if (typeof value === 'number') {
      return value;
    }
  }

  return 0;
}

function normalizeSdkAttributes(raw: unknown): Record<string, SdkLogAttribute> {
  if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) {
    return {};
  }

  const normalized: Record<string, SdkLogAttribute> = {};

  for (const [key, attr] of Object.entries(raw)) {
    if (typeof attr !== 'object' || attr === null || Array.isArray(attr)) {
      continue;
    }

    const value = (attr as Record<string, unknown>).value;

    if (typeof value !== 'string' && typeof value !== 'number' && typeof value !== 'boolean') {
      continue;
    }

    const type = (attr as Record<string, unknown>).type;
    normalized[key] = { value, type: typeof type === 'string' ? type : 'string' };
  }

  return normalized;
}

// The S20 wire shape is snake_case per repo convention; camelCase keys are
// accepted too so a small backend drift degrades instead of blanking rows.
function normalizeSdkLogEntry(raw: unknown): SdkLogEntry {
  if (typeof raw !== 'object' || raw === null) {
    return { id: 0, ts: '', level: '', message: '', attributes: {}, trace_id: '', span_id: '', release: '' };
  }

  const record = raw as Record<string, unknown>;

  return {
    id: pickNumber(record, ['id']),
    ts: pickString(record, ['ts']),
    level: pickString(record, ['level']),
    message: pickString(record, ['message']),
    attributes: normalizeSdkAttributes(record.attributes),
    trace_id: pickString(record, ['trace_id', 'traceId']),
    span_id: pickString(record, ['span_id', 'spanId']),
    release: pickString(record, ['release']),
  };
}

export async function listSdkLogs(serviceId: string, search: SdkLogSearch = {}): Promise<SdkLogEntry[]> {
  const params = new URLSearchParams({ service_id: serviceId, source: 'sdk' });

  if (search.q !== undefined && search.q !== '') {
    params.set('q', search.q);
  }

  if (search.after !== undefined && search.after !== '') {
    params.set('after', search.after);
  }

  if (search.before !== undefined && search.before !== '') {
    params.set('before', search.before);
  }

  if (search.level !== undefined && search.level !== '') {
    params.set('level', search.level);
  }

  if (search.trace_id !== undefined && search.trace_id !== '') {
    params.set('trace_id', search.trace_id);
  }

  if (search.limit !== undefined) {
    params.set('limit', String(search.limit));
  }

  const body = await request<SdkLogsResponse>(`/api/logs?${params.toString()}`);

  return (body.lines ?? []).map(normalizeSdkLogEntry);
}

export async function fetchAudit(serviceId: string, limit = 100): Promise<AuditEntry[]> {
  const params = new URLSearchParams({ limit: String(limit) });

  if (serviceId !== '') {
    params.set('service_id', serviceId);
  }

  const body = await request<AuditResponse>(`/api/audit?${params.toString()}`);

  return body.audit ?? [];
}

export interface ApiKey {
  id: number;
  service_id: string;
  key_prefix: string;
  sample_rate: number;
  revoked_at: string | null;
  created_at: string;
}

interface KeysResponse {
  keys: ApiKey[];
}

export interface CreatedApiKey {
  id: number;
  service_id: string;
  key: string;
  key_prefix: string;
  sample_rate: number;
}

export async function fetchKeys(serviceId: string): Promise<ApiKey[]> {
  const body = await request<KeysResponse>(
    `/api/keys?service_id=${encodeURIComponent(serviceId)}`,
  );

  return body.keys ?? [];
}

export async function createKey(serviceId: string, sampleRate: number): Promise<CreatedApiKey> {
  return request<CreatedApiKey>('/api/keys', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ service_id: serviceId, sample_rate: sampleRate }),
  });
}

export async function revokeKey(id: number): Promise<void> {
  await request<void>(`/api/keys/${id}/revoke`, { method: 'POST' });
}

export interface ImageView {
  id: string;
  repo_tags: string[];
  size_bytes: number;
  created_at: string;
  containers: number;
  dangling: boolean;
}

interface ImagesResponse {
  images: ImageView[];
}

export interface ImagePruneResult {
  deleted: number;
  reclaimed_bytes: number;
}

export async function fetchImages(): Promise<ImageView[]> {
  const body = await request<ImagesResponse>('/api/docker/images');

  return body.images ?? [];
}

export async function pruneImages(): Promise<ImagePruneResult> {
  return request<ImagePruneResult>('/api/docker/images/prune', { method: 'POST' });
}

export interface DaemonSnapshot {
  server_version: string;
  architecture: string;
  operating_system: string;
  kernel_version: string;
  ncpu: number;
  mem_total_bytes: number;
  containers_running: number;
  containers_stopped: number;
  images: number;
}

export interface SelfSnapshot {
  version: string;
  uptime_secs: number;
  database_bytes: number;
}

export interface SystemSnapshot {
  hostname: string;
  os: string;
  arch: string;
  uptime_secs: number | null;
  load_1: number | null;
  cpu_percent: number | null;
  mem_used_bytes: number | null;
  mem_total_bytes: number | null;
  disk_used_bytes: number | null;
  disk_total_bytes: number | null;
  docker: DaemonSnapshot | null;
  self: SelfSnapshot;
}

export async function fetchSystem(): Promise<SystemSnapshot> {
  return request<SystemSnapshot>('/api/system');
}

export interface FleetContainer {
  name: string;
  project: string;
  managed: boolean;
  service_id?: string | null;
  state: string;
  cpu_percent: number;
  mem_bytes: number;
  mem_limit: number;
  restarts: number;
  sampled_at: string;
}

interface FleetContainersResponse {
  containers: FleetContainer[];
}

export async function fetchFleetContainers(): Promise<FleetContainer[]> {
  const body = await request<FleetContainersResponse>('/api/system/containers');

  return body.containers ?? [];
}

export type SystemHistoryMetric = 'cpu' | 'mem' | 'load';

export interface HistoryPoint {
  ts: string;
  value: number;
}

interface SystemHistoryResponse {
  points: HistoryPoint[];
}

export async function fetchSystemHistory(
  metric: SystemHistoryMetric,
  hours: number,
): Promise<HistoryPoint[]> {
  const params = new URLSearchParams({ metric, hours: String(hours) });
  const body = await request<SystemHistoryResponse>(`/api/system/history?${params.toString()}`);

  return body.points ?? [];
}

export interface DatabaseView {
  id: string;
  label: string;
  configured: boolean;
  reachable: boolean;
  version?: string | null;
  size_bytes?: number | null;
  connections_used?: number | null;
  connections_max?: number | null;
  uptime_secs?: number | null;
  used_memory_bytes?: number | null;
  integrity?: string | null;
  last_backup_at?: string | null;
}

interface DatabasesResponse {
  databases: DatabaseView[];
}

export interface DatabaseBackup {
  name: string;
  size_bytes: number;
  created_at: string;
  sha256: string;
}

interface DatabaseBackupsResponse {
  backups: DatabaseBackup[];
}

export interface DatabaseJob {
  id: number | string;
  kind: string;
  target: string;
  status: string;
  detail: string;
  started_at: string;
  finished_at?: string | null;
}

interface DatabaseJobsResponse {
  jobs: DatabaseJob[];
}

interface DatabaseJobStartResponse {
  id: number | string;
  status: string;
}

export async function fetchDatabases(): Promise<DatabaseView[]> {
  const body = await request<DatabasesResponse>('/api/databases');

  return body.databases ?? [];
}

export async function fetchDatabaseBackups(): Promise<DatabaseBackup[]> {
  const body = await request<DatabaseBackupsResponse>('/api/databases/backups');

  return body.backups ?? [];
}

export async function startDatabaseBackup(): Promise<DatabaseJobStartResponse> {
  return request<DatabaseJobStartResponse>('/api/databases/backups', { method: 'POST' });
}

export async function fetchDatabaseJobs(): Promise<DatabaseJob[]> {
  const body = await request<DatabaseJobsResponse>('/api/databases/jobs');

  return body.jobs ?? [];
}

export async function fetchDatabaseJob(id: number | string): Promise<DatabaseJob> {
  return request<DatabaseJob>(`/api/databases/jobs/${encodeURIComponent(String(id))}`);
}

export async function verifyDatabaseBackup(name: string): Promise<{ name: string; ok: boolean }> {
  return request<{ name: string; ok: boolean }>(
    `/api/databases/backups/${encodeURIComponent(name)}/verify`,
    { method: 'POST' },
  );
}

export async function startDatabaseRestore(name: string): Promise<DatabaseJobStartResponse> {
  return request<DatabaseJobStartResponse>('/api/databases/restore', jsonBody({ name }));
}

export type OnboardingStrategy = 'recreate' | 'bluegreen';

export interface OnboardingSuggest {
  container: string;
  service_id: string;
  strategy: string;
  compose_project: string;
  compose_dir: string;
  service: string;
  health_url: string;
  public_url: string;
  deploy_script: string;
  rollback_script: string;
  blue_service?: string;
  green_service?: string;
  blue_target?: string;
  green_target?: string;
  blue_url?: string;
  green_url?: string;
  nginx_conf?: string;
  marker?: string;
  cutover_script?: string;
  confidence: string;
  reasons: string[];
  warnings: string[];
}

export async function suggestService(container: string): Promise<OnboardingSuggest> {
  const params = new URLSearchParams({ container });
  const body = await request<OnboardingSuggest>(`/api/onboarding/suggest?${params.toString()}`);

  return {
    ...body,
    reasons: body.reasons ?? [],
    warnings: body.warnings ?? [],
  };
}

export interface CreateServiceInput {
  id: string;
  name?: string;
  strategy: OnboardingStrategy;
  compose_project: string;
  compose_dir: string;
  config: Record<string, string>;
}

export interface CreatedService {
  id: string;
  strategy: string;
}

export async function createService(input: CreateServiceInput): Promise<CreatedService> {
  return request<CreatedService>('/api/services', jsonBody(input));
}
