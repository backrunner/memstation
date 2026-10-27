import { workspaceRequest } from '#lib/server/workspaces.ts';
import type { MemoryControl, MemoryCandidate, MemoryPublication, Integration, Partition, BackgroundCall, MemoryCapabilities } from '#lib/memory-service.ts';
import type { AgentRecord, CursorPage, MemoryGeneration, WorkspacePage } from '#lib/types.ts';
import type { PageServerLoad } from './$types';
export const load: PageServerLoad = async ({parent,fetch,params,url,depends}) => {
 const data=await parent(); depends('memory:lifecycle');
 const partition=url.searchParams.get('partition_id')??'';
 const base=`/${encodeURIComponent(params.workspace)}`;
 const query=new URLSearchParams({partition_id:partition});
 const paged=(kind:string)=>new URLSearchParams({partition_id:partition,after:url.searchParams.get(`${kind}_after`)??''});
 const owner=data.workspaceResult.value?.user_id??'';
 const readAgents=async()=>{
  try {
   const response=await fetch(`/manager/api/targets/${encodeURIComponent(params.target)}/api/admin/users/${encodeURIComponent(owner)}/agents?limit=100&status=active`,{cache:'no-store'});
   if(response.ok)return {value:await response.json() as CursorPage<AgentRecord>,failure:''};
  } catch { /* Keep connection forms usable with an explicit directory error. */ }
  return {value:null,failure:'Unable to load Agents. Reconnect the service or retry.'};
 };
 const [control,candidates,publications,integrations,partitions,usage,capabilities,generations,agents,destinations]=await Promise.all([
  workspaceRequest<MemoryControl>(fetch,params.target,`${base}/lifecycle/overview?${paged("control")}`),
  workspaceRequest<{items:MemoryCandidate[]}>(fetch,params.target,`${base}/lifecycle/memory/candidates?${paged("candidates")}`),
  workspaceRequest<{items:MemoryPublication[]}>(fetch,params.target,`${base}/lifecycle/publications?${paged("publications")}`),
  workspaceRequest<{items:Integration[]}>(fetch,params.target,`${base}/integrations?${new URLSearchParams({user_id:data.workspaceResult.value?.user_id??''})}`),
  workspaceRequest<{items:Partition[]}>(fetch,params.target,`${base}/partitions?${new URLSearchParams({user_id:data.workspaceResult.value?.user_id??''})}`),
  workspaceRequest<{items:BackgroundCall[]}>(fetch,params.target,`${base}/lifecycle/usage?${paged("usage")}`),
  workspaceRequest<MemoryCapabilities>(fetch,params.target,`${base}/lifecycle/capabilities?${query}`),
  workspaceRequest<{items:MemoryGeneration[]}>(fetch,params.target,`${base}/lifecycle/generations?${query}`),
  readAgents(),
  workspaceRequest<WorkspacePage>(fetch,params.target,`?${new URLSearchParams({user_id:owner,limit:'100'})}`)
 ]);
 return {pageQuery:url.searchParams.toString(),partition,control,candidates,publications,integrations,partitions,usage,capabilities,generations,agents,destinations};
};
