<script lang="ts">
  import { onMount } from 'svelte';

  let { items }: { items: { id: string; label: string }[] } = $props();
  let navigation: HTMLElement;
  let active = $state('');
  let maskStart = $state(false);
  let maskEnd = $state(false);

  onMount(() => {
    let frame = 0;
    let initialFrame = 0;
    let revealRequested = true;
    let requested: { id: string; arrived: boolean } | null = null;
    const horizontal = () => window.matchMedia('(max-width: 900px)').matches;
    const updateMask = () => {
      const position = horizontal() ? navigation.scrollLeft : navigation.scrollTop;
      const visible = horizontal() ? navigation.clientWidth : navigation.clientHeight;
      const total = horizontal() ? navigation.scrollWidth : navigation.scrollHeight;
      maskStart = position > 1;
      maskEnd = position + visible < total - 1;
    };
    const reveal = (id: string) => {
      const link = navigation.querySelectorAll<HTMLAnchorElement>('a')[items.findIndex(item => item.id === id)];
      if (!link) return;
      const container = navigation.getBoundingClientRect();
      const selected = link.getBoundingClientRect();
      if (horizontal()) {
        if (selected.left < container.left + 14) navigation.scrollLeft += selected.left - container.left - 14;
        else if (selected.right > container.right - 14) navigation.scrollLeft += selected.right - container.right + 14;
      } else {
        if (selected.top < container.top + 14) navigation.scrollTop += selected.top - container.top - 14;
        else if (selected.bottom > container.bottom - 14) navigation.scrollTop += selected.bottom - container.bottom + 14;
      }
    };
    const update = () => {
      frame = 0;
      const sections = items.map(item => ({ ...item, element: document.getElementById(item.id) })).filter(item => item.element);
      if (!sections.length) return;
      // Use the same resolved offset as native anchor navigation, including the measured header.
      const boundary = Number.parseFloat(getComputedStyle(sections[0].element!).scrollMarginTop) || 0;
      let current = sections[0].id;
      for (const section of sections) {
        if (section.element!.getBoundingClientRect().top <= boundary + 2) current = section.id;
        else break;
      }
      if (scrollY > 0 && scrollY + innerHeight >= document.documentElement.scrollHeight - 2) current = sections.at(-1)!.id;
      const intent = requested;
      if (intent) {
        const target = sections.find(section => section.id === intent.id);
        const bounds = target?.element?.getBoundingClientRect();
        if (bounds && bounds.bottom > boundary && bounds.top < innerHeight) {
          const end = Math.max(0, Math.min(scrollY + bounds.top - boundary, document.documentElement.scrollHeight - innerHeight));
          // Several short sections can share the same clamped scroll destination.
          // Preserve the chosen anchor there until the user resumes scrolling.
          if (Math.abs(scrollY - end) <= 2) {
            intent.arrived = true;
            current = intent.id;
          } else if (intent.arrived) current = intent.id;
        }
      }
      const changed = active !== current;
      active = current;
      if (changed || revealRequested) reveal(current);
      revealRequested = false;
      updateMask();
    };
    const schedule = () => { if (!frame) frame = requestAnimationFrame(update); };
    const choose = (id: string) => {
      requested = items.some(item => item.id === id) ? { id, arrived: false } : null;
      schedule();
    };
    const anchorClick = (event: MouseEvent) => {
      if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.altKey || event.shiftKey) return;
      const link = (event.target instanceof Element ? event.target : null)?.closest('a');
      if (link) choose(link.hash.slice(1));
    };
    const hashChange = () => choose(location.hash.slice(1));
    const scrollKeys = new Set(['ArrowUp', 'ArrowDown', 'PageUp', 'PageDown', 'Home', 'End', ' ']);
    const resumeScrolling = (event: Event) => {
      if (event.defaultPrevented || document.documentElement.scrollHeight <= innerHeight + 2) return;
      if (event instanceof WheelEvent && (event.ctrlKey || event.metaKey || event.deltaY === 0)) return;
      if (event.type === 'touchmove' && (event as TouchEvent).touches.length !== 1) return;
      const target = event.target instanceof Element ? event.target : null;
      if (target?.closest('nav, [role="listbox"]')) return;
      if (event instanceof KeyboardEvent && (!scrollKeys.has(event.key) || target?.closest('input, textarea, select, [contenteditable], [role="combobox"], a, button, [role="button"], [role="switch"]'))) return;
      requested = null;
      schedule();
    };
    const scrollbarPointer = (event: PointerEvent) => {
      if (event.clientX >= document.documentElement.clientWidth - 16 && !navigation.contains(event.target as Node)) resumeScrolling(event);
    };
    const resize = () => { revealRequested = true; schedule(); };
    let navigationWidth = navigation.clientWidth;
    let navigationHeight = navigation.clientHeight;
    const observer = new ResizeObserver(() => {
      // Content and header changes can move sections without changing the TOC viewport.
      // Only reveal again when its available space changes; manual TOC scrolling stays put.
      if (navigation.clientWidth !== navigationWidth || navigation.clientHeight !== navigationHeight) {
        navigationWidth = navigation.clientWidth;
        navigationHeight = navigation.clientHeight;
        revealRequested = true;
      }
      schedule();
    });
    observer.observe(navigation);
    const container = navigation.parentElement;
    const sheet = container?.querySelector('.settings-sheet');
    const header = container?.closest('.page-stack')?.querySelector(':scope > .page-header');
    if (sheet) observer.observe(sheet);
    if (header) observer.observe(header);
    window.addEventListener('scroll', schedule, { passive: true });
    window.addEventListener('resize', resize, { passive: true });
    window.addEventListener('hashchange', hashChange);
    window.addEventListener('wheel', resumeScrolling, { passive: true });
    window.addEventListener('touchmove', resumeScrolling, { passive: true });
    window.addEventListener('keydown', resumeScrolling);
    window.addEventListener('pointerdown', scrollbarPointer, { passive: true });
    navigation.addEventListener('click', anchorClick);
    navigation.addEventListener('scroll', updateMask, { passive: true });
    hashChange();
    // The form loads asynchronously; the browser can resolve the initial hash
    // before its section exists. Restore it once this navigation is mounted.
    const initialId = items.find(item => item.id === location.hash.slice(1))?.id;
    if (initialId) initialFrame = requestAnimationFrame(() => {
      if (requested?.id === initialId) document.getElementById(initialId)?.scrollIntoView({ block: 'start' });
    });
    return () => {
      cancelAnimationFrame(frame);
      cancelAnimationFrame(initialFrame);
      observer.disconnect();
      window.removeEventListener('scroll', schedule);
      window.removeEventListener('resize', resize);
      window.removeEventListener('hashchange', hashChange);
      window.removeEventListener('wheel', resumeScrolling);
      window.removeEventListener('touchmove', resumeScrolling);
      window.removeEventListener('keydown', resumeScrolling);
      window.removeEventListener('pointerdown', scrollbarPointer);
      navigation.removeEventListener('click', anchorClick);
      navigation.removeEventListener('scroll', updateMask);
    };
  });
</script>

<nav bind:this={navigation} class="system-section-nav" aria-label="System sections" data-mask-start={maskStart} data-mask-end={maskEnd}>
  {#each items as item (item.id)}<a href={`#${item.id}`} aria-current={active === item.id ? 'location' : undefined}>{item.label}</a>{/each}
</nav>

<style>
  .system-section-nav {
    --fade-start: 0px; --fade-end: 0px;
    display: grid; gap: 2px; position: sticky; top: var(--page-anchor-offset);
    max-height: max(44px, calc(100dvh - var(--page-anchor-offset) - var(--shell-inset) - var(--page-padding-bottom)));
    overflow-y: auto; overscroll-behavior: contain; scrollbar-width: thin;
    mask-image: linear-gradient(to bottom, transparent, #000 var(--fade-start), #000 calc(100% - var(--fade-end)), transparent);
  }
  .system-section-nav[data-mask-start='true'] { --fade-start: 12px; }
  .system-section-nav[data-mask-end='true'] { --fade-end: 12px; }
  a { display: flex; align-items: center; justify-content: flex-start; text-align: left; min-height: 36px; padding: 6px 10px; border-radius: var(--radius-sm); color: var(--text-secondary); font-size: var(--font-size-md); }
  a:hover { color: var(--text); background: var(--surface-muted); }
  a[aria-current] { color: var(--brand-strong); background: var(--brand-soft); font-weight: 600; }
  @media (max-width: 900px) {
    .system-section-nav { display: flex; position: static; max-height: none; overflow-x: auto; overflow-y: hidden; min-width: 0; border-bottom: 1px solid var(--border); mask-image: linear-gradient(to right, transparent, #000 var(--fade-start), #000 calc(100% - var(--fade-end)), transparent); }
    a { flex-shrink: 0; min-height: 44px; }
  }
</style>
