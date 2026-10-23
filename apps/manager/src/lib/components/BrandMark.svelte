<script lang="ts">
  import { asset } from '$app/paths';

  let { size = 32 }: { size?: number } = $props();
</script>

<img src={asset('brand/memstation-96.png')} width={size} height={size} alt="" aria-hidden="true" class="brand-mark" />

<style>
  .brand-mark {
    display: block;
    flex: 0 0 auto;
  }
</style>
