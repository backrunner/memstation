<svelte:options runes={false} />

<script lang="ts">
  import '$lib/styles/app.css';

  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/stores';
  import { Select } from 'bits-ui';
  import { LayoutDashboard, LogIn, MessageSquareText, Database, ServerCog, ShieldCheck, FolderCog, RefreshCw, Users, BarChart3, Settings, Play } from 'lucide-svelte';

  import StatusBadge from '$lib/components/StatusBadge.svelte';
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
        { href: '/playground/chat', label: 'Playground Chat', icon: Play },
        { href: '/playground/memory', label: 'Playground Memory', icon: Play },
        { href: '/playground/protocols', label: 'Playground Protocols', icon: Play },
        { href: '/conversations', label: 'Conversations', icon: MessageSquareText },
        { href: '/memory', label: 'Memory Workspace', icon: Database }
      ]
    },
    {
      label: 'Manage',
      items: [
        { href: '/users', label: 'Users', icon: Users },
        { href: '/statistics/tokens', label: 'Statistics', icon: BarChart3 },
        { href: '/settings/system', label: 'System', icon: Settings },
        { href: '/settings/models', label: 'Models', icon: Settings },
        { href: '/settings/providers', label: 'Providers', icon: Settings }
      ]
    },
    {
      label: 'Operations',
      items: [
        { href: '/operations/health', label: 'Health', icon: ServerCog },
        { href: '/operations/queues', label: 'Queues', icon: FolderCog },
        { href: '/operations/tasks', label: 'Tasks', icon: FolderCog },
        { href: '/operations/cache', label: 'Cache', icon: ServerCog },
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
</script>

<svelte:head>
  <title>MemStation Manager</title>
  <meta name="color-scheme" content="light" />
</svelte:head>

<div class="shell">
  <aside class="shell-sidebar">
    <div class="brand-block">
      <span class="sidebar-label">MemStation</span>
      <span class="brand-title">Manager Control Plane</span>
      <span class="brand-subtitle">Direct-to-target operations console.</span>
    </div>

    <div class="sidebar-section">
      <span class="sidebar-label">Session</span>
      <div class="inline-group" style="justify-content: space-between;">
        <StatusBadge tone={$targetState.session.authenticated ? 'success' : $targetState.session.error ? 'danger' : 'warning'} label={$targetState.session.checking ? 'Checking' : $targetState.session.authenticated ? 'Admin Session Active' : 'Login Required'} />
        {#if $targetState.session.authenticated}
          <button class="button-ghost" type="button" on:click={handleLogout}>Logout</button>
        {:else}
          <a class="button-ghost" href="/login">Login</a>
        {/if}
      </div>
      {#if $targetState.session.error}
        <div class="error-state">{$targetState.session.error}</div>
      {/if}
    </div>

    {#each navSections as section}
      <div class="sidebar-section">
        <span class="sidebar-label">{section.label}</span>
        <nav class="nav-list">
          {#each section.items as item}
            <a class={`nav-link ${isActive($page.url.pathname, item.href) ? 'is-active' : ''}`} href={item.href}>
              <span class="inline-group">
                <svelte:component this={item.icon} size={16} />
                <span>{item.label}</span>
              </span>
            </a>
          {/each}
        </nav>
      </div>
    {/each}
  </aside>

  <main class="shell-main">
    <div class="topbar">
      <div>
        <div class="inline-group" style="margin-bottom: 6px;">
          <span class="sidebar-label">Target</span>
          <StatusBadge tone="brand" label={$selectedTarget?.environment ?? 'unknown'} />
        </div>
        <div class="brand-title" style="font-size: 1rem;">{$selectedTarget?.label ?? 'No target selected'}</div>
        <div class="brand-subtitle">{$selectedTarget?.baseUrl ?? 'Select a MemStation target to begin.'}</div>
      </div>

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
        <a class="button" href={$targetState.session.authenticated ? '/dashboard' : '/login'}>
          <LogIn size={16} />
          {$targetState.session.authenticated ? 'Open Workspace' : 'Login'}
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
