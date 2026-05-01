<svelte:options runes={false} />

<script lang="ts">
  import '$lib/styles/app.css';

  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { Select } from 'bits-ui';
  import {
    Activity,
    BarChart3,
    Blocks,
    Boxes,
    Brain,
    ChevronDown,
    ChevronRight,
    ClipboardList,
    HardDrive,
    LayoutDashboard,
    Menu,
    MessageSquareText,
    PlugZap,
    RefreshCw,
    Search,
    Settings,
    ShieldCheck,
    Sparkles,
    Terminal,
    Users,
    Workflow
  } from 'lucide-svelte';

  import TargetManagerDialog from '$lib/components/TargetManagerDialog.svelte';
  import { fetchAdminSession, fetchHealth, loginAdmin, logoutAdmin } from '$lib/api/client';
  import {
    clearAllTargets,
    initializeTargets,
    markTargetSessionConnected,
    removeTarget,
    selectTarget,
    selectedTarget,
    targetState,
    updateSession,
    upsertTarget
  } from '$lib/stores/targets';

  type NavItem = {
    href: string;
    label: string;
    icon: typeof LayoutDashboard;
  };

  type NavSection = {
    label: string;
    items: NavItem[];
  };

  // Nav structure aligns with frontend-panel.md §5.1 — nine modules grouped
  // by operator intent: observe → test → inspect → administer.
  const navSections: NavSection[] = [
    {
      label: 'Overview',
      items: [{ href: '/dashboard', label: 'Dashboard', icon: LayoutDashboard }]
    },
    {
      label: 'Playground',
      items: [
        { href: '/playground/chat', label: 'Chat', icon: Sparkles },
        { href: '/playground/memory', label: 'Recall', icon: Search },
        { href: '/playground/protocols', label: 'Protocols', icon: Terminal }
      ]
    },
    {
      label: 'Inspect',
      items: [
        { href: '/conversations', label: 'Conversations', icon: MessageSquareText },
        { href: '/memory', label: 'Memory', icon: Brain },
        { href: '/users', label: 'Users', icon: Users },
        { href: '/statistics/tokens', label: 'Statistics', icon: BarChart3 }
      ]
    },
    {
      label: 'Operate',
      items: [
        { href: '/operations/health', label: 'Health', icon: Activity },
        { href: '/operations/queues', label: 'Queues', icon: Workflow },
        { href: '/operations/tasks', label: 'Tasks', icon: ClipboardList },
        { href: '/operations/cache', label: 'Cache', icon: HardDrive }
      ]
    },
    {
      label: 'Administer',
      items: [
        { href: '/settings/system', label: 'System', icon: Settings },
        { href: '/settings/models', label: 'Models', icon: Boxes },
        { href: '/settings/providers', label: 'Providers', icon: PlugZap },
        { href: '/audit', label: 'Audit', icon: ShieldCheck }
      ]
    }
  ];

  // Breadcrumb map: pathname → [label, label, ...] for display in topbar.
  const routeLabels: Record<string, string> = {
    dashboard: 'Dashboard',
    playground: 'Playground',
    chat: 'Chat',
    memory: 'Memory',
    protocols: 'Protocols',
    conversations: 'Conversations',
    users: 'Users',
    statistics: 'Statistics',
    tokens: 'Tokens',
    settings: 'Settings',
    system: 'System',
    models: 'Models',
    providers: 'Providers',
    operations: 'Operations',
    health: 'Health',
    queues: 'Queues',
    tasks: 'Tasks',
    cache: 'Cache',
    audit: 'Audit',
    summaries: 'Summaries',
    impressions: 'Impressions',
    facts: 'Facts',
    keys: 'Key Memories',
    login: 'Connect'
  };

  let targetDialogOpen = false;
  let mobileSidebarOpen = false;
  let lastTargetId = '';
  let mounted = false;
  let targetVersions: Record<string, string> = {};

  $: targetItems = $targetState.targets.map((target) => ({
    value: target.id,
    label: target.label,
    endpoint: formatTargetEndpoint(target.baseUrl),
    serverVersion: formatTargetVersion(targetVersions[target.id]),
    hasServerVersion: Boolean(targetVersions[target.id]?.trim())
  }));
  $: activeTargetItem = targetItems.find((item) => item.value === $targetState.selectedId);
  $: isAuthRoute = $page.url.pathname === '/login';
  $: needsInitialConnect = mounted && $targetState.targets.length === 0;
  $: breadcrumbs = buildBreadcrumbs($page.url.pathname);

  onMount(async () => {
    mounted = true;
    initializeTargets();
    void refreshSelectedTarget();
  });

  $: if (mounted && $selectedTarget?.id && $selectedTarget.id !== lastTargetId) {
    lastTargetId = $selectedTarget.id;
    void refreshSelectedTarget();
  }

  $: if (needsInitialConnect && !isAuthRoute) {
    void goto('/login');
  }

  async function refreshSelectedTarget(target = $selectedTarget): Promise<void> {
    if (!target?.id || !target.baseUrl) {
      return;
    }
    updateSession({ authenticated: false, checking: true });
    const [session] = await Promise.all([fetchAdminSession(target), refreshTargetVersion(target)]);
    if ($targetState.selectedId !== target.id) {
      return;
    }
    updateSession(session);
  }

  async function resetConnections(): Promise<void> {
    const logoutTargets = $targetState.targets.filter((target) => target.baseUrl);
    await Promise.allSettled(logoutTargets.map((target) => logoutAdmin(target)));
    clearAllTargets();
    await goto('/login');
  }

  function isActive(pathname: string, href: string): boolean {
    return pathname === href || pathname.startsWith(`${href}/`);
  }

  function refreshSelectedSession(): void {
    void refreshSelectedTarget();
  }

  async function refreshTargetVersion(target = $selectedTarget): Promise<void> {
    if (!target) {
      return;
    }

    try {
      const health = await fetchHealth(target, '/health/live');
      targetVersions = {
        ...targetVersions,
        [target.id]: health.version?.trim() ?? ''
      };
    } catch {
      targetVersions = {
        ...targetVersions,
        [target.id]: ''
      };
    }
  }

  function closeMobileSidebar(): void {
    mobileSidebarOpen = false;
  }

  function handleWindowKeydown(event: KeyboardEvent): void {
    if (event.key === 'Escape') {
      closeMobileSidebar();
    }
  }

  function buildBreadcrumbs(pathname: string): Array<{ label: string; href: string; isLast: boolean }> {
    const segments = pathname.split('/').filter(Boolean);
    if (segments.length === 0) {
      return [{ label: 'Dashboard', href: '/dashboard', isLast: true }];
    }
    let accumulated = '';
    return segments.map((segment, index) => {
      accumulated += `/${segment}`;
      const isLast = index === segments.length - 1;
      const label = routeLabels[segment] ?? decodeSegment(segment);
      return { label, href: accumulated, isLast };
    });
  }

  function decodeSegment(value: string): string {
    try {
      const decoded = decodeURIComponent(value);
      if (decoded.length > 20) {
        return `${decoded.slice(0, 8)}…${decoded.slice(-6)}`;
      }
      return decoded;
    } catch {
      return value;
    }
  }

  function formatTargetEndpoint(baseUrl: string): string {
    const value = baseUrl.trim();
    if (!value) {
      return 'unconfigured';
    }

    try {
      const url = new URL(value);
      const path = url.pathname === '/' ? '' : url.pathname.replace(/\/$/, '');
      return `${url.host}${path}`;
    } catch {
      return value.replace(/^https?:\/\//, '').replace(/\/$/, '') || 'unconfigured';
    }
  }

  function formatTargetVersion(value: string | undefined): string {
    const version = value?.trim();
    if (!version) {
      return 'version unknown';
    }
    if (/^\d/.test(version)) {
      return `v${version}`;
    }
    return version;
  }

  function handleTargetSave(
    event: CustomEvent<{ target: Parameters<typeof upsertTarget>[0]; adminKey?: string }>
  ): void {
    const savedTarget = upsertTarget(event.detail.target);
    lastTargetId = savedTarget.id;
    if (event.detail.adminKey?.trim()) {
      void connectSavedTarget(savedTarget, event.detail.adminKey);
    } else {
      void refreshSelectedTarget(savedTarget);
    }
  }

  async function connectSavedTarget(savedTarget: ReturnType<typeof upsertTarget>, adminKey: string): Promise<void> {
    try {
      const payload = await loginAdmin(savedTarget, adminKey);
      markTargetSessionConnected(savedTarget.id, payload.checkedAt);
      targetVersions = {
        ...targetVersions,
        [savedTarget.id]: ''
      };
      void refreshTargetVersion(savedTarget);
      updateSession({ authenticated: true, checking: false, checkedAt: payload.checkedAt });
    } catch (error) {
      updateSession({
        authenticated: false,
        checking: false,
        checkedAt: new Date().toISOString(),
        error: error instanceof Error ? error.message : 'Connection failed'
      });
    }
  }

  function sessionStatusTone(): 'success' | 'warning' | 'danger' | 'muted' {
    if ($targetState.session.checking) return 'muted';
    if ($targetState.session.error) return 'danger';
    if ($targetState.session.authenticated) return 'success';
    return 'warning';
  }

  function sessionStatusLabel(): string {
    if ($targetState.session.checking) return 'Checking target…';
    if ($targetState.session.error) return $targetState.session.error;
    if ($targetState.session.authenticated) return 'Session active';
    return 'Session required';
  }

  function adminInitials(): string {
    if ($targetState.session.authenticated) {
      return 'OK';
    }
    return '—';
  }
</script>

<svelte:head>
  <title>MemStation Manager</title>
  <meta name="color-scheme" content="light" />
</svelte:head>

<svelte:window on:keydown={handleWindowKeydown} />

{#if isAuthRoute || needsInitialConnect}
  <div class="app-shell is-auth">
    <slot />
  </div>
{:else}
  <div class="app-shell" class:is-sidebar-open={mobileSidebarOpen}>
    <aside class="app-sidebar" aria-label="Primary navigation">
      <div class="sidebar-brand">
        <span class="sidebar-brand-mark">MS</span>
        <span class="sidebar-brand-name">MemStation</span>
      </div>

      <nav class="sidebar-nav" aria-label="Primary navigation">
        {#each navSections as section}
          <div class="sidebar-section">
            <span class="sidebar-section-label">{section.label}</span>
            <div class="sidebar-nav-list">
              {#each section.items as item}
                <a
                  class="sidebar-nav-link"
                  class:is-active={isActive($page.url.pathname, item.href)}
                  href={item.href}
                  on:click={closeMobileSidebar}
                >
                  <span class="sidebar-nav-icon">
                    <svelte:component this={item.icon} size={15} strokeWidth={1.75} />
                  </span>
                  <span class="sidebar-nav-text">{item.label}</span>
                </a>
              {/each}
            </div>
          </div>
        {/each}
      </nav>

    </aside>
    {#if mobileSidebarOpen}
      <button
        class="sidebar-scrim"
        type="button"
        aria-label="Close navigation"
        on:click={closeMobileSidebar}
      ></button>
    {/if}

    <main class="app-main">
      <header class="app-topbar">
        <div class="mobile-topbar-brand">
          <button
            class="btn-ghost btn-icon mobile-nav-trigger"
            type="button"
            aria-label="Open navigation"
            aria-expanded={mobileSidebarOpen}
            on:click={() => (mobileSidebarOpen = true)}
          >
            <Menu size={17} strokeWidth={1.8} />
          </button>
          <span class="sidebar-brand-mark">MS</span>
        </div>

        <nav class="topbar-breadcrumb" aria-label="Breadcrumb">
          {#each breadcrumbs as crumb, index}
            {#if index > 0}
              <span class="topbar-breadcrumb-sep" aria-hidden="true">
                <ChevronRight size={12} strokeWidth={2} />
              </span>
            {/if}
            {#if crumb.isLast}
              <span class="topbar-breadcrumb-item is-current">{crumb.label}</span>
            {:else}
              <a class="topbar-breadcrumb-item" href={crumb.href}>{crumb.label}</a>
            {/if}
          {/each}
        </nav>

        <div class="topbar-spacer"></div>

        <div class="topbar-actions">
          <div class="target-selector">
            <Select.Root
              type="single"
              value={$targetState.selectedId}
              items={targetItems}
              onValueChange={(value) => selectTarget(value)}
            >
              <Select.Trigger class="target-selector-trigger" aria-label="Switch target">
                {#if activeTargetItem}
                  <span class="target-selector-label">{activeTargetItem.endpoint}</span>
                {:else}
                  <span class="target-selector-label">Select target</span>
                {/if}
                <span class="target-selector-caret">
                  <ChevronDown size={14} strokeWidth={2} />
                </span>
              </Select.Trigger>
              <Select.Portal>
                <Select.Content class="select-content target-selector-content" sideOffset={6} align="end">
                  <Select.Viewport>
                    {#each targetItems as item}
                      <Select.Item
                        class="select-item target-selector-item"
                        value={item.value}
                        label={item.label}
                      >
                        <span class="target-item-endpoint">{item.endpoint}</span>
                        <span class="target-item-version" class:is-muted={!item.hasServerVersion}>{item.serverVersion}</span>
                      </Select.Item>
                    {/each}
                  </Select.Viewport>
                </Select.Content>
              </Select.Portal>
            </Select.Root>
          </div>

          <button
            class="btn-ghost btn-icon topbar-refresh"
            type="button"
            on:click={refreshSelectedSession}
            title="Refresh target connection"
            aria-label="Refresh"
          >
            <RefreshCw size={15} strokeWidth={1.75} />
          </button>

          <button
            class="btn-secondary btn-sm topbar-targets"
            type="button"
            on:click={() => (targetDialogOpen = true)}
          >
            <Blocks size={13} strokeWidth={1.75} />
            Targets
          </button>

          <span class="topbar-divider topbar-session-divider" aria-hidden="true"></span>

          <div class="connection-pill" title={sessionStatusLabel()}>
            <span class={`status-dot ${sessionStatusTone()}`}></span>
            <span class="session-menu-initials">{adminInitials()}</span>
          </div>

          <button class="btn-ghost btn-sm" type="button" on:click={resetConnections}>
            Reset
          </button>
        </div>
      </header>

      <div class="app-content">
        <slot />
      </div>
    </main>
  </div>
{/if}

<TargetManagerDialog
  bind:open={targetDialogOpen}
  targets={$targetState.targets}
  selectedId={$targetState.selectedId}
  on:save={handleTargetSave}
  on:remove={(event) => removeTarget(event.detail.id)}
/>
