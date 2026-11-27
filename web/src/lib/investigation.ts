import type { Notification } from './api.ts';

export interface InvestigationRange {
  serviceId?: string;
  start?: string;
  end?: string;
}

type InvestigationView = 'logs' | 'issues' | 'audit' | 'databases' | 'fleet';
const halfHourMs = 30 * 60_000;

export function alertInvestigation(
  alert: Pick<Notification, 'service_id' | 'created_at'>,
): (InvestigationRange & { start: string; end: string }) | null {
  const at = Date.parse(alert.created_at);
  const start = new Date(at - halfHourMs);
  const end = new Date(at + halfHourMs);
  if (!Number.isFinite(start.getTime()) || !Number.isFinite(end.getTime())) return null;
  return {
    serviceId: alert.service_id || undefined,
    start: start.toISOString(),
    end: end.toISOString(),
  };
}

function timeBound(input: string | null | undefined): string | undefined {
  if (!input) return undefined;
  const parsed = new Date(input);
  return Number.isFinite(parsed.getTime()) ? parsed.toISOString() : undefined;
}

export function readInvestigation(hash: string): InvestigationRange {
  const params = new URLSearchParams(hash.split('?')[1]);
  const serviceId = params.get('service_id') || undefined;
  const start = timeBound(params.get('start'));
  const end = timeBound(params.get('end'));
  return { ...(serviceId && { serviceId }), ...(start && { start }), ...(end && { end }) };
}

export function investigationHref(view: InvestigationView, filters: InvestigationRange): string {
  const params = new URLSearchParams();
  const serviceId = filters.serviceId?.trim();
  if (serviceId) params.set('service_id', serviceId);

  const start = timeBound(filters.start);
  const end = timeBound(filters.end);
  if (start) params.set('start', start);
  if (end) params.set('end', end);

  const query = params.toString();
  return `#/${view}${query === '' ? '' : `?${query}`}`;
}

export function alertHref(id: number): string {
  return `#/notifications?alert=${id}`;
}
