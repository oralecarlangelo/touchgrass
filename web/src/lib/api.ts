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

export type PerformanceResolution = 'auto' | 'minute' | 'hour';
export type PerformanceRouteSort = 'p95_ms' | 'request_count' | 'error_rate' | 'route_family';

export interface PerformanceFilters {
  serviceId?: string;
  start?: string;
  end?: string;
  resolution?: PerformanceResolution;
  routeFamily?: string;
  sort?: PerformanceRouteSort;
  limit?: number;
}

export interface PerformanceSnapshot {
  request_count: number;
  error_count: number;
  error_rate: number;
  average_ms: number;
  p50_ms: number;
  p95_ms: number;
  p99_ms: number;
}

export interface PerformanceAnomaly {
  kind: string;
  score: number;
  summary: string;
}

export interface PerformanceSummaryView extends PerformanceSnapshot {
  start: string;
  end: string;
  resolution: Exclude<PerformanceResolution, 'auto'>;
  previous: PerformanceSnapshot;
  anomalies: PerformanceAnomaly[];
}

export interface PerformanceTimelinePoint {
  bucket_start: string;
  request_count: number;
  error_count: number;
  error_rate: number;
  p50_ms: number;
  p95_ms: number;
  p99_ms: number;
  log_errors: number;
  log_warnings: number;
  cpu_percent: number | null;
  memory_percent: number | null;
  postgres_connection_pct: number | null;
  redis_evictions: number | null;
}

export interface RuntimeMarker {
  source_event_id: string;
  occurred_at: string;
  service_id: string;
  environment: string;
  event_type: string;
  container_name: string;
  compose_service: string;
  expected: boolean;
}

export interface PerformanceTimelineView {
  start: string;
  end: string;
  resolution: Exclude<PerformanceResolution, 'auto'>;
  points: PerformanceTimelinePoint[];
  events: RuntimeMarker[];
  deployments: Deploy[];
}

export interface PerformanceRouteRow extends PerformanceSnapshot {
  method: string;
  route_family: string;
  previous: PerformanceSnapshot;
}

export interface PostgresPerformanceRow {
  bucket_start: string;
  service_id: string;
  environment: string;
  metric: string;
  value: number;
  delta: number;
  reset: boolean;
}

export interface RedisPerformanceRow extends PostgresPerformanceRow {
  command_family?: string;
}

export interface CollectorStatusView {
  source: string;
  last_success_at?: string;
  last_recorded_at?: string;
  last_error?: string;
  dropped_records: number;
  stale: boolean;
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

const performanceResolutions = new Set<PerformanceResolution>(['auto', 'minute', 'hour']);
const performanceSorts = new Set<PerformanceRouteSort>([
  'p95_ms',
  'request_count',
  'error_rate',
  'route_family',
]);

function performanceQuery(filters: PerformanceFilters): string {
  const params = new URLSearchParams();
  const start = filters.start === undefined ? null : new Date(filters.start);
  const end = filters.end === undefined ? null : new Date(filters.end);

  if (start !== null && !Number.isNaN(start.getTime())) {
    params.set('start', start.toISOString());
  }
  if (end !== null && !Number.isNaN(end.getTime())) {
    params.set('end', end.toISOString());
  }
  if (filters.resolution !== undefined && performanceResolutions.has(filters.resolution)) {
    params.set('resolution', filters.resolution);
  }
  if (
    filters.routeFamily !== undefined &&
    filters.routeFamily.trim() !== '' &&
    filters.routeFamily.length <= 160
  ) {
    params.set('route_family', filters.routeFamily.trim());
  }
  if (filters.sort !== undefined && performanceSorts.has(filters.sort)) {
    params.set('sort', filters.sort);
  }
  if (
    filters.limit !== undefined &&
    Number.isInteger(filters.limit) &&
    filters.limit >= 1 &&
    filters.limit <= 100
  ) {
    params.set('limit', String(filters.limit));
  }

  const encoded = params.toString();

  return encoded === '' ? '' : `?${encoded}`;
}

export function performanceApiPath(
  resource: 'summary' | 'timeline' | 'routes' | 'postgres' | 'redis',
  filters: PerformanceFilters = {},
): string {
  const service = filters.serviceId?.trim();
  const base =
    service === undefined || service === ''
      ? `/api/performance/${resource}`
      : `/api/services/${encodeURIComponent(service)}/performance/${resource}`;

  return `${base}${performanceQuery(filters)}`;
}

export async function fetchPerformanceSummary(
  filters: PerformanceFilters = {},
): Promise<PerformanceSummaryView> {
  const result = await request<PerformanceSummaryView>(performanceApiPath('summary', filters));

  return { ...result, anomalies: result.anomalies ?? [] };
}

export async function fetchPerformanceTimeline(
  filters: PerformanceFilters = {},
): Promise<PerformanceTimelineView> {
  const result = await request<PerformanceTimelineView>(performanceApiPath('timeline', filters));

  return {
    ...result,
    points: result.points ?? [],
    events: result.events ?? [],
    deployments: result.deployments ?? [],
  };
}

export async function fetchPerformanceRoutes(
  filters: PerformanceFilters = {},
): Promise<PerformanceRouteRow[]> {
  const result = await request<{ routes: PerformanceRouteRow[] }>(
    performanceApiPath('routes', filters),
  );

  return result.routes ?? [];
}

export async function fetchPerformancePostgres(
  filters: PerformanceFilters = {},
): Promise<PostgresPerformanceRow[]> {
  const result = await request<{ metrics: PostgresPerformanceRow[] }>(
    performanceApiPath('postgres', filters),
  );

  return result.metrics ?? [];
}

export async function fetchPerformanceRedis(
  filters: PerformanceFilters = {},
): Promise<RedisPerformanceRow[]> {
  const result = await request<{ metrics: RedisPerformanceRow[] }>(
    performanceApiPath('redis', filters),
  );

  return result.metrics ?? [];
}

export async function fetchCollectorStatuses(): Promise<CollectorStatusView[]> {
  const result = await request<{ collectors: CollectorStatusView[] }>('/api/collectors/status');

  return result.collectors ?? [];
}

export async function deleteService(serviceId: string): Promise<void> {
  await request<undefined>(`/api/services/${encodeURIComponent(serviceId)}`, { method: 'DELETE' });
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

export interface SessionInfo {
  authenticated: boolean;
  username: string;
  role: string;
}

export async function fetchSession(): Promise<SessionInfo> {
  try {
    return await request<SessionInfo>('/api/auth/me');
  } catch (err) {
    if (isUnauthorized(err)) {
      return { authenticated: false, username: '', role: '' };
    }

    throw err;
  }
}

export async function login(username: string, password: string): Promise<void> {
  await request<undefined>('/api/auth/login', jsonBody({ username, password }));
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

export type SecuritySeverity = 'low' | 'medium' | 'high' | 'critical';
export type SecurityConfidence = 'low' | 'medium' | 'high';
export type SecurityCategory = 'posture' | 'process' | 'drift';
export type SecurityFindingStatus = 'open' | 'acknowledged' | 'resolved' | 'suppressed';

export interface SecuritySummary {
  open: number;
  critical: number;
  high: number;
  suppressed: number;
  coverage_complete: boolean;
  last_observed_at?: string | null;
}

export interface SecurityFinding {
  id: number;
  fingerprint: string;
  episode: number;
  container_id: string;
  rule_id: string;
  category: SecurityCategory;
  severity: SecuritySeverity;
  confidence: SecurityConfidence;
  status: SecurityFindingStatus;
  occurrence_count: number;
  evidence: Record<string, string>;
  opened_at: string;
  last_seen_at: string;
  acknowledged_at?: string | null;
  acknowledged_by?: string;
  resolved_at?: string | null;
  resolved_by?: string;
  suppressed_until?: string | null;
  suppression_reason?: string;
  suppression_actor?: string;
  notification_status?: '' | 'delivered' | 'degraded';
}

export interface SecurityFindingFilters {
  status?: SecurityFindingStatus;
  severity?: SecuritySeverity;
  containerId?: string;
  sort?: 'last_seen' | 'opened' | 'severity';
  limit?: number;
  offset?: number;
}

export interface SecurityPolicyBody {
  allow_privileged?: boolean;
  allow_host_pid?: boolean;
  allow_host_network?: boolean;
  allow_docker_socket?: boolean;
  allow_root?: boolean;
  allow_writable_root_fs?: boolean;
  allowed_capabilities?: string[];
  allowed_published_ports?: string[];
  allowed_executable_paths?: string[];
  allowed_writable_paths?: string[];
}

export interface SecurityPolicy extends SecurityPolicyBody {
  id: number;
  scope: 'global' | 'service' | 'container';
  scope_id: string;
  updated_by: string;
  updated_at: string;
}

export async function fetchSecuritySummary(): Promise<SecuritySummary> {
  return request<SecuritySummary>('/api/security/summary');
}

export async function fetchSecurityFindings(filters: SecurityFindingFilters = {}): Promise<SecurityFinding[]> {
  const params = new URLSearchParams();
  if (filters.status !== undefined) params.set('status', filters.status);
  if (filters.severity !== undefined) params.set('severity', filters.severity);
  if (filters.containerId !== undefined && filters.containerId !== '') params.set('container_id', filters.containerId);
  if (filters.sort !== undefined) params.set('sort', filters.sort);
  if (filters.limit !== undefined) params.set('limit', String(filters.limit));
  if (filters.offset !== undefined) params.set('offset', String(filters.offset));
  const query = params.toString();
  const body = await request<{ findings?: SecurityFinding[] }>(`/api/security/findings${query === '' ? '' : `?${query}`}`);
  return (body.findings ?? []).map(normalizeSecurityFinding);
}

export async function fetchSecurityFinding(id: number): Promise<SecurityFinding> {
  return normalizeSecurityFinding(await request<SecurityFinding>(`/api/security/findings/${id}`));
}

export async function acknowledgeSecurityFinding(id: number): Promise<SecurityFinding> {
  return normalizeSecurityFinding(await request<SecurityFinding>(`/api/security/findings/${id}/acknowledge`, { method: 'POST' }));
}

export async function resolveSecurityFinding(id: number): Promise<SecurityFinding> {
  return normalizeSecurityFinding(await request<SecurityFinding>(`/api/security/findings/${id}/resolve`, { method: 'POST' }));
}

export async function suppressSecurityFinding(id: number, reason: string, until: string | null): Promise<SecurityFinding> {
  return normalizeSecurityFinding(await request<SecurityFinding>(`/api/security/findings/${id}/suppress`, jsonBody({ reason, until })));
}

export async function fetchSecurityPolicies(): Promise<SecurityPolicy[]> {
  const body = await request<{ policies?: SecurityPolicy[] }>('/api/security/policies');
  return (body.policies ?? []).map(normalizeSecurityPolicy);
}

export async function createSecurityPolicy(scope: SecurityPolicy['scope'], scopeId: string, policy: SecurityPolicyBody): Promise<SecurityPolicy> {
  return normalizeSecurityPolicy(await request<SecurityPolicy>('/api/security/policies', jsonBody({ scope, scope_id: scopeId, policy })));
}

export async function updateSecurityPolicy(id: number, policy: SecurityPolicyBody): Promise<SecurityPolicy> {
  return normalizeSecurityPolicy(await request<SecurityPolicy>(`/api/security/policies/${id}`, {
    method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ policy }),
  }));
}

export async function deleteSecurityPolicy(id: number): Promise<void> {
  await request<undefined>(`/api/security/policies/${id}`, { method: 'DELETE' });
}

function normalizeSecurityFinding(finding: SecurityFinding): SecurityFinding {
  return { ...finding, evidence: finding.evidence ?? {} };
}

function normalizeSecurityPolicy(policy: SecurityPolicy): SecurityPolicy {
  return {
    ...policy,
    allowed_capabilities: policy.allowed_capabilities ?? [],
    allowed_published_ports: policy.allowed_published_ports ?? [],
    allowed_executable_paths: policy.allowed_executable_paths ?? [],
    allowed_writable_paths: policy.allowed_writable_paths ?? [],
  };
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

export interface ManagedUser {
  id: number;
  username: string;
  role: string;
  disabled: boolean;
  created_at: string;
}

interface UsersResponse {
  users: ManagedUser[];
}

export async function fetchUsers(): Promise<ManagedUser[]> {
  const body = await request<UsersResponse>('/api/users');

  return body.users ?? [];
}

export async function createUser(input: {
  username: string;
  password: string;
  role: string;
}): Promise<ManagedUser> {
  return request<ManagedUser>('/api/users', jsonBody(input));
}

export async function setUserRole(id: number, role: string): Promise<ManagedUser> {
  return request<ManagedUser>(`/api/users/${id}/role`, jsonBody({ role }));
}

export async function resetUserPassword(id: number, password: string): Promise<void> {
  await request<undefined>(`/api/users/${id}/password`, jsonBody({ password }));
}

export async function disableUser(id: number): Promise<ManagedUser> {
  return request<ManagedUser>(`/api/users/${id}/disable`, { method: 'POST' });
}

export async function enableUser(id: number): Promise<ManagedUser> {
  return request<ManagedUser>(`/api/users/${id}/enable`, { method: 'POST' });
}

export async function deleteUser(id: number): Promise<void> {
  await request<undefined>(`/api/users/${id}`, { method: 'DELETE' });
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
  id: string;
  name: string;
  project: string;
  managed: boolean;
  service_id?: string | null;
  state: string;
  status?: string;
  classification: 'managed' | 'approved_observed' | 'unknown' | 'ignored' | 'missing_expected';
  current: boolean;
  present: boolean;
  health: string;
  cpu_percent: number | null;
  mem_bytes: number | null;
  mem_limit: number | null;
  restarts: number | null;
  sampled_at?: string;
  first_seen_at?: string;
  last_seen_at?: string;
  absent_at?: string | null;
  risk_counts: { critical: number; high: number; medium: number; low: number };
}

export interface ContainerFreshness {
  reconciled_at: string | null;
  complete: boolean;
  stale: boolean;
  last_error_category?: string;
  observed_count: number;
}

export interface FleetContainersResponse {
  containers: FleetContainer[];
  freshness: ContainerFreshness;
}

export async function fetchFleetContainers(): Promise<FleetContainersResponse> {
  const body = await request<FleetContainersResponse>('/api/system/containers');

  return {
    containers: body.containers ?? [],
    freshness: body.freshness,
  };
}

export interface SafeContainerInspection {
  user: string;
  entrypoint_hash?: string;
  command_hash?: string;
  privileged: boolean;
  readonly_root_fs: boolean;
  cap_add: string[];
  pid_mode?: string;
  ipc_mode?: string;
  network_mode?: string;
  restart_policy?: string;
  memory_bytes?: number;
  nano_cpus?: number;
  pids_limit?: number | null;
  mounts: { type: string; destination: string; writable: boolean }[];
  has_docker_socket: boolean;
}

export interface ContainerDetailResponse {
  container: FleetContainer;
  image: string;
  image_id: string;
  inspection: SafeContainerInspection;
}

export async function fetchContainer(id: string): Promise<ContainerDetailResponse> {
  return request<ContainerDetailResponse>(`/api/system/containers/${encodeURIComponent(id)}`);
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
