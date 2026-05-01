<svelte:options runes={false} />

<script lang="ts">
  import { page } from '$app/stores';

  export let current = '/memory';

  const items = [
    { href: '/memory', label: 'Workspace' },
    { href: '/memory/summaries', label: 'Summaries' },
    { href: '/memory/impressions', label: 'Impressions' },
    { href: '/memory/facts', label: 'Facts' },
    { href: '/memory/keys', label: 'Key Memories' }
  ];

  let suffix = '';

  $: {
    const userId = $page.url.searchParams.get('user_id');
    suffix = userId ? `?user_id=${encodeURIComponent(userId)}` : '';
  }
</script>

<nav class="subnav-pills" aria-label="Memory workspace sections">
  {#each items as item}
    <a
      class="subnav-pill"
      class:is-active={current === item.href}
      href={`${item.href}${suffix}`}
    >{item.label}</a>
  {/each}
</nav>
