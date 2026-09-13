<script lang="ts">
  import { assets } from '$app/paths';

  let { size = 32 }: { size?: number } = $props();
</script>

<img src={`${assets}/brand/memstation-96.png`} width={size} height={size} alt="" aria-hidden="true" class="brand-mark" />

<style>
  .brand-mark {
    display: block;
    flex: 0 0 auto;
  }
</style>
