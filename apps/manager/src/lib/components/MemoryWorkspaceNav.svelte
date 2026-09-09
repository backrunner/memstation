<svelte:options runes={false} />

<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/stores';

  export let current = '/memory';

  const items = [
    { href: '/memory', label: 'Workspace' },
    { href: '/memory/summaries', label: 'Summaries' },
    { href: '/memory/impressions', label: 'Impressions' },
    { href: '/memory/facts', label: 'Facts' },
    { href: '/memory/items', label: 'Agent Memory' },
    { href: '/memory/keys', label: 'Key Memories' }
  ];

  let suffix = '';
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
    suffix = userId ? `?user_id=${encodeURIComponent(userId)}` : '';
  }
</script>

<nav bind:this={navigation} class="subnav-pills" aria-label="Memory workspace sections">
  {#each items as item}
    <a
      class="subnav-pill"
      tabindex="0"
      class:is-active={current === item.href}
      aria-current={current === item.href ? 'page' : undefined}
      href={`${item.href}${suffix}`}
    >{item.label}</a>
  {/each}
</nav>
