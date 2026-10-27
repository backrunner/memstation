<script lang="ts">
  import { Button } from 'bits-ui';

  let { ref = $bindable(null), children, ...props }: Button.RootProps = $props();
</script>

<Button.Root bind:ref {...props}>
  {@render children?.()}
</Button.Root>
