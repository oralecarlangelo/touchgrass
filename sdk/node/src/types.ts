// Public SDK types. Wire shapes match POST /api/ingest.

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

export interface BreadcrumbInput {
  category?: string;
  message?: string;
}

export interface ErrorReport {
  type: 'exception' | 'message';
  message: string;
  stack: StackFrame[];
  breadcrumbs: Breadcrumb[];
  release: string;
}

export type ScrubPattern = RegExp | string;

export interface InitOptions {
  /** Base URL of the touchgrass server, e.g. https://tg.internal. */
  endpoint: string;
  /** Per-service ingestion key (tg_...). */
  key: string;
  /** Release tag attached to every report. Defaults to TOUCHGRASS_RELEASE. */
  release?: string;
  /** PII patterns redacted client-side before send. */
  scrub?: ScrubPattern[];
  /** Max queued reports; oldest drop past the cap. Defaults to 100. */
  maxQueue?: number;
  /** Install uncaughtException/unhandledRejection capture. Defaults to true. */
  captureUncaught?: boolean;
}
