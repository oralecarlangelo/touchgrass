// LatestRequest shares one active read and cancels it when its owner is done.
export class LatestRequest<T> {
  private controller: AbortController | null = null;
  private pending: Promise<T> | null = null;

  run(read: (signal: AbortSignal) => Promise<T>): Promise<T> {
    if (this.pending !== null) return this.pending;

    this.controller = new AbortController();
    const pending = read(this.controller.signal).finally(() => {
      if (this.pending === pending) {
        this.pending = null;
        this.controller = null;
      }
    });
    this.pending = pending;

    return pending;
  }

  cancel(): void {
    this.controller?.abort();
    this.controller = null;
    this.pending = null;
  }
}
