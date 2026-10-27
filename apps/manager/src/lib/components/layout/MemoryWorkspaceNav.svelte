<svelte:options runes={false} />

<script lang="ts">
  import { selectedTarget } from '#lib/stores/targets.ts';
  import { workspaceLink } from '#lib/scope/navigation.ts';
  import { onMount } from 'svelte';
  import { createPageStore } from '#lib/stores/page.ts';

  const page = createPageStore();

  export let current = '/memory';

  const items = [
    { kind:'', href: '/memory', label: 'Memory documents' },
    { kind:'summary', href: '/memory/summaries', label: 'Summaries' },
    { kind:'impression', href: '/memory/impressions', label: 'Impressions' },
    { kind:'fact', href: '/memory/facts', label: 'Facts' },
    { kind:'', href: '/memory/items', label: 'Agent Memory' },
    { kind:'', href: '/memory/keys', label: 'Key Memories' }
  ];

  let owner: string | undefined;
  let navigation: HTMLElement;

  onMount(() => {
    const revealActive = () => {
      const active = navigation.querySelector<HTMLElement>('[aria-current=page]');
      if (active) navigation.scrollLeft = Math.max(0, active.offsetLeft - navigation.offsetLeft - 12);
    };
    const observer = new ResizeObserver(revealActive);
    observer.observe(navigation);
    revealActive();
    return () => observer.disconnect();
  });

  $: {
    const userId = $page.url.searchParams.get('user_id');
    owner = userId || undefined;
  }
</script>

<nav bind:this={navigation} class="subnav-pills" aria-label="Memory workspace sections">
  {#each items as item}
    <a
      class="subnav-pill"
      tabindex="0"
      class:is-active={current === item.href}
      aria-current={current === item.href ? 'page' : undefined}
      href={workspaceLink($selectedTarget?.id,item.href === '/memory/keys' ? 'keys' : 'memory', {owner,kind:item.kind || undefined})}
    >{item.label}</a>
  {/each}
</nav>
