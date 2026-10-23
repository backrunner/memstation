'use strict';

// Synthetic planning fixtures. No network calls, credentials or persisted mutations.
const workspaces = {
  project: { name: 'MemStation 项目', kind: '项目', subject: 'MemStation', members: ['开发者', '编码 Agent', '审查 Agent'] },
  group: { name: '摄影交流群', kind: '群聊', subject: '摄影交流群', members: ['林青', '周瑞', '群聊 Agent'] },
  personal: { name: '个人事务', kind: '个人', subject: '用户', members: ['用户', '个人 Agent'] }
};
const memories = [
  { id:'p1', ws:'project', type:'事实', subject:'MemStation', text:'Go 服务是唯一业务核心，Manager 通过管理接口访问。', producer:'编码 Agent', visibility:'共享', time:'10:32', source:'开发者', quote:'业务编排和授权放在 Go 服务，Manager 只承担展示与请求组织。', evidence:'直接引用', revision:3, applicability:'项目通用', sourceId:'source-p1' },
  { id:'p2', ws:'project', type:'关键记忆', subject:'MemStation', text:'报告测试通过时，应附带真实执行结果与对应版本。', producer:'审查 Agent', visibility:'共享', time:'10:20', source:'开发者', quote:'测试跑了什么、在哪个版本跑的，要能回溯。', evidence:'直接引用', revision:1, applicability:'项目通用', sourceId:'source-p2' },
  { id:'p3', ws:'project', type:'执行笔记', subject:'检索模块', text:'等待真实并发测量后，再决定是否调整候选数量。', producer:'编码 Agent', visibility:'私有', time:'10:12', source:'编码 Agent', quote:'当前只完成代码检查，尚未取得生产量级测量结果。', evidence:'Agent 记录', revision:1, applicability:'当前 Run', sourceId:'source-p3' },
  { id:'p4', ws:'project', type:'候选', subject:'检索模块', text:'候选数量应提高到 60。', producer:'审查 Agent', visibility:'共享', time:'10:08', source:'审查 Agent', quote:'建议对 60 个候选进行对照测量。', evidence:'待验证建议', revision:1, applicability:'实验分支', sourceId:'source-p4', warning:'待确认：建议尚无执行证据，不用于事实注入。' },
  { id:'g1', ws:'group', type:'事实', subject:'林青', text:'林青最近开始学习摄影，正在练习街头拍摄。', producer:'群聊 Agent', visibility:'共享', time:'09:42', source:'林青', quote:'我最近开始学习摄影，这周想试试街头拍摄。', evidence:'本人自述', revision:1, applicability:'本群', sourceId:'source-g1' },
  { id:'g2', ws:'group', type:'事实', subject:'周瑞', text:'周瑞周末更想去画画，不参加这次街拍。', producer:'群聊 Agent', visibility:'共享', time:'09:44', source:'周瑞', quote:'我周末更想去画画，这次街拍就先不参加了。', evidence:'本人自述', revision:1, applicability:'本次活动', sourceId:'source-g2' },
  { id:'g3', ws:'group', type:'印象', subject:'林青', text:'林青可能对城市生活题材感兴趣。', producer:'群聊 Agent', visibility:'共享', time:'09:46', source:'林青', quote:'我最近在看一些街头摄影作品。', evidence:'模型推断', revision:1, applicability:'本群', sourceId:'source-g3', warning:'推断尚未确认；依据为本群发言，不作为确定偏好。' },
  { id:'h1', ws:'personal', type:'关键记忆', subject:'用户', text:'用户偏好简洁的中文回复，并保留必要的验证证据。', producer:'个人 Agent', visibility:'共享', time:'08:10', source:'用户', quote:'回复简洁一些，用中文，必要的验证结果要说清楚。', evidence:'本人自述', revision:2, applicability:'个人空间', sourceId:'source-h1' }
];
const agents = [
  {id:'coder',name:'编码 Agent',purpose:'项目实现与工具执行',last:'10:32'},
  {id:'reviewer',name:'审查 Agent',purpose:'检查变更与核对证据',last:'10:20'},
  {id:'chat',name:'群聊 Agent',purpose:'群消息摄取与按需回复',last:'09:46'},
  {id:'personal',name:'个人 Agent',purpose:'个人事务和偏好整理',last:'08:10'}
];
const capabilities = { read:'读取记忆', events:'记录事件', propose:'提交候选', invoke:'调用网关' };
const grants = {project:{coder:['read','events','propose','invoke'],reviewer:['read','events','propose']},group:{chat:['read','events','propose','invoke']},personal:{personal:['read','events','propose','invoke']}};
const views = { overview:'概览',memory:'记忆',conversations:'对话',runs:'执行记录',subjects:'成员与实体',access:'设置与访问',workspaces:'工作区',agents:'Agents' };
const $ = (selector) => document.querySelector(selector);
const esc = (value) => String(value).replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
let state = { workspace:'project', view:'memory', selected:'', query:'', type:'全部', visibility:'全部', subject:'全部' };
let editId = '';
let grantAgent = '';
const workspaceOptions = () => Object.entries(workspaces).map(([id,w])=>`<option value="${id}">${w.name}</option>`).join('');
$('#workspace-picker').innerHTML = workspaceOptions();
$('#mobile-workspace-picker').innerHTML = workspaceOptions();
const localMemories = () => memories.filter(m=>m.ws===state.workspace);
const href = (view, selected='', ws=state.workspace) => '#'+new URLSearchParams({workspace:ws,view,...(selected?{selected}: {})}).toString();
function navigate(view, selected='', ws=state.workspace) { location.hash=href(view,selected,ws); }
function announce(message) { $('#status').textContent=message; }
function parseRoute() {
  const p=new URLSearchParams(location.hash.slice(1));
  const ws=workspaces[p.get('workspace')]?p.get('workspace'):'project';
  if(ws!==state.workspace) { state.query='';state.type='全部';state.visibility='全部';state.subject='全部'; }
  state.workspace=ws;
  state.view=views[p.get('view')]?p.get('view'):'memory';
  state.selected=p.get('selected')||'';
  render();
}
function navigation() {
  const links=(names)=>names.map(v=>`<a class="ms-nav-link" href="${href(v)}" ${state.view===v?'aria-current="page"':''}>${views[v]}</a>`).join('');
  return '<span class="ms-nav-group">当前工作区</span>'+links(['overview','memory','conversations','runs','subjects','access'])+'<span class="ms-nav-group">管理</span>'+links(['workspaces','agents']);
}
function heading(title,sub='') { return `<div class="ms-page-heading"><div><h1>${title}</h1>${sub?`<p>${sub}</p>`:''}</div></div>`; }
function render() {
  $('#workspace-picker').value=state.workspace;
  $('#mobile-workspace-picker').value=state.workspace;
  $('#desktop-nav').innerHTML=navigation();$('#mobile-nav').innerHTML=navigation();
  $('#breadcrumb').textContent=workspaces[state.workspace].name+' / '+views[state.view];
  $('#status').textContent='';
  const screens={memory:renderMemory,overview:renderOverview,subjects:renderSubjects,agents:renderAgents,access:renderAccess,runs:renderRuns,conversations:renderConversations,workspaces:renderWorkspaces};
  $('#content').innerHTML=screens[state.view]();
  bindPage();
}
function renderMemory() {
  const subjects=[...new Set(localMemories().map(m=>m.subject))];
  const items=localMemories().filter(m=>(state.type==='全部'||m.type===state.type)&&(state.visibility==='全部'||m.visibility===state.visibility)&&(state.subject==='全部'||m.subject===state.subject)&&(!state.query||(m.text+m.subject+m.source).includes(state.query)));
  const selected=items.find(m=>m.id===state.selected);
  const options=(items,current)=>items.map(v=>`<option ${v===current?'selected':''}>${esc(v)}</option>`).join('');
  return heading('记忆','按主题或人物检索，沿来源核对每一条记录。')+`
    <form id="search-form" class="ms-toolbar"><label class="ms-field">搜索记忆<input id="query" value="${esc(state.query)}" placeholder="搜索主题、人物或问题"></label><button class="ms-primary">搜索</button></form>
    <details class="ms-filters" ${state.visibility!=='全部'||state.subject!=='全部'?'open':''}><summary>筛选${state.visibility!=='全部'||state.subject!=='全部'?' · 已选条件':''}</summary><div class="ms-filter-fields"><label class="ms-field">描述主体<select id="subject-filter">${options(['全部',...subjects],state.subject)}</select></label><label class="ms-field">可见范围<select id="visibility-filter">${options(['全部','共享','私有'],state.visibility)}</select></label></div></details>
    <div class="ms-subnav" aria-label="记忆类型">${['全部','事实','印象','关键记忆','候选'].map(t=>`<button data-type="${t}" aria-pressed="${state.type===t}">${t}</button>`).join('')}</div>
    <div class="ms-split ${selected?'has-selection':''}"><section class="ms-list-pane" aria-label="记忆结果"><p class="ms-help">${items.length} 条记录 · 当前工作区</p><div class="ms-list">${items.map(m=>`<article class="ms-record ${m.id===state.selected?'is-selected':''}"><a class="ms-record-title" href="${href('memory',m.id)}">${esc(m.text)}</a><div class="ms-record-meta"><span>${esc(m.subject)} · ${m.type}</span><span>${m.visibility==='私有'?m.producer+' 私有':'工作区共享'}</span><span>${m.time}</span></div>${m.warning?`<p class="ms-attention">${m.warning}</p>`:''}</article>`).join('')||'<div class="ms-empty">没有匹配记录。调整关键词或筛选条件后再试。</div>'}</div></section>${selected?memoryDetail(selected):''}</div>`;
}
function memoryDetail(m) {
  return `<aside class="ms-inspector" aria-label="记忆详情"><div class="ms-inspector-head"><a href="${href('memory')}">返回结果</a><button data-edit="${m.id}">编辑</button></div><h2>${esc(m.text)}</h2>${m.warning?`<p class="ms-attention">${m.warning}</p>`:''}<dl class="ms-facts"><dt>描述主体</dt><dd><a href="${href('subjects',m.subject)}">${m.subject}</a></dd><dt>可见范围</dt><dd>${m.visibility==='私有'?m.producer+' 私有':'本工作区共享'}</dd><dt>适用条件</dt><dd>${m.applicability}</dd><dt>生成者</dt><dd>${m.producer}</dd></dl><section><h3>来源证据</h3><p>${m.source} · ${m.time} · ${m.evidence}</p><p class="ms-quote">${esc(m.quote)}</p><a href="${href('conversations',m.sourceId)}">查看原始消息 →</a></section><section><h3>修订</h3><p>版本 ${m.revision} · ${m.lastReason?esc(m.lastReason):'按来源生成'}</p></section><section><details><summary>关系与原始记录</summary><p class="ms-help">内部标识 ${m.id} · 来源 ${m.sourceId}</p><a href="${href('runs')}">查看相关执行</a></details></section></aside>`;
}
function renderOverview() {
  const ms=localMemories();const count=Object.keys(grants[state.workspace]).length;
  return heading('概览',workspaces[state.workspace].name+' · '+workspaces[state.workspace].kind+'空间')+`<div class="ms-stats"><div class="ms-stat"><span>记忆</span><strong>${ms.length}</strong></div><div class="ms-stat"><span>已授权 Agent</span><strong>${count}</strong></div><div class="ms-stat"><span>成员与实体</span><strong>${workspaces[state.workspace].members.length}</strong></div><div class="ms-stat"><span>待确认</span><strong>${ms.filter(m=>m.warning).length}</strong></div></div><div class="ms-grid"><section><h2>最近记忆</h2><div class="ms-list">${ms.slice(0,3).map(m=>`<article class="ms-record"><a href="${href('memory',m.id)}">${esc(m.text)}</a><p class="ms-help">${m.subject} · ${m.time}</p></article>`).join('')}</div></section><section><h2>工作区访问</h2><div class="ms-list">${Object.keys(grants[state.workspace]).map(id=>`<article class="ms-record"><a href="${href('access')}">${agents.find(a=>a.id===id).name}</a><p class="ms-help">${grants[state.workspace][id].map(c=>capabilities[c]).join('、')}</p></article>`).join('')}</div></section></div>`;
}
function renderSubjects() {
  const names=[...new Set([...workspaces[state.workspace].members,...localMemories().map(m=>m.subject)])];
  const selected=names.includes(state.selected)?state.selected:'';
  const details=selected?`<aside class="ms-inspector"><div class="ms-inspector-head"><h2>${esc(selected)}</h2><a href="${href('subjects')}">返回列表</a></div><p class="ms-muted">当前空间内的画像与知识</p><section><h3>事实与关键记忆</h3>${localMemories().filter(m=>m.subject===selected&&m.type!=='印象').map(m=>`<article class="ms-record"><a href="${href('memory',m.id)}">${esc(m.text)}</a><p class="ms-help">${m.evidence} · ${m.source}</p></article>`).join('')||'<p class="ms-help">暂无已确认记录。</p>'}</section><section><h3>印象与推断</h3>${localMemories().filter(m=>m.subject===selected&&m.type==='印象').map(m=>`<article class="ms-record"><a href="${href('memory',m.id)}">${esc(m.text)}</a><p class="ms-help">${m.evidence} · 可查看来源</p></article>`).join('')||'<p class="ms-help">暂无印象。</p>'}</section></aside>`:'';
  return heading('成员与实体','画像依据当前空间的消息和证据。')+`<div class="ms-split ${selected?'has-selection':''}"><div class="ms-list-pane ms-list">${names.map(n=>`<article class="ms-record"><a class="ms-record-title" href="${href('subjects',n)}">${n}</a><p class="ms-help">${n.includes('Agent')?'Agent':state.workspace==='group'?'群成员':'人物或项目实体'} · ${localMemories().filter(m=>m.subject===n).length} 条相关记忆</p></article>`).join('')}</div>${details}</div>`;
}
function renderAgents() {
  return heading('Agents','独立身份与凭据，共用服务，按工作区授予访问范围。')+`<table class="ms-table"><thead><tr><th>Agent</th><th>用途</th><th>已授权空间</th><th>最近活动</th><th>操作</th></tr></thead><tbody>${agents.map(a=>`<tr><td><strong>${a.name}</strong></td><td data-label="用途">${a.purpose}</td><td data-label="空间">${Object.entries(grants).filter(([,g])=>g[a.id]?.length).map(([ws])=>`<a href="${href('access','',ws)}">${workspaces[ws].name}</a>`).join('、')||'无'}</td><td data-label="最近活动">${a.last}</td><td><button data-grant="${a.id}">编辑访问</button></td></tr>`).join('')}</tbody></table><p class="ms-help">编辑访问操作作用于当前选中的 ${workspaces[state.workspace].name}。最近活动不表示在线。</p>`;
}
function renderAccess() {
  return heading('设置与访问','明确哪些 Agent 可以在 '+workspaces[state.workspace].name+' 中读取和写入。')+`<table class="ms-table"><thead><tr><th>Agent</th><th>有效能力</th><th>操作</th></tr></thead><tbody>${agents.filter(a=>grants[state.workspace][a.id]?.length).map(a=>`<tr><td><strong>${a.name}</strong></td><td data-label="有效能力">${grants[state.workspace][a.id].map(c=>capabilities[c]).join('、')}</td><td><button data-grant="${a.id}">编辑</button></td></tr>`).join('')||'<tr><td colspan="3">尚未授权 Agent。</td></tr>'}</tbody></table><section class="ms-section"><h2>添加 Agent</h2><div class="ms-actions">${agents.filter(a=>!grants[state.workspace][a.id]?.length).map(a=>`<button data-grant="${a.id}">${a.name}</button>`).join('')||'<p class="ms-muted">所有示例 Agent 已有授权。</p>'}</div></section><section class="ms-section"><h2>共享规则</h2><p>默认仅使用本工作区的共享内容，以及当前 Agent 自己的私有笔记。</p><p class="ms-help">其他工作区的记忆需要显式发布，身份关联不会自动扩大访问范围。</p></section>`;
}
function renderRuns() {
  const actor=agents.find(a=>grants[state.workspace][a.id]?.length)?.name||'未授权 Agent';
  const rs=state.workspace==='project'?[{id:'run-1',title:'实现事件摄取校验',agent:'编码 Agent',status:'已报告完成',time:'10:32'},{id:'run-2',title:'审查作用域与证据',agent:'审查 Agent',status:'进行中',time:'10:20'}]:[{id:'run-3',title:state.workspace==='group'?'整理本群消息和成员画像':'整理个人偏好',agent:actor,status:'已报告完成',time:'09:46'}];
  const selected=rs.find(r=>r.id===state.selected);
  return heading('执行记录','查看外部 Agent 的执行、工具结果和生成的记忆。')+`<div class="ms-split ${selected?'has-selection':''}"><div class="ms-list-pane ms-list">${rs.map(r=>`<article class="ms-record"><a class="ms-record-title" href="${href('runs',r.id)}">${r.title}</a><div class="ms-record-meta"><span>${r.agent}</span><span>${r.status}</span><span>最后报告 ${r.time}</span></div></article>`).join('')}</div>${selected?`<aside class="ms-inspector"><a href="${href('runs')}">返回执行列表</a><h2>${selected.title}</h2><p class="ms-help">${selected.agent} · ${selected.status}</p><ol class="ms-timeline"><li><strong>读取当前空间上下文</strong><p class="ms-muted">已排除未授权来源</p></li><li><strong>处理消息或工具结果</strong><p class="ms-muted">来源事件已记录</p></li><li><strong>提交候选与证据</strong><p class="ms-muted">沿来源版本校验</p></li></ol><a href="${href('memory')}">查看本空间记忆 →</a><p class="ms-help">此页记录执行情况，不控制远程 Agent 进程。</p></aside>`:''}</div>`;
}
function renderConversations() {
  return heading('对话','保留发言人、引用和来源版本；每条消息可回到对应记忆。')+`<div class="ms-list">${localMemories().map(m=>`<article class="ms-message ${state.selected===m.sourceId?'is-selected':''}" id="${m.sourceId}"><div class="ms-record-meta"><strong>${m.source}</strong><span>${m.time}</span><span>来源版本 1</span></div><p>${esc(m.quote)}</p><p><a href="${href('memory',m.id)}">查看派生记忆 →</a></p></article>`).join('')}</div>`;
}
function renderWorkspaces() {
  return heading('工作区','每个空间分别管理记忆、参与者和访问范围。')+`<div class="ms-list">${Object.entries(workspaces).map(([id,w])=>`<article class="ms-record"><a class="ms-record-title" href="${href('overview','',id)}">${w.name}</a><div class="ms-record-meta"><span>${w.kind}</span><span>${memories.filter(m=>m.ws===id).length} 条记忆</span><span>${Object.keys(grants[id]).filter(a=>grants[id][a].length).length} 个已授权 Agent</span></div></article>`).join('')}</div>`;
}
function bindPage() {
  $('#search-form')?.addEventListener('submit',e=>{e.preventDefault();state.query=$('#query').value.trim();state.selected='';render();$('#query').focus();});
  $('#subject-filter')?.addEventListener('change',e=>{state.subject=e.target.value;state.selected='';render();$('#subject-filter').focus();});
  $('#visibility-filter')?.addEventListener('change',e=>{state.visibility=e.target.value;state.selected='';render();$('#visibility-filter').focus();});
}
document.addEventListener('click',e=>{
  const close=e.target.closest('[data-close]');if(close){$('#'+close.dataset.close).close();return;}
  const type=e.target.closest('[data-type]');if(type){state.type=type.dataset.type;state.selected='';render();document.querySelector(`[data-type="${state.type}"]`)?.focus();return;}
  const edit=e.target.closest('[data-edit]');if(edit){const m=localMemories().find(m=>m.id===edit.dataset.edit);if(!m)return;editId=m.id;$('#edit-scope').textContent=workspaces[state.workspace].name+' · '+m.subject+' · 版本 '+m.revision;$('#edit-form').elements.content.value=m.text;$('#edit-form').elements.reason.value='';$('#edit-dialog').showModal();return;}
  const grant=e.target.closest('[data-grant]');if(grant){grantAgent=grant.dataset.grant;$('#grant-scope').textContent=agents.find(a=>a.id===grantAgent).name+' → '+workspaces[state.workspace].name;$('#grant-fields').innerHTML=Object.entries(capabilities).map(([id,label])=>`<label class="ms-check"><input type="checkbox" name="${id}" ${(grants[state.workspace][grantAgent]||[]).includes(id)?'checked':''}>${label}</label>`).join('');$('#grant-dialog').showModal();return;}
  if(e.target.closest('#mobile-nav a')) $('#nav-dialog').close();
});
$('#workspace-picker').addEventListener('change',e=>navigate(state.view,'',e.target.value));
$('#mobile-workspace-picker').addEventListener('change',e=>{$('#nav-dialog').close();navigate(state.view,'',e.target.value);});
$('#open-nav').addEventListener('click',()=>$('#nav-dialog').showModal());
$('#nav-dialog').addEventListener('close',()=>$('#open-nav').focus({preventScroll:true}));
$('#edit-form').addEventListener('submit',e=>{e.preventDefault();const m=localMemories().find(m=>m.id===editId);if(!m)return;m.text=e.target.elements.content.value.trim();m.lastReason=e.target.elements.reason.value.trim();m.revision++;$('#edit-dialog').close();render();document.querySelector(`[data-edit="${m.id}"]`)?.focus();announce('示例记忆已保存为版本 '+m.revision+'，可见范围保持不变。');});
$('#grant-form').addEventListener('submit',e=>{e.preventDefault();grants[state.workspace][grantAgent]=Object.keys(capabilities).filter(c=>e.target.elements[c].checked);$('#grant-dialog').close();render();document.querySelector(`[data-grant="${grantAgent}"]`)?.focus();announce('示例访问范围已更新，仅作用于 '+workspaces[state.workspace].name+'。');});
window.addEventListener('hashchange',parseRoute);
parseRoute();
