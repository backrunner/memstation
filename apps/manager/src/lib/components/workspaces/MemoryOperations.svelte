<script lang="ts">
 import { untrack } from 'svelte';
 import { beforeNavigate, invalidate } from '$app/navigation';
 import Button from '../Button.svelte';
 import CollapsibleSection from '../CollapsibleSection.svelte';
 import PageHeader from '../PageHeader.svelte';
 import TextInputField from '../TextInputField.svelte';
 import TextareaField from '../TextareaField.svelte';
 import SelectField from '../SelectField.svelte';
 import MetadataRow from '../MetadataRow.svelte';
 import CapabilityFields from '../agents/CapabilityFields.svelte';
 import { useSessionDraftUnloadWarning } from '#lib/stores/session-drafts.ts';
 import type { PageData } from '../../../routes/t/[target]/w/[workspace]/lifecycle/$types';
 import type { Integration, Partition, MemoryCandidate, RecordDetail, SourceDetail, MemoryOperation } from '#lib/memory-service.ts';
 let {data}:{data:PageData}=$props();
 let workspace=$derived(data.workspaceResult.value);
 let partition=$state(untrack(()=>data.partition));
 let busy=$state(false);let error=$state('');let success=$state('');
 let content=$state('');let kind=$state('fact');let subjects=$state('');let targetNode=$state('');let targetRevision=$state(0);
 let selectedSource=$state<SourceDetail|null>(null);let inspectRecord=$state<RecordDetail|null>(null);let recordID=$state('');
 let confirmedKind=$state('fact');let selectedOperation=$state<MemoryOperation|null>(null);
 let integration=$state<Integration>({id:'',name:'',kind:'chat',agent_id:'',external_space_id:'',allowed_event_types:['message.created','message.edited','message.deleted'],revision:0});
 let privatePartition=$state<Partition>({id:'',agent_id:'',audience_actor_ids:[],revision:0});let audience=$state('');
 let grantAgent=$state('');let grantRevision=$state(0);let grantCaps=$state<string[]>([]);let grantLoaded=$state(false);
 let publicationTarget=$state('');let publicationPartition=$state('');let replacement=$state('');let replacementRevision=$state(0);let preview=$state(false);
 let eraseKind=$state('source');let eraseID=$state('');let eraseRevision=$state(1);let eraseConfirm=$state('');
 let setupDirty=$state(false);
 const kinds=['fact','preference','event','relationship','group_habit','summary','inference'].map(value=>({value,label:value.replaceAll('_',' ')}));
 $effect(()=>{if(selectedSource){const state=data.control.value?.sources.find(s=>s.id===selectedSource!.id);if(state&&state.status!=='active')selectedSource=null;}if(inspectRecord&&data.candidates.value?.items.some(c=>c.result_node_id===inspectRecord!.node_id&&c.status==='invalidated')){inspectRecord=null;preview=false;}});
 const dirty=$derived(Boolean(content||setupDirty));
 useSessionDraftUnloadWarning(()=>dirty);
 beforeNavigate(n=>{if(!n.willUnload&&dirty&&!window.confirm('Discard unsaved memory changes and leave this page?'))n.cancel();});
 function nextPage(kind:string,after:string){const q=new URLSearchParams(data.pageQuery);q.set(`${kind}_after`,after);return `?${q}`;}
 function split(value:string){return value.split(',').map(v=>v.trim()).filter(Boolean);}
 const root=$derived(`/manager/api/targets/${encodeURIComponent(data.routeTarget.id)}/api/admin/workspaces/${encodeURIComponent(workspace?.id??'')}`);
 const pendingKeys=new Map<string,string>();
 async function request<T>(path:string,body?:unknown,method='POST',management=false):Promise<T>{
  const q=new URLSearchParams({user_id:workspace?.user_id??'',...(management?{}:{partition_id:data.partition})});
  const payload=body===undefined?undefined:JSON.stringify(body);
  const identity=`${method}:${root}:${data.partition}:${path}:${payload??''}`;
  if(body!==undefined&&!pendingKeys.has(identity))pendingKeys.set(identity,crypto.randomUUID());
  const response=await fetch(`${root}${management?'':'/lifecycle'}${path}?${q}`,{method:body===undefined?'GET':method,headers:{'Content-Type':'application/json',...(body!==undefined?{'Idempotency-Key':pendingKeys.get(identity)!}: {})},body:payload,cache:'no-store'});
  if(!response.ok)throw new Error(response.status===409?'This record changed. Your draft is preserved; reload its current revision before retrying.':response.status===403?'You no longer have permission for this scope.':response.status===404?'This record or source is no longer visible.':`The operation failed (${response.status}). Your draft is preserved.`);
  const value=await response.json() as T;pendingKeys.delete(identity);return value;
 }
 async function act(action:()=>Promise<void>,message='Saved.'){if(busy)return;busy=true;error='';success='';try{await action();success=message;await invalidate('memory:lifecycle');}catch(cause){error=cause instanceof Error?cause.message:'Unable to complete this operation.';}finally{busy=false;}}
 async function saveIntegration(){await act(async()=>{integration=await request<Integration>('/integrations',{...integration,user_id:workspace?.user_id,workspace_id:workspace?.id},'PUT',true);setupDirty=false;},'Integration binding saved.');}
 function editIntegration(value:Integration){integration=structuredClone(value);setupDirty=false;}
 async function savePartition(){await act(async()=>{privatePartition=await request<Partition>('/partitions',{...privatePartition,user_id:workspace?.user_id,workspace_id:workspace?.id,audience_actor_ids:split(audience)},'PUT',true);setupDirty=false;},'Private audience policy saved.');}
 function editPartition(value:Partition){privatePartition=structuredClone(value);audience=value.audience_actor_ids.join(', ');setupDirty=false;}
 async function loadGrant(){await act(async()=>{const g=await request<{revision:number;capabilities:string[]}>(`/agents/${encodeURIComponent(grantAgent)}/grant`,undefined,'GET',true);grantRevision=g.revision;grantCaps=g.capabilities;grantLoaded=true;setupDirty=false;},'Current permissions loaded.');}
 async function saveGrant(){await act(async()=>{const g=await request<{revision:number;capabilities:string[]}>(`/agents/${encodeURIComponent(grantAgent)}/grant`,{user_id:workspace?.user_id,capabilities:grantCaps,expected_revision:grantRevision},'PUT',true);grantRevision=g.revision;grantLoaded=true;setupDirty=false;},'Workspace permissions saved.');}
 async function propose(event:SubmitEvent){event.preventDefault();await act(async()=>{await request('/memory/candidates',{kind,content,partition_id:data.partition,subject_actor_ids:split(subjects),source_refs:selectedSource?[{workspace_id:workspace?.id,message_id:selectedSource.id,revision:selectedSource.revision,part_index:0}]:[],target_node_id:targetNode,target_revision:targetRevision});content='';targetNode='';targetRevision=0;},'Candidate submitted for review.');}
 async function decide(c:MemoryCandidate,decision:string){await act(async()=>{await request(`/memory/candidates/${encodeURIComponent(c.id)}/decision`,{expected_revision:c.revision,decision,...(c.kind==='inference'?{confirmed_kind:confirmedKind}:{})});},decision==='approve'?'Candidate approved. Indexing continues in the background.':'Candidate rejected.');}
 async function source(id:string){await act(async()=>{selectedSource=await request<SourceDetail>(`/sources/${encodeURIComponent(id)}`);},'Source loaded.');}
 async function inspect(){await act(async()=>{inspectRecord=await request<RecordDetail>(`/memory/records/${encodeURIComponent(recordID)}`);preview=false;},'Memory revision loaded.');}
 function correct(){if(inspectRecord){targetNode=inspectRecord.node_id;targetRevision=inspectRecord.revision;content=inspectRecord.content;subjects=inspectRecord.subject_actor_ids.join(', ');}}
 async function publish(){if(!inspectRecord)return;await act(async()=>{await request('/publications',{source_node_id:inspectRecord!.node_id,source_revision:inspectRecord!.revision,target_workspace_id:publicationTarget,target_partition_id:publicationPartition,actor_mapping:{},replaces_publication_id:replacement,expected_publication_revision:replacementRevision});preview=false;replacement='';replacementRevision=0;},'Publication copy approved.');}
 async function operation(id:string){await act(async()=>{selectedOperation=await request<MemoryOperation>(`/operations/${encodeURIComponent(id)}`);},'Operation refreshed.');}
 async function erase(){if(eraseConfirm!==workspace?.name)return;await act(async()=>{const selector={partition_id:data.partition,...(eraseKind==='workspace'?{workspace:true}:eraseKind==='actor'?{actor_id:eraseID}:eraseKind==='node'?{node_ids:[eraseID]}:{source_refs:[{workspace_id:workspace?.id,message_id:eraseID,revision:eraseRevision,part_index:0}]})};selectedOperation=await request<MemoryOperation>('/memory/forget',selector);eraseConfirm='';selectedSource=null;inspectRecord=null;preview=false;},'Deletion accepted. Reads are blocked before background cleanup.');}
 function download(){if(!selectedOperation?.result)return;const href=URL.createObjectURL(new Blob([JSON.stringify(selectedOperation.result,null,2)],{type:'application/json'}));const a=document.createElement('a');a.href=href;a.download=`memory-export-${selectedOperation.id}.json`;a.click();URL.revokeObjectURL(href);}
</script>

<section class="page-stack memory-service">
 <PageHeader title="Memory service" subtitle={workspace?.name??'Workspace unavailable'} />
 {#if error}<div class="error-state" role="alert">{error}</div>{/if}
 {#if success}<p class="notice-card" role="status">{success}</p>{/if}
 <form method="GET" class="scope-form"><SelectField label="Visible partition" name="partition_id" bind:value={partition} items={[{value:'',label:'Workspace audience'},...(data.partitions.value?.items??[]).map(p=>({value:p.id,label:`Private: ${p.id}`}))]} /><Button type="submit" class="btn-secondary">Open partition</Button><Button class="btn-secondary" disabled={busy} onclick={()=>invalidate('memory:lifecycle')}>Refresh status</Button></form>
 {#if data.partition}<p class="notice-card">Private content inspection is audited. This view is limited to the selected audience partition.</p>{/if}
 {#each [data.control,data.candidates,data.publications,data.integrations,data.partitions,data.usage,data.capabilities,data.generations].filter(v=>v.failure) as failure}<p class="error-state" role="alert">{failure.failure}</p>{/each}
 <CollapsibleSection title="AI runtime and index generations">
  {#if data.capabilities.value}
   <MetadataRow items={[`AI: ${data.capabilities.value.runtime.ai_backend}`,`Usage authority: ${data.capabilities.value.runtime.usage_authority}`]} />
   {#if data.capabilities.value.runtime.ai_backend==='broker'}
    <p class="helper-text">Memory AI roles use the deployment broker configuration. Provider settings apply to direct gateway calls. Broker credentials are never shown here.</p>
    {#each Object.entries(data.capabilities.value.runtime.role_aliases) as [role,alias]}<MetadataRow items={[role,alias]} />{/each}
    <MetadataRow items={[data.capabilities.value.runtime.embedding.model_version,`${data.capabilities.value.runtime.embedding.dimensions} dimensions`,data.capabilities.value.runtime.embedding.fingerprint]} />
   {/if}
  {/if}
  <p class="helper-text">Rebuild covers every partition in this workspace. Existing approved facts, corrections and publications are preserved. Changed sources or model fingerprints prevent an unsafe switch.</p>
  <Button class="btn-secondary" disabled={busy} onclick={()=>act(async()=>{await request('/generations',{user_id:workspace?.user_id,scope:'user',activate_on_success:true});},'Index rebuild queued. The current generation remains readable until the replacement is complete.')}>Rebuild workspace index</Button>
  {#each data.generations.value?.items??[] as generation(generation.generation_id)}
   <div class="record-row"><div><strong>{generation.is_active?'Active generation':generation.status}</strong><MetadataRow items={[generation.generation_id,`${generation.processed_count}/${generation.source_count} processed`,`Embedding version ${generation.embedding_version}`]} />{#if generation.failure_count}<p class="error-state">{generation.failure_count} failed tasks. Check processing jobs below.</p>{/if}</div>
   {#if !generation.is_active && generation.status!=='discarded'}<Button class="btn-secondary" disabled={busy} onclick={()=>act(async()=>{await request(`/generations/${encodeURIComponent(generation.generation_id)}`,{},'DELETE');},'Inactive generation discarded.')}>Discard generation</Button>{/if}
   </div>
  {/each}
 </CollapsibleSection>
 <CollapsibleSection title="Integration and access settings">
  <p class="panel-meta">Workspace members describe people. Agent grants authorize service credentials independently.</p>
  <a class="text-link" href="access">Manage agents and service credentials</a>
  <form class="page-stack" onsubmit={e=>{e.preventDefault();void saveGrant();}} oninput={()=>setupDirty=true}>
   <TextInputField label="Agent ID" bind:value={grantAgent} required oninput={()=>grantLoaded=false} />
   <div><Button class="btn-secondary" onclick={loadGrant} disabled={busy||!grantAgent}>Load current grant</Button></div>
   <CapabilityFields bind:value={grantCaps} allowed={workspace?.capabilities??[]} disabled={busy} />
   <p class="helper-text">For a new grant, revision 0 creates it. Existing grants require loading their current revision.</p>
   <div><Button type="submit" class="btn-primary" disabled={busy||!grantAgent}>{grantLoaded?'Save grant':'Create grant'}</Button></div>
  </form>
  {#each data.integrations.value?.items??[] as item (item.id+item.agent_id)}<div class="record-row"><div><strong>{item.name}</strong><MetadataRow items={[item.external_space_id,item.agent_id,`Revision ${item.revision}`]} /></div><Button class="btn-secondary" onclick={()=>editIntegration(item)}>Edit binding</Button></div>{/each}
  <form class="page-stack" onsubmit={e=>{e.preventDefault();void saveIntegration();}} oninput={()=>setupDirty=true}>
   <h2 class="section-title">{integration.id?'Edit integration':'New integration'}</h2>
   {#if integration.id}<p class="helper-text">This edits one existing observer binding. Revoke its ingestion permissions before replacing it. Additional observers can share the integration through the administration API.</p>{/if}
   <div class="fields"><TextInputField label="Integration name" bind:value={integration.name} required /><TextInputField label="Integration kind" bind:value={integration.kind} readonly={Boolean(integration.id)} required /><TextInputField label="External space ID" bind:value={integration.external_space_id} readonly={Boolean(integration.id)} required /><TextInputField label="Observing agent ID" bind:value={integration.agent_id} readonly={Boolean(integration.id)} required /></div>
   <div class="actions"><Button type="submit" class="btn-primary" disabled={busy}>Save integration</Button><Button class="btn-secondary" onclick={()=>{integration={id:'',name:'',kind:'chat',agent_id:'',external_space_id:'',allowed_event_types:['message.created','message.edited','message.deleted'],revision:0};setupDirty=false;}}>New binding</Button>{#if integration.id}<Button class="btn-secondary" disabled={busy} onclick={()=>{integration.allowed_event_types=[];void saveIntegration();}}>Revoke ingestion</Button>{/if}</div>
  </form>
  {#each data.partitions.value?.items??[] as item(item.id)}<div class="record-row"><div><strong>{item.id}</strong><MetadataRow items={[item.agent_id,`Revision ${item.revision}`,`Audience: ${item.audience_actor_ids.join(', ')}`]} /></div><Button class="btn-secondary" onclick={()=>editPartition(item)}>Edit audience</Button></div>{/each}
  <form class="page-stack" onsubmit={e=>{e.preventDefault();void savePartition();}} oninput={()=>setupDirty=true}>
   <h2 class="section-title">Private audience policy</h2><TextInputField label="Authorized agent ID" bind:value={privatePartition.agent_id} required /><TextInputField label="Audience actor IDs" bind:value={audience} required={privatePartition.revision === 0} help="Comma-separated trusted actor IDs. Clear all to revoke this existing partition. Group output cannot read private content." />
   <div><Button type="submit" class="btn-primary" disabled={busy}>Save private audience</Button></div>
  </form>
 </CollapsibleSection>
 <CollapsibleSection title="Actors and sources" count={data.control.value?.sources.length??0}>
  {#each data.control.value?.actors??[] as actor(actor.actor_id)}<div class="record-row"><div><strong>{actor.display_name||'Unnamed actor'}</strong><MetadataRow items={[actor.actor_id,actor.kind,`Revision ${actor.revision}`]} /></div></div>{/each}
  {#each data.control.value?.sources??[] as item(item.id)}<div class="record-row"><div><strong>{item.id}</strong><MetadataRow items={[`Revision ${item.revision}`,item.status,item.speaker_actor_id||'Unknown author']} /></div><Button class="btn-secondary" disabled={busy||item.status!=='active'} onclick={()=>source(item.id)}>Read source</Button></div>{/each}
  {#if selectedSource}<article class="page-stack"><h3 class="section-title">Source revision {selectedSource.revision}</h3><p class="mono">{selectedSource.id}</p>{#each selectedSource.parts as part}<p class="source-text">{part.text??''}</p>{/each}<div><Button class="btn-secondary" onclick={()=>selectedSource=null}>Clear selected source</Button></div></article>{/if}
 </CollapsibleSection>
 <section class="page-stack"><h2 class="section-title">Processing progress</h2>
  {#each data.control.value?.receipts??[] as receipt(receipt.id)}<article class="record-row"><div><strong>{receipt.id}</strong><MetadataRow items={[receipt.status,`Accepted ${receipt.accepted_sequence}`,`Indexed ${receipt.indexed_sequence}`,receipt.stage??'',receipt.generation_id??'']} />{#if receipt.failure_code}<p class="error-state">{receipt.failure_code}. Attempt {receipt.retry_count}. {receipt.dead_letter?'Dead letter queue.':''}</p>{/if}</div></article>{:else}<p class="panel-meta">No ingestion receipts in this partition.</p>{/each}
  {#each data.control.value?.jobs.filter(j=>j.status!=='complete')??[] as job(job.id)}<article class="record-row"><div><strong>{job.task_type}</strong><MetadataRow items={[job.status,`Attempt ${job.attempt}`,job.failure_code??'',job.next_retry_at??'']} /></div>{#if ['failed','dead_letter'].includes(job.status)}<Button class="btn-secondary" disabled={busy} onclick={()=>act(async()=>{await request(`/jobs/${encodeURIComponent(job.id)}/replay`,{});},'Original task queued for replay.')}>Replay task</Button>{/if}</article>{/each}
 </section>
 {#if data.control.value?.next_cursor}<a class="text-link" href={nextPage("control",data.control.value.next_cursor)}>Next actors, sources and processing records</a>{/if}
 <section class="page-stack"><h2 class="section-title">Candidate review</h2>
  {#each data.candidates.value?.items??[] as candidate(candidate.id)}<article class="candidate"><MetadataRow items={[candidate.kind,candidate.status,`Revision ${candidate.revision}`]} /><p class="source-text">{candidate.content||'Source no longer available.'}</p><MetadataRow items={[candidate.subject_actor_ids.join(', ')||'Workspace subject',`${candidate.source_refs.length} source references`]} />
   {#each candidate.source_refs as ref}<Button class="btn-secondary" disabled={busy||candidate.status==='invalidated'} onclick={()=>source(ref.message_id)}>Read source revision {ref.revision}</Button>{/each}
   {#if candidate.status==='pending'}{#if candidate.kind==='inference'}<SelectField label="Confirmed memory kind" bind:value={confirmedKind} items={kinds.filter(v=>v.value!=='inference')} />{/if}<div class="actions"><Button class="btn-primary" disabled={busy} onclick={()=>decide(candidate,'approve')}>Approve revision {candidate.revision}</Button><Button class="btn-secondary" disabled={busy} onclick={()=>decide(candidate,'reject')}>Reject</Button></div>{/if}
   {#if candidate.result_node_id&&candidate.status==='approved'}<Button class="btn-secondary" onclick={()=>{recordID=candidate.result_node_id!;void inspect();}}>Open approved memory</Button>{/if}
  </article>{:else}<p class="panel-meta">No candidates in this partition.</p>{/each}
  {#if data.candidates.value?.items.length===100}<a class="text-link" href={nextPage("candidates",data.candidates.value.items.at(-1)!.id)}>Next candidates</a>{/if}
  <CollapsibleSection title="Propose or correct memory" open={Boolean(content)}><form class="page-stack" onsubmit={propose}>
   <SelectField label="Memory kind" bind:value={kind} items={kinds} /><TextareaField label="Memory content" bind:value={content} required maxlength={16000} /><TextInputField label="Subject actor IDs" bind:value={subjects} help="Leave empty for a workspace-level observation." />
   <p class="helper-text">{selectedSource?`Evidence: ${selectedSource.id}, revision ${selectedSource.revision}.`:'No selected source. Approval will be recorded as a human attestation.'} {targetNode?`Corrects ${targetNode}, revision ${targetRevision}.`:''}</p><div><Button type="submit" class="btn-primary" disabled={busy}>Submit candidate</Button></div>
  </form></CollapsibleSection>
 </section>
 <CollapsibleSection title="Inspect and publish memory" open={Boolean(inspectRecord)}>
  <form class="scope-form" onsubmit={e=>{e.preventDefault();void inspect();}}><TextInputField label="Memory node ID" bind:value={recordID} required /><Button type="submit" class="btn-secondary" disabled={busy}>Load memory</Button></form>
  {#if inspectRecord}<p class="source-text">{inspectRecord.content}</p><MetadataRow items={[`Revision ${inspectRecord.revision}`,inspectRecord.generation_id,inspectRecord.status]} /><div><Button class="btn-secondary" onclick={correct}>Prepare correction candidate</Button></div>
   <form class="page-stack" onsubmit={e=>{e.preventDefault();preview=true;}}><TextInputField label="Target workspace ID" bind:value={publicationTarget} required /><TextInputField label="Target private partition ID" bind:value={publicationPartition} help="Leave empty to publish to the target workspace audience." /><div><Button type="submit" class="btn-secondary">Preview publication</Button></div></form>
   {#if preview}<section class="publication-preview"><h3 class="section-title">Approve this target copy</h3><p class="source-text">{inspectRecord.content}</p><p>Target: {publicationTarget}. Audience: {publicationPartition||'Workspace'}. Subject: target workspace. Original messages and actor profiles are not included.</p><Button class="btn-primary" disabled={busy} onclick={publish}>Approve publication</Button></section>{/if}
  {/if}
  {#each data.publications.value?.items??[] as item(item.id)}<article class="record-row"><div><strong>{item.target_workspace_id}</strong><MetadataRow items={[item.status,`Revision ${item.revision}`,item.target_node_id]} /></div><div class="actions">{#if item.status!=='revoked'}<Button class="btn-secondary" disabled={busy} onclick={()=>act(async()=>{await request(`/publications/${encodeURIComponent(item.id)}/retract`,{expected_revision:item.revision});},'Publication retracted; cleanup is tracked.')}>Retract copy</Button><Button class="btn-secondary" onclick={()=>{replacement=item.id;replacementRevision=item.revision;recordID=item.source_node_id;publicationTarget=item.target_workspace_id;publicationPartition=item.target_partition_id??'';void inspect();}}>Review updated source</Button>{/if}</div></article>{/each}
 </CollapsibleSection>
 {#if data.publications.value?.items.length===100}<a class="text-link" href={nextPage("publications",data.publications.value.items.at(-1)!.id)}>Next publications</a>{/if}
 <CollapsibleSection title="Deletion, export and retention">
  <p class="panel-meta">Deletion blocks reads immediately. Online cleanup and independent deletion history are reported separately from backup retention.</p>
  <form class="page-stack" onsubmit={e=>{e.preventDefault();void erase();}}>
   <SelectField label="Delete by" bind:value={eraseKind} items={[{value:'source',label:'Source message'},{value:'actor',label:'Actor contributions'},{value:'node',label:'Memory node'},{value:'workspace',label:'Entire selected partition'}]} />
   {#if eraseKind!=='workspace'}<TextInputField label="Record ID" bind:value={eraseID} required />{/if}{#if eraseKind==='source'}<TextInputField label="Source revision" type="number" min={1} bind:value={eraseRevision} required />{/if}
   <TextInputField label="Confirm workspace name" bind:value={eraseConfirm} required help={`Type ${workspace?.name??''} to confirm permanent deletion from the selected partition.`} /><div><Button type="submit" class="btn-danger" disabled={busy||eraseConfirm!==workspace?.name}>Request deletion</Button></div>
  </form>
  <div><Button class="btn-secondary" disabled={busy} onclick={()=>act(async()=>{selectedOperation=await request<MemoryOperation>('/exports',{});},'Export queued.')}>Export this partition</Button></div>
  {#each data.control.value?.operations??[] as op(op.id)}<div class="record-row"><div><strong>{op.kind}</strong><MetadataRow items={[op.status,op.read_blocked?'Reads blocked':'',op.online_cleaned?'Online cleanup complete':'',op.journal_persisted?'Deletion history persisted':'',op.backup_status]} />{#if op.failure_code}<p class="error-state">{op.failure_code}</p>{/if}</div><Button class="btn-secondary" disabled={busy} onclick={()=>operation(op.id)}>Inspect operation</Button></div>{/each}
  {#if selectedOperation}<div class="page-stack"><strong>{selectedOperation.id}</strong><MetadataRow items={[selectedOperation.status,selectedOperation.backup_status,selectedOperation.expires_at??'']} /><div class="actions"><Button class="btn-secondary" disabled={busy} onclick={()=>operation(selectedOperation!.id)}>Refresh operation</Button>{#if selectedOperation.kind==='export'&&selectedOperation.status==='complete'&&selectedOperation.result}<Button class="btn-primary" onclick={download}>Download export</Button>{/if}</div></div>{/if}
 </CollapsibleSection>
 <CollapsibleSection title="Background AI usage" count={data.usage.value?.items.length??0}>
  <p class="panel-meta">Broker records are references for reconciliation. MemStation does not add these charges to gateway billing. Unknown calls remain unresolved until the broker confirms their outcome.</p>
  {#each data.usage.value?.items??[] as call(call.id)}<article class="candidate"><strong>{call.role}</strong><MetadataRow items={[call.model_alias,call.model_version,call.status,`Attempt ${call.attempt}`]} /><MetadataRow items={[`Input ${call.input_tokens}`,`Output ${call.output_tokens}`,call.reported_cost?`${call.reported_cost} ${call.currency}`:'Cost not reported']} /><p class="helper-text">Call {call.id}. Broker usage {call.broker_usage_id||'Unknown'}. Job {call.job_id||'Query'}.</p>{#if call.status==='unknown'}<div><Button class="btn-secondary" disabled={busy} onclick={()=>act(async()=>{await request(`/usage/${encodeURIComponent(call.id)}/reconcile`,{});},'Broker usage refreshed.')}>Reconcile call</Button></div>{/if}</article>{:else}<p class="panel-meta">No broker calls in this partition.</p>{/each}
  {#if data.usage.value?.items.length===100}<a class="text-link" href={nextPage("usage",data.usage.value.items.at(-1)!.id)}>Next background calls</a>{/if}
 </CollapsibleSection>
</section>
<style>
 .memory-service{min-width:0}.scope-form{display:flex;gap:12px;align-items:end;flex-wrap:wrap}.scope-form :global(.field){flex:1;min-width:180px}.fields{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:12px}.record-row{display:flex;justify-content:space-between;align-items:start;gap:16px;padding:12px 0;border-bottom:1px solid var(--border-subtle)}.record-row>div{min-width:0;overflow-wrap:anywhere}.candidate{display:grid;gap:12px;padding:16px 0;border-bottom:1px solid var(--border-subtle)}.source-text{white-space:pre-wrap;overflow-wrap:anywhere;margin:0}.actions{display:flex;gap:8px;flex-wrap:wrap}.publication-preview{display:grid;gap:12px;padding:16px;border:1px solid var(--border)}.mono{overflow-wrap:anywhere}
 @media(max-width:600px){.fields{grid-template-columns:1fr}.record-row{flex-direction:column}.scope-form :global(.field){width:100%}}
</style>
