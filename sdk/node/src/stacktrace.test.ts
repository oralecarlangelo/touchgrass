import { describe, expect, it } from 'vitest';
import { parseStack } from './stacktrace.js';

describe('parseStack', () => {
  it('parses V8 frames innermost first', () => {
    const stack = [
      'Error: boom',
      '    at handler (/app/app.js:10:3)',
      '    at async outer (/app/main.js:5:1)',
      '    at /app/top.js:1:1',
    ].join('\n');

    expect(parseStack(stack)).toEqual([
      { function: 'handler', file: '/app/app.js', line: 10, column: 3 },
      { function: 'outer', file: '/app/main.js', line: 5, column: 1 },
      { function: '<anonymous>', file: '/app/top.js', line: 1, column: 1 },
    ]);
  });

  it('skips unparseable lines', () => {
    const stack = ['Error: boom', 'garbage line', '    at ok (/a.js:1:2)'].join('\n');

    expect(parseStack(stack)).toEqual([
      { function: 'ok', file: '/a.js', line: 1, column: 2 },
    ]);
  });

  it('keeps file URLs and node internals intact', () => {
    const stack = [
      'Error: boom',
      '    at file:///tmp/gen.mjs:28:9',
      '    at run (node:internal/process:10:5)',
      '    at Array.forEach (<anonymous>)',
    ].join('\n');

    expect(parseStack(stack)).toEqual([
      { function: '<anonymous>', file: 'file:///tmp/gen.mjs', line: 28, column: 9 },
      { function: 'run', file: 'node:internal/process', line: 10, column: 5 },
    ]);
  });

  it('returns empty for non-strings and empties', () => {
    expect(parseStack(undefined)).toEqual([]);
    expect(parseStack(null)).toEqual([]);
    expect(parseStack('')).toEqual([]);
    expect(parseStack(42)).toEqual([]);
  });

  it('caps frames at one hundred', () => {
    const lines = ['Error: deep'];

    for (let i = 0; i < 150; i += 1) {
      lines.push(`    at frame${i} (/a.js:${i + 1}:1)`);
    }

    const frames = parseStack(lines.join('\n'));

    expect(frames).toHaveLength(100);
    expect(frames[0]?.function).toBe('frame0');
  });
});
