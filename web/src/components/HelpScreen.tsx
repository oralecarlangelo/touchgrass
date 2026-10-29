import { useMemo, useState, type ComponentType } from 'react';
import {
  Activity,
  ArrowUpRight,
  BellRing,
  BookOpen,
  DatabaseBackup,
  Logs,
  Radar,
  Search,
  ShieldAlert,
} from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import type { ViewId } from '@/lib/views.ts';

interface HelpFaq {
  category: string;
  question: string;
  answer: string;
}

interface Playbook {
  title: string;
  description: string;
  action: string;
  view: ViewId;
  icon: ComponentType<{ className?: string; 'aria-hidden'?: boolean }>;
  adminOnly?: boolean;
}

const operatorLoop = [
  ['Check coverage', 'Confirm that Fleet and collectors are current.'],
  ['Find the signal', 'Start with notifications, issues, or security findings.'],
  ['Correlate evidence', 'Compare metrics, logs, releases, and container state.'],
  ['Take action', 'Use the service runbook; Touchgrass preserves the evidence.'],
  ['Verify recovery', 'Confirm health, freshness, and alerts before closing.'],
] as const;

const playbooks: Playbook[] = [
  {
    title: 'Run the daily health check',
    description: 'Review service health, container coverage, host resources, and unread notifications.',
    action: 'Open dashboard',
    view: 'dashboard',
    icon: Radar,
  },
  {
    title: 'Investigate slow or failing traffic',
    description: 'Start with latency and error rate, then correlate the time window with logs and issues.',
    action: 'Open performance',
    view: 'performance',
    icon: Activity,
  },
  {
    title: 'Investigate a suspicious container',
    description: 'Check coverage first, then review the finding, process snapshot, writable-layer changes, and image.',
    action: 'Open security',
    view: 'security',
    icon: ShieldAlert,
  },
  {
    title: 'Trace an application error',
    description: 'Search by service, release, level, or trace ID and compare the result with grouped issues.',
    action: 'Search logs',
    view: 'logs',
    icon: Logs,
  },
  {
    title: 'Configure alert rules',
    description: 'Create or tune thresholds after you understand the normal baseline for each service.',
    action: 'Open rules',
    view: 'rules',
    icon: BellRing,
    adminOnly: true,
  },
  {
    title: 'Protect database recovery',
    description: 'Review backup freshness and restore readiness before an incident makes the decision urgent.',
    action: 'Open databases',
    view: 'databases',
    icon: DatabaseBackup,
    adminOnly: true,
  },
];

const helpFaqs: HelpFaq[] = [
  {
    category: 'Collection',
    question: 'Why does Touchgrass say collector coverage is stale or partial?',
    answer: 'At least one Docker collector could not complete its last pass. Open Performance and check Collector status. Fix Docker socket access or the reported collector error, then wait for a complete pass. Touchgrass keeps earlier findings open while coverage is incomplete so missing evidence cannot create a false resolution.',
  },
  {
    category: 'Security',
    question: 'Does Touchgrass automatically stop a suspicious container?',
    answer: 'No. Touchgrass detects, records, and correlates evidence; containment remains an operator decision. If compromise is plausible, follow the service incident procedure: isolate traffic, rotate credentials, preserve evidence, and rebuild from a trusted image.',
  },
  {
    category: 'Security',
    question: 'Why is a security finding still visible after I removed the file or process?',
    answer: 'A finding is a durable incident episode. A later complete scan resolves the active episode, but its history remains available. Check the last-seen time and collector freshness before deciding whether the condition is still active.',
  },
  {
    category: 'Access',
    question: 'What can a read-only user do?',
    answer: 'Read-only users can inspect services, fleet state, metrics, logs, issues, notifications, security evidence, and audit history. They cannot change policies, infrastructure, users, keys, or finding workflow state.',
  },
  {
    category: 'Fleet',
    question: 'Why is a container missing from Fleet?',
    answer: 'Fleet reconciles Docker lifecycle states every 30 seconds. Check Fleet freshness, Docker socket permissions, and whether the container existed long enough to be observed. An incomplete reconciliation preserves the last complete inventory rather than marking containers absent.',
  },
  {
    category: 'Alerts',
    question: 'Where should I start when notifications are noisy?',
    answer: 'Identify the rule producing the noise, compare it with the service baseline in Performance, and narrow the threshold or service scope. Avoid silencing a symptom before checking whether collector freshness or a recent release explains the spike.',
  },
  {
    category: 'Deployments',
    question: 'How do I validate a deployment?',
    answer: 'Open the service, confirm the deployment completed, then compare health, error rate, latency, logs, and new issues for the release. A successful command is not enough; verify the live service before treating the deployment as complete.',
  },
  {
    category: 'Data',
    question: 'How long does Touchgrass keep logs, metrics, and occurrences?',
    answer: 'Retention is bounded by the server configuration and store-specific caps. Older data is trimmed automatically. Use the full documentation for retention settings and inspect log statistics when investigating dropped or truncated data.',
  },
  {
    category: 'Troubleshooting',
    question: 'Why are application errors or SDK logs missing?',
    answer: 'Confirm the endpoint and ingestion key, check the key sample rate, and flush short-lived processes before exit. Application logs appear under the Application source in Logs, not the container source.',
  },
];

export function filterHelpFaqs(query: string): HelpFaq[] {
  const needle = query.trim().toLocaleLowerCase();
  if (needle === '') return helpFaqs;

  return helpFaqs.filter((faq) =>
    [faq.category, faq.question, faq.answer].some((value) =>
      value.toLocaleLowerCase().includes(needle),
    ),
  );
}

export default function HelpScreen({
  isAdmin,
  onView,
}: {
  isAdmin: boolean;
  onView: (view: ViewId) => void;
}) {
  const [query, setQuery] = useState('');
  const visibleFaqs = useMemo(() => filterHelpFaqs(query), [query]);
  const visiblePlaybooks = playbooks.filter((playbook) => !playbook.adminOnly || isAdmin);

  return (
    <div className="space-y-10 pb-10">
      <header className="flex flex-col gap-4 border-b pb-6 sm:flex-row sm:items-end sm:justify-between">
        <div className="max-w-2xl space-y-2">
          <h1 className="text-2xl font-semibold tracking-tight">Use Touchgrass with confidence</h1>
          <p className="text-muted-foreground text-sm leading-6">
            Follow the operator loop, choose the task in front of you, and keep evidence connected from detection through recovery.
          </p>
        </div>
        <Button variant="outline" asChild>
          <a href="/docs/" target="_blank" rel="noreferrer">
            <BookOpen aria-hidden />
            Full documentation
            <ArrowUpRight aria-hidden />
          </a>
        </Button>
      </header>

      <section aria-labelledby="operator-loop-title" className="space-y-3">
        <div>
          <h2 id="operator-loop-title" className="text-lg font-semibold">The operator loop</h2>
          <p className="text-muted-foreground text-sm">Use this sequence for routine checks and active incidents.</p>
        </div>
        <ol className="border-border grid overflow-hidden rounded-xl border sm:grid-cols-5">
          {operatorLoop.map(([title, description], index) => (
            <li key={title} className="border-border relative border-b p-4 last:border-b-0 sm:border-r sm:border-b-0 sm:last:border-r-0">
              <span className="bg-foreground text-background mb-3 flex h-6 w-6 items-center justify-center rounded-full text-xs font-semibold" aria-hidden>
                {index + 1}
              </span>
              <h3 className="text-sm font-medium">{title}</h3>
              <p className="text-muted-foreground mt-1 text-xs leading-5">{description}</p>
            </li>
          ))}
        </ol>
      </section>

      <section aria-labelledby="playbooks-title" className="space-y-3">
        <div>
          <h2 id="playbooks-title" className="text-lg font-semibold">What are you trying to do?</h2>
          <p className="text-muted-foreground text-sm">
            Start with the closest task. Each path opens the evidence you need first.
          </p>
        </div>
        {!isAdmin && (
          <p className="border-border bg-muted/40 rounded-lg border px-4 py-3 text-sm">
            You have read-only access. Investigate and share evidence here. Ask an admin to change policy or infrastructure.
          </p>
        )}
        <div className="grid gap-px overflow-hidden rounded-xl border bg-border md:grid-cols-2">
          {visiblePlaybooks.map((playbook) => {
            const Icon = playbook.icon;
            return (
              <article key={playbook.title} className="bg-background flex min-h-44 flex-col p-5">
                <Icon className="text-muted-foreground mb-5 h-5 w-5" aria-hidden />
                <h3 className="font-medium">{playbook.title}</h3>
                <p className="text-muted-foreground mt-1 max-w-prose text-sm leading-6">{playbook.description}</p>
                <Button className="mt-auto self-start px-0" variant="link" onClick={() => onView(playbook.view)}>
                  {playbook.action}
                </Button>
              </article>
            );
          })}
        </div>
      </section>

      <section aria-labelledby="faq-title" className="space-y-4">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
          <div>
            <h2 id="faq-title" className="text-lg font-semibold">Frequently asked questions</h2>
            <p className="text-muted-foreground text-sm">Search by task, symptom, or part of the answer.</p>
          </div>
          <div className="relative w-full sm:max-w-xs">
            <label htmlFor="help-search" className="sr-only">Search frequently asked questions</label>
            <Search className="text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 h-4 w-4 -translate-y-1/2" aria-hidden />
            <Input
              id="help-search"
              type="search"
              className="pl-8"
              placeholder="Search help"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
            />
          </div>
        </div>

        <div className="divide-border border-border divide-y border-y">
          {visibleFaqs.map((faq) => (
            <details key={faq.question} className="group py-4">
              <summary className="flex cursor-pointer list-none items-start justify-between gap-4 text-sm font-medium focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring [&::-webkit-details-marker]:hidden">
                <span>{faq.question}</span>
                <span className="text-muted-foreground shrink-0 text-xs font-normal">{faq.category}</span>
              </summary>
              <p className="text-muted-foreground mt-3 max-w-3xl pr-16 text-sm leading-6">{faq.answer}</p>
            </details>
          ))}
          {visibleFaqs.length === 0 && (
            <div className="py-8 text-center">
              <p className="text-sm font-medium">No matching answers</p>
              <p className="text-muted-foreground mt-1 text-sm">Try a service name, symptom, or broader term.</p>
            </div>
          )}
        </div>
      </section>
    </div>
  );
}
