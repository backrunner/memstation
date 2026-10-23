<script lang="ts">
  import Button from '#lib/components/Button.svelte';
  import SelectField from '#lib/components/SelectField.svelte';
  import '#lib/styles/app.css';

  import { onMount } from 'svelte';
  import type { Snippet } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/state';
  import { Dialog } from 'bits-ui';
  import {
    Activity,
    BarChart3,
    Blocks,
    Boxes,
    Brain,
    ChevronRight,
    ClipboardList,
    Database,
    LayoutDashboard,
    LogOut,
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
    Workflow,
    X
  } from '@lucide/svelte';

  import TargetManagerDialog from '#lib/components/TargetManagerDialog.svelte';
  import { fetchAdminSession, fetchHealth, loginAdmin, logoutAdmin } from '#lib/api/client.ts';
  import BrandMark from '#lib/components/BrandMark.svelte';
  import { forgetTargetCredential, rememberTargetCredential } from '#lib/targets/credentials.ts';
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
  } from '#lib/stores/targets.ts';

  type NavItem = {
    href: string;
    label: string;
    icon: typeof LayoutDashboard;
  };

  interface Props {
    children?: Snippet | undefined;
  }

  let { children = undefined }: Props = $props();

  type NavSection = {
    label: string;
    items: NavItem[];
  };

  const navSections: NavSection[] = [
    {
      label: 'Workspace',
      items: [
        { href: '/dashboard', label: 'Dashboard', icon: LayoutDashboard },
        { href: '/conversations', label: 'Conversations', icon: MessageSquareText },
        { href: '/memory', label: 'Memory', icon: Brain },
        { href: '/users', label: 'Users', icon: Users }
      ]
    },
    {
      label: 'Test',
      items: [
        { href: '/playground/chat', label: 'Chat', icon: Sparkles },
        { href: '/playground/memory', label: 'Recall', icon: Search },
        { href: '/playground/protocols', label: 'Protocols', icon: Terminal }
      ]
    },
    {
      label: 'Operations',
      items: [
        { href: '/operations/health', label: 'Health', icon: Activity },
        { href: '/operations/queues', label: 'Queues', icon: Workflow },
        { href: '/operations/tasks', label: 'Tasks', icon: ClipboardList },
        { href: '/operations/cache', label: 'Cache', icon: Database },
        { href: '/statistics/tokens', label: 'Usage', icon: BarChart3 },
        { href: '/audit', label: 'Audit', icon: ShieldCheck }
      ]
    },
    {
      label: 'Settings',
      items: [
        { href: '/settings/system', label: 'System', icon: Settings },
        { href: '/settings/models', label: 'Models', icon: Boxes },
        { href: '/settings/providers', label: 'Providers', icon: PlugZap }
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

  let targetDialogOpen = $state(false);
  let mobileSidebarOpen = $state(false);
  let mobileNavTrigger: HTMLButtonElement | null = $state(null);
  let mobileViewport = $state(false);
  let lastTargetId = $state('');
  let mounted = $state(false);
  let targetVersions: Record<string, string> = $state({});


  onMount(() => {
    const viewport = window.visualViewport;
    const syncDialogViewport = () => {
      document.documentElement.style.setProperty('--visual-viewport-height', `${viewport?.height ?? window.innerHeight}px`);
      document.documentElement.style.setProperty('--visual-viewport-top', `${viewport?.offsetTop ?? 0}px`);
    };
    syncDialogViewport();
    viewport?.addEventListener('resize', syncDialogViewport);
    viewport?.addEventListener('scroll', syncDialogViewport);
    const media = window.matchMedia('(max-width: 1024px)');
    const syncViewport = () => {
      mobileViewport = media.matches;
      if (!mobileViewport) mobileSidebarOpen = false;
    };
    syncViewport();
    media.addEventListener('change', syncViewport);
    mounted = true;
    initializeTargets();
    void refreshSelectedTarget();
    return () => {
      media.removeEventListener('change', syncViewport);
      viewport?.removeEventListener('resize', syncDialogViewport);
      viewport?.removeEventListener('scroll', syncDialogViewport);
      document.documentElement.style.removeProperty('--visual-viewport-height');
      document.documentElement.style.removeProperty('--visual-viewport-top');
    };
  });



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
      const sectionDestinations: Record<string, string> = {
        '/operations': '/operations/health',
        '/settings': '/settings/system',
        '/statistics': '/statistics/tokens',
        '/playground': '/playground/chat'
      };
      const href = segments[0] === 'operations' && segments[1] === 'tasks' && index === 2
        ? `/operations/queues/${segment}`
        : sectionDestinations[accumulated] ?? accumulated;
      return { label, href, isLast };
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
      rememberTargetCredential(savedTarget.id, event.detail.adminKey);
      void connectSavedTarget(savedTarget, event.detail.adminKey);
    } else {
      void refreshSelectedTarget(savedTarget);
    }
  }

  async function connectSavedTarget(savedTarget: ReturnType<typeof upsertTarget>, adminKey: string): Promise<void> {
    try {
      const payload = await loginAdmin(savedTarget, adminKey);
      rememberTargetCredential(savedTarget.id, adminKey);
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

  function sessionStatusLabel(): string {
    if ($targetState.session.checking) return 'Checking target…';
    if ($targetState.session.error) return $targetState.session.error;
    if ($targetState.session.authenticated) return 'Session active';
    return 'Session required';
  }

  let targetItems = $derived($targetState.targets.map((target) => ({
    value: target.id,
    label: target.label,
    endpoint: formatTargetEndpoint(target.baseUrl),
    serverVersion: formatTargetVersion(targetVersions[target.id]),
    hasServerVersion: Boolean(targetVersions[target.id]?.trim())
  })));
  let isAuthRoute = $derived(page.url.pathname === '/login');
  let needsInitialConnect = $derived(mounted && $targetState.targets.length === 0);
  let breadcrumbs = $derived(buildBreadcrumbs(page.url.pathname));
  $effect(() => {
    if (mounted && $selectedTarget?.id && $selectedTarget.id !== lastTargetId) {
      lastTargetId = $selectedTarget.id;
      void refreshSelectedTarget();
    }
  });
  $effect(() => {
    if (needsInitialConnect && !isAuthRoute) {
      void goto('/login');
    }
  });
</script>

<svelte:head>
  <title>MemStation Manager</title>
  <meta name="color-scheme" content="light" />
</svelte:head>

{#snippet sidebarNavigation(mobile = false)}
      <div class="sidebar-brand">
        <BrandMark />
        <span class="sidebar-brand-name">MemStation</span>
        {#if mobile}
          <Dialog.Close class="btn-ghost btn-icon sidebar-close" aria-label="Close navigation"><X size={16} /></Dialog.Close>
        {/if}
      </div>

      <nav class="sidebar-nav" aria-label="Primary navigation">
        {#each navSections as section}
          <div class="sidebar-section">
            <span class="sidebar-section-label">{section.label}</span>
            <div class="sidebar-nav-list">
              {#each section.items as item}
                <a
                  class="sidebar-nav-link"
                  tabindex="0"
                  class:is-active={isActive(page.url.pathname, item.href)}
                  aria-current={isActive(page.url.pathname, item.href) ? 'page' : undefined}
                  href={item.href}
                  onclick={closeMobileSidebar}
                >
                  <span class="sidebar-nav-icon">
                    <item.icon size={15} strokeWidth={1.75} />
                  </span>
                  <span class="sidebar-nav-text">{item.label}</span>
                </a>
              {/each}
            </div>
          </div>
        {/each}
      </nav>

{/snippet}

{#if isAuthRoute || needsInitialConnect}
  <div class="app-shell is-auth">
    {@render children?.()}
  </div>
{:else}
  <Dialog.Root bind:open={mobileSidebarOpen}>
  <div class="app-shell">
    {#if !mobileViewport}
    <aside class="app-sidebar" aria-label="Primary navigation">
      {@render sidebarNavigation()}
    </aside>
    {/if}

    <main class="app-main">
      <header class="app-topbar">
        <div class="mobile-topbar-brand">
          <Dialog.Trigger bind:ref={mobileNavTrigger} class="btn-ghost btn-icon mobile-nav-trigger" aria-label="Open navigation">
            <Menu size={17} strokeWidth={1.8} />
          </Dialog.Trigger>
          <BrandMark size={28} />
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
            <SelectField label="Switch target" hideLabel value={$targetState.selectedId} items={targetItems}
              onValueChange={(value) => selectTarget(value)} title={sessionStatusLabel()}
              triggerClass="target-selector-trigger" contentClass="target-selector-content" itemClass="target-selector-item" align="end">
              {#snippet selection(item)}<span class="target-selector-label">{item?.endpoint ?? 'Select target'}</span>{/snippet}
              {#snippet option(item)}
                <span class="target-item-endpoint">{item.endpoint}</span>
                <span class="target-item-version" class:is-muted={!item.hasServerVersion}>{item.serverVersion}</span>
              {/snippet}
            </SelectField>
          </div>

          <Button
            class="btn-ghost btn-icon topbar-refresh"
            type="button"
            onclick={refreshSelectedSession}
            title="Refresh target connection"
            aria-label="Refresh"
          >
            <RefreshCw size={15} strokeWidth={1.75} />
          </Button>

          <Button
            class="btn-ghost btn-icon topbar-targets"
            type="button"
            onclick={() => (targetDialogOpen = true)}
            title="Manage targets"
            aria-label="Manage targets"
          >
            <Blocks size={16} strokeWidth={1.75} />
          </Button>

          <Button
            class="btn-ghost btn-icon"
            type="button"
            onclick={resetConnections}
            title="Disconnect targets"
            aria-label="Disconnect targets"
          >
            <LogOut size={16} strokeWidth={1.75} />
          </Button>
        </div>
      </header>
      {#if $targetState.session.checking || $targetState.session.error || !$targetState.session.authenticated}
        <p class="connection-notice" class:is-error={Boolean($targetState.session.error)} role="status">
          {sessionStatusLabel()}
        </p>
      {/if}

      <div class="app-content">
        {@render children?.()}
      </div>
    </main>
  </div>
  {#if mobileViewport}
    <Dialog.Portal>
      <Dialog.Overlay class="dialog-overlay" />
      <Dialog.Content
        class="app-sidebar mobile-sidebar"
        onCloseAutoFocus={(event) => {
          // Touch opening in WebKit does not focus the trigger automatically.
          event.preventDefault();
          mobileNavTrigger?.focus({ preventScroll: true });
        }}
      >
        <Dialog.Title class="sr-only">Navigation</Dialog.Title>
        {@render sidebarNavigation(true)}
      </Dialog.Content>
    </Dialog.Portal>
  {/if}
  </Dialog.Root>
{/if}

<TargetManagerDialog
  bind:open={targetDialogOpen}
  targets={$targetState.targets}
  selectedId={$targetState.selectedId}
  on:save={handleTargetSave}
  on:remove={(event) => {
    forgetTargetCredential(event.detail.id);
    removeTarget(event.detail.id);
  }}
/>
