import { describe, expect, it } from 'vitest';

import { LatestRequest } from './latest-request.ts';

describe('LatestRequest', () => {
  it('shares one in-flight read between simultaneous consumers', async () => {
    const requests = new LatestRequest<number>();
    let calls = 0;
    let resolve!: (value: number) => void;
    const read = () => {
      calls += 1;
      return new Promise<number>((done) => { resolve = done; });
    };

    const first = requests.run(read);
    const second = requests.run(read);
    resolve(7);

    await expect(first).resolves.toBe(7);
    await expect(second).resolves.toBe(7);
    expect(calls).toBe(1);
  });

  it('aborts the active read on cleanup', () => {
    const requests = new LatestRequest<number>();
    let signal: AbortSignal | undefined;

    void requests.run((nextSignal) => {
      signal = nextSignal;
      return new Promise<number>(() => undefined);
    });
    requests.cancel();

    expect(signal?.aborted).toBe(true);
  });
});
