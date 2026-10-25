import { workspaceRequest } from '#lib/server/workspaces.ts';
import type { MemoryControl, MemoryCandidate, MemoryPublication, Integration, Partition, BackgroundCall, MemoryCapabilities } from '#lib/memory-service.ts';
import type { MemoryGeneration } from '#lib/types.ts';
import type { PageServerLoad } from './$types';
export const load: PageServerLoad = async ({parent,fetch,params,url,depends}) => {
 const data=await parent(); depends('memory:lifecycle');
 const partition=url.searchParams.get('partition_id')??'';
 const base=`/${encodeURIComponent(params.workspace)}`;
 const query=new URLSearchParams({partition_id:partition});
 const paged=(kind:string)=>new URLSearchParams({partition_id:partition,after:url.searchParams.get(`${kind}_after`)??''});
 const [control,candidates,publications,integrations,partitions,usage,capabilities,generations]=await Promise.all([
  workspaceRequest<MemoryControl>(fetch,params.target,`${base}/lifecycle/overview?${paged("control")}`),
  workspaceRequest<{items:MemoryCandidate[]}>(fetch,params.target,`${base}/lifecycle/memory/candidates?${paged("candidates")}`),
  workspaceRequest<{items:MemoryPublication[]}>(fetch,params.target,`${base}/lifecycle/publications?${paged("publications")}`),
  workspaceRequest<{items:Integration[]}>(fetch,params.target,`${base}/integrations?${new URLSearchParams({user_id:data.workspaceResult.value?.user_id??''})}`),
  workspaceRequest<{items:Partition[]}>(fetch,params.target,`${base}/partitions?${new URLSearchParams({user_id:data.workspaceResult.value?.user_id??''})}`),
  workspaceRequest<{items:BackgroundCall[]}>(fetch,params.target,`${base}/lifecycle/usage?${paged("usage")}`),
  workspaceRequest<MemoryCapabilities>(fetch,params.target,`${base}/lifecycle/capabilities?${query}`),
  workspaceRequest<{items:MemoryGeneration[]}>(fetch,params.target,`${base}/lifecycle/generations?${query}`)
 ]);
 return {pageQuery:url.searchParams.toString(),partition,control,candidates,publications,integrations,partitions,usage,capabilities,generations};
};
