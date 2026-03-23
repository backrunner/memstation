<svelte:options runes={false} />

<script lang="ts">
  import { onMount } from 'svelte';

  import type { ECharts, EChartsCoreOption } from 'echarts/core';

  export let option: EChartsCoreOption = {};
  export let height = 320;

  let element: HTMLDivElement;
  let chart: ECharts | null = null;
  let bootPromise: Promise<void> | null = null;

  function render(): void {
    if (!chart) {
      return;
    }
    chart.setOption(option, true);
    chart.resize();
  }

  async function ensureChart(): Promise<void> {
    if (!element || chart) {
      return;
    }
    if (!bootPromise) {
      bootPromise = (async () => {
        const [{ init, use }, { BarChart, LineChart }, { GridComponent, LegendComponent, TitleComponent, TooltipComponent }, { CanvasRenderer }] =
          await Promise.all([
            import('echarts/core'),
            import('echarts/charts'),
            import('echarts/components'),
            import('echarts/renderers')
          ]);
        use([LineChart, BarChart, GridComponent, TooltipComponent, TitleComponent, LegendComponent, CanvasRenderer]);
        chart = init(element, undefined, {
          renderer: 'canvas'
        });
      })();
    }
    await bootPromise;
    render();
  }

  onMount(() => {
    let disposed = false;
    let observer: ResizeObserver | null = null;

    void ensureChart().then(() => {
      if (disposed || !element || !chart) {
        return;
      }
      observer = new ResizeObserver(() => {
        chart?.resize();
      });
      observer.observe(element);
    });

    return () => {
      disposed = true;
      observer?.disconnect();
      chart?.dispose();
      chart = null;
      bootPromise = null;
    };
  });

  $: if (chart) {
    render();
  } else if (element) {
    void ensureChart();
  }
</script>

<div bind:this={element} class="chart-surface" style={`height: ${height}px;`}></div>
