import type { ReactNode } from 'react';
import { BookOpen, ChevronDown, CircleHelp, Menu } from 'lucide-react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { Separator } from '@/components/ui/separator';
import { TooltipProvider } from '@/components/ui/tooltip';
import type { ServiceView } from '@/lib/api.ts';
import { navGroups, type ViewId } from '@/lib/views.ts';
import NotificationBell from './NotificationBell.tsx';
import ServiceSwitcher from './ServiceSwitcher.tsx';
import ThemeToggle from './ThemeToggle.tsx';
import UserMenu from './UserMenu.tsx';

export default function AppShell({
  view,
  onView,
  services,
  selectedId,
  onSelectService,
  onLogout,
  onUnauthorized,
  username,
  isAdmin,
  children,
}: {
  view: ViewId;
  onView: (view: ViewId) => void;
  services: ServiceView[];
  selectedId: string | null;
  onSelectService: (id: string | null) => void;
  onLogout: () => void;
  onUnauthorized: () => void;
  username: string;
  isAdmin: boolean;
  children: ReactNode;
}) {
  const visibleGroups = navGroups
    .map((group) => ({
      ...group,
      items: group.items.filter((tab) => tab.id !== 'users' || isAdmin),
    }))
    .filter((group) => group.items.length > 0);

  const activeGroup = (groupId: string): boolean =>
    visibleGroups.some((group) => group.id === groupId && group.items.some((tab) => tab.id === view));

  return (
    <TooltipProvider>
      <div className="bg-background min-h-screen">
        <header className="border-border bg-card sticky top-0 z-40 border-b">
          <div className="mx-auto flex h-14 max-w-6xl items-center gap-2 px-4">
            <span className="text-sm font-semibold tracking-tight">touchgrass</span>
            {!isAdmin && (
              <Badge variant="secondary" title="Your account is read-only">
                read-only
              </Badge>
            )}
            <Separator orientation="vertical" className="mx-1 hidden h-5 md:block" />

            <div className="hidden md:block">
              <ServiceSwitcher services={services} selectedId={selectedId} onSelect={onSelectService} />
            </div>

            <nav aria-label="Primary" className="ml-2 hidden items-center gap-1 lg:flex">
              {visibleGroups.map((group) => (
                <DropdownMenu key={group.id}>
                  <DropdownMenuTrigger asChild>
                    <Button
                      variant={activeGroup(group.id) ? 'secondary' : 'ghost'}
                      size="sm"
                      aria-current={activeGroup(group.id) ? 'page' : undefined}
                    >
                      {group.label}
                      <ChevronDown className="h-3.5 w-3.5 opacity-50" aria-hidden />
                    </Button>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align="start">
                    {group.items.map((tab) => (
                      <DropdownMenuItem
                        key={tab.id}
                        aria-current={view === tab.id ? 'page' : undefined}
                        onSelect={() => onView(tab.id)}
                      >
                        {tab.label}
                      </DropdownMenuItem>
                    ))}
                  </DropdownMenuContent>
                </DropdownMenu>
              ))}
            </nav>

            <div className="ml-auto flex items-center gap-1">
              <Button
                variant={view === 'help' ? 'secondary' : 'ghost'}
                size="icon"
                aria-label="Help and how-to guides"
                title="Help and how-to guides"
                onClick={() => onView('help')}
              >
                <CircleHelp className="h-4 w-4" aria-hidden />
              </Button>
              <NotificationBell
                onViewAll={() => onView('notifications')}
                onUnauthorized={onUnauthorized}
                isAdmin={isAdmin}
              />
              <ThemeToggle />
              <UserMenu username={username} isAdmin={isAdmin} onLogout={onLogout} />
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button variant="ghost" size="icon" aria-label="Menu" className="lg:hidden">
                    <Menu className="h-4 w-4" aria-hidden />
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" className="w-48">
                  {visibleGroups.map((group, index) => (
                    <DropdownMenuGroup key={group.id}>
                      {index > 0 && <DropdownMenuSeparator />}
                      <DropdownMenuLabel>{group.label}</DropdownMenuLabel>
                      {group.items.map((tab) => (
                        <DropdownMenuItem key={tab.id} onSelect={() => onView(tab.id)}>
                          {tab.label}
                        </DropdownMenuItem>
                      ))}
                    </DropdownMenuGroup>
                  ))}
                  <DropdownMenuSeparator />
                  <DropdownMenuItem onSelect={() => onView('help')}>
                    <CircleHelp aria-hidden />
                    Help and how-to guides
                  </DropdownMenuItem>
                  <DropdownMenuItem asChild>
                    <a href="/docs/" target="_blank" rel="noreferrer">
                      <BookOpen aria-hidden />
                      Full documentation
                    </a>
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
            </div>
          </div>
        </header>

        <main className="mx-auto max-w-6xl space-y-4 px-4 py-6">{children}</main>
      </div>
    </TooltipProvider>
  );
}
