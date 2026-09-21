import type { StackFrame } from './types.js';

const maxFrames = 100;

// Matches V8 frames: "at fn (file:line:col)", "at async fn (file:line:col)",
// "at file:line:col". The file part is greedy so URLs (file:///...,
// node:internal/...) and drive-letter paths survive.
const framePattern = /^\s*at\s+(?:async\s+)?(?:(.+?)\s+\()?\(?(.+):(\d+):(\d+)\)?$/;

function parseLine(line: string): StackFrame | null {
  const match = framePattern.exec(line);

  if (match === null) {
    return null;
  }

  return {
    function: match[1] ?? '<anonymous>',
    file: match[2] ?? '',
    line: Number.parseInt(match[3] ?? '0', 10),
    column: Number.parseInt(match[4] ?? '0', 10),
  };
}

/**
 * parseStack converts a V8 stack string into frames, innermost first.
 * Never throws: unparseable input yields an empty list.
 */
export function parseStack(stack: unknown): StackFrame[] {
  try {
    if (typeof stack !== 'string' || stack === '') {
      return [];
    }

    const frames: StackFrame[] = [];

    for (const line of stack.split('\n')) {
      const frame = parseLine(line);

      if (frame !== null) {
        frames.push(frame);
      }

      if (frames.length >= maxFrames) {
        break;
      }
    }

    return frames;
  } catch {
    return [];
  }
}
