<svelte:options runes={false} />

<script lang="ts">
  import { onMount } from 'svelte';
  import type { ECharts, EChartsCoreOption } from 'echarts/core';

  export let option: EChartsCoreOption = {};
  export let height = 320;

  let element: HTMLDivElement;
  let chart: ECharts | null = null;
  let error = '';

  onMount(() => {
    let disposed = false;
    let observer: ResizeObserver | null = null;

    async function initialize(): Promise<void> {
      try {
        const [{ init, use }, { BarChart, LineChart }, { GridComponent, LegendComponent, TitleComponent, TooltipComponent }, { CanvasRenderer }] =
          await Promise.all([
            import('echarts/core'),
            import('echarts/charts'),
            import('echarts/components'),
            import('echarts/renderers')
          ]);
        // Navigation can finish before the chart bundle arrives.
        if (disposed) return;
        use([LineChart, BarChart, GridComponent, TooltipComponent, TitleComponent, LegendComponent, CanvasRenderer]);
        chart = init(element, undefined, { renderer: 'canvas' });
        observer = new ResizeObserver(() => chart?.resize());
        observer.observe(element);
      } catch {
        if (!disposed) error = 'Chart could not be loaded. Reload this page to try again.';
      }
    }

    void initialize();
    return () => {
      disposed = true;
      observer?.disconnect();
      chart?.dispose();
      chart = null;
    };
  });

  // Keep option an explicit dependency so range and filter changes redraw.
  $: if (chart) chart.setOption(option, true);
</script>

{#if error}<p class="helper-text" role="status">{error}</p>{/if}
<div bind:this={element} class="chart-surface" style={`height: ${height}px;`}></div>
