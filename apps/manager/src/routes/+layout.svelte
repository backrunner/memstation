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
    Bot,
    Brain,
    Braces,
    Boxes,
    ClipboardList,
    HardDrive,
    LayoutDashboard,
    LogIn,
    MessageSquareText,
    PlugZap,
    RefreshCw,
    Search,
    Settings2,
    ShieldCheck,
    Users,
    Workflow
  } from 'lucide-svelte';

  import TargetManagerDialog from '$lib/components/TargetManagerDialog.svelte';
  import { fetchAdminSession, loginAdmin, logoutAdmin } from '$lib/api/client';
  import {
    clearTargetAdminKey,
    initializeTargets,
    peekTargetAdminKey,
    removeTarget,
    selectTarget,
    selectedTarget,
    stageTargetAdminKey,
    targetState,
    updateSession,
    upsertTarget
  } from '$lib/stores/targets';

  const navSections = [
    {
      label: 'Operate',
      items: [
        { href: '/dashboard', label: 'Dashboard', icon: LayoutDashboard },
        { href: '/playground/chat', label: 'Playground Chat', icon: Bot },
        { href: '/playground/memory', label: 'Playground Memory', icon: Search },
        { href: '/playground/protocols', label: 'Playground Protocols', icon: Braces },
        { href: '/conversations', label: 'Conversations', icon: MessageSquareText },
        { href: '/memory', label: 'Memory Workspace', icon: Brain }
      ]
    },
    {
      label: 'Manage',
      items: [
        { href: '/users', label: 'Users', icon: Users },
        { href: '/statistics/tokens', label: 'Statistics', icon: BarChart3 },
        { href: '/settings/system', label: 'System', icon: Settings2 },
        { href: '/settings/models', label: 'Models', icon: Boxes },
        { href: '/settings/providers', label: 'Providers', icon: PlugZap }
      ]
    },
    {
      label: 'Operations',
      items: [
        { href: '/operations/health', label: 'Health', icon: Activity },
        { href: '/operations/queues', label: 'Queues', icon: Workflow },
        { href: '/operations/tasks', label: 'Tasks', icon: ClipboardList },
        { href: '/operations/cache', label: 'Cache', icon: HardDrive },
        { href: '/audit', label: 'Audit', icon: ShieldCheck }
      ]
    }
  ];

  let targetDialogOpen = false;
  let lastTargetId = '';
  let targetItems: Array<{ value: string; label: string }> = [];
  let mounted = false;

  $: targetItems = $targetState.targets.map((target) => ({
    value: target.id,
    label: `${target.label} · ${target.environment}`
  }));

  onMount(async () => {
    mounted = true;
    initializeTargets();
    await refreshSession();
  });

  $: if (mounted && $selectedTarget?.id && $selectedTarget.id !== lastTargetId) {
    lastTargetId = $selectedTarget.id;
    void refreshSession();
  }

  async function refreshSession(target = $selectedTarget): Promise<void> {
    if (!target) {
      return;
    }
    updateSession({ authenticated: false, checking: true });
    const pendingKey = peekTargetAdminKey(target.id);
    let loginError = '';

    if (pendingKey) {
      try {
        await loginAdmin(target, pendingKey);
      } catch (error) {
        loginError = error instanceof Error ? error.message : 'Auto sign-in failed';
      }
    }

    const session = await fetchAdminSession(target);
    if (loginError && !session.authenticated) {
      session.error = loginError;
    }
    updateSession(session);
  }

  async function handleLogout(): Promise<void> {
    if (!$selectedTarget) {
      return;
    }
    await logoutAdmin($selectedTarget);
    clearTargetAdminKey($selectedTarget.id);
    await refreshSession();
    await goto('/login');
  }

  function isActive(pathname: string, href: string): boolean {
    return pathname === href || pathname.startsWith(`${href}/`);
  }

  function refreshSelectedSession(): void {
    void refreshSession();
  }

  function handleTargetSave(event: CustomEvent<{ target: Parameters<typeof upsertTarget>[0]; adminKey?: string }>): void {
    const savedTarget = upsertTarget(event.detail.target);
    stageTargetAdminKey(savedTarget.id, event.detail.adminKey);
    lastTargetId = savedTarget.id;
    void refreshSession(savedTarget);
  }

  $: sessionTone = $targetState.session.authenticated ? 'success' : $targetState.session.error ? 'danger' : 'warning';
  $: sessionLabel = $targetState.session.checking ? 'Checking Session' : $targetState.session.authenticated ? 'Admin Session Active' : 'Login Required';
  $: isLoginRoute = $page.url.pathname === '/login';
</script>

<svelte:head>
  <title>MemStation Manager</title>
  <meta name="color-scheme" content="light" />
</svelte:head>

<div class="shell">
  <aside class="shell-sidebar">
    <div class="brand-block">
      <span class="sidebar-label">MemStation</span>
      <span class="brand-title">Manager</span>
      <div class={`shell-status ${sessionTone}`}>
        <span>{sessionLabel}</span>
      </div>
      {#if $targetState.session.error}
        <div class="helper-text">{$targetState.session.error}</div>
      {/if}
    </div>

    {#each navSections as section}
          <div class="sidebar-section">
        <span class="sidebar-label">{section.label}</span>
        <nav class="nav-list">
          {#each section.items as item}
            <a class={`nav-link ${isActive($page.url.pathname, item.href) ? 'is-active' : ''}`} href={item.href}>
              <span class="nav-link-icon">
                <svelte:component this={item.icon} size={16} />
              </span>
              <span>{item.label}</span>
            </a>
          {/each}
        </nav>
      </div>
    {/each}
  </aside>

  <main class="shell-main">
    <div class="topbar">
      {#if !isLoginRoute}
        <div class="topbar-meta">
          <span class="sidebar-label">Target</span>
          <div class="topbar-title">{$selectedTarget?.label ?? 'No target selected'}</div>
          <div class="helper-text">
            {$selectedTarget?.environment ?? 'unknown'}{#if $selectedTarget?.baseUrl} · {$selectedTarget.baseUrl}{/if}
          </div>
        </div>
      {/if}

      <div class="topbar-group">
        <div style="min-width: 280px;">
          <Select.Root type="single" value={$targetState.selectedId} items={targetItems} onValueChange={(value) => selectTarget(value)}>
            <Select.Trigger class="select-trigger">
              {targetItems.find((item) => item.value === $targetState.selectedId)?.label ?? 'Select target'}
            </Select.Trigger>
            <Select.Portal>
              <Select.Content class="select-content" sideOffset={8}>
                <Select.Viewport>
                  {#each targetItems as item}
                    <Select.Item class="select-item" value={item.value} label={item.label}>{item.label}</Select.Item>
                  {/each}
                </Select.Viewport>
              </Select.Content>
            </Select.Portal>
          </Select.Root>
        </div>
        <button class="button-secondary" type="button" on:click={refreshSelectedSession} title="Refresh target session and health">
          <RefreshCw size={16} />
          Refresh
        </button>
        <button class="button-secondary" type="button" on:click={() => (targetDialogOpen = true)}>
          Manage Targets
        </button>
        {#if $targetState.session.authenticated}
          <button class="button-ghost" type="button" on:click={handleLogout}>Logout</button>
        {/if}
        <a class="button" href={$targetState.session.authenticated ? '/dashboard' : '/login'}>
          <LogIn size={16} />
          {$targetState.session.authenticated ? 'Workspace' : 'Login'}
        </a>
      </div>
    </div>

    <slot />
  </main>
</div>

<TargetManagerDialog
  bind:open={targetDialogOpen}
  targets={$targetState.targets}
  selectedId={$targetState.selectedId}
  on:save={handleTargetSave}
  on:remove={(event) => removeTarget(event.detail.id)}
/>
