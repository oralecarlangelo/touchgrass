import { ChevronRight } from 'lucide-react';

import type { EnvTarget } from '@/lib/api.ts';
import { envDocsHref } from '@/lib/env.ts';

// EnvHelp is the short "How this works" disclosure under the table.
export default function EnvHelp({ target }: { target: EnvTarget | null }) {
  return (
    <details className="group text-sm">
      <summary className="text-muted-foreground hover:text-foreground flex cursor-pointer list-none items-center gap-1 text-xs select-none">
        <ChevronRight className="size-3.5 transition-transform group-open:rotate-90" aria-hidden />
        How this works
      </summary>
      <div className="text-muted-foreground mt-2 space-y-2 pl-5 text-xs leading-relaxed">
        <p>
          Entries are stored encrypted in touchgrass and rendered into{' '}
          <span className="text-foreground font-mono">{target?.path ?? 'the configured file'}</span>
          {target !== null && (
            <>
              {' '}
              (owner <span className="font-mono">{target.owner}</span>, mode{' '}
              <span className="font-mono">{target.mode}</span>)
            </>
          )}{' '}
          on the host.
        </p>
        <p>
          Edits are staged here first. Apply renders the file, runs the validator, writes it
          atomically, restarts the service and probes its health. If validation or the probe fails,
          the previous file is restored and your changes stay staged.
        </p>
        <p>
          Secrets are masked for everyone. Admins can reveal one for 20 seconds or copy it without
          showing it; every reveal is audited and rate limited. Viewers see keys and plain values only.
        </p>
        <p>
          <a href={envDocsHref} target="_blank" rel="noreferrer" className="text-primary underline-offset-4 hover:underline">
            Read the environment guide
          </a>
        </p>
      </div>
    </details>
  );
}
