export const capabilityOptions = [
  { value: 'gateway.invoke', label: 'Call models', help: 'Send requests through the AI gateway.' },
  { value: 'memory.read', label: 'Read memory', help: 'Search memory in authorized workspaces.' },
  { value: 'memory.edit', label: 'Edit memory', help: 'Modify memory. Tool writes also require the user setting and allowed keys.' },
  { value: 'files.read', label: 'Read files', help: 'Read uploaded files and use them in gateway requests.' },
  { value: 'files.write', label: 'Manage files', help: 'Upload and delete files.' },
  { value: 'events.write', label: 'Ingest events', help: 'Submit confirmed source events and read processing progress.' },
  { value: 'sources.read', label: 'Read sources', help: 'Read authorized source text and revisions.' },
  { value: 'sources.retract', label: 'Retract sources', help: 'Remove source messages and derived contributions.' },
  { value: 'memory.propose', label: 'Propose memory', help: 'Submit candidates for review.' },
  { value: 'memory.forget', label: 'Erase memory', help: 'Request tracked deletion within the authorized scope.' },
  { value: 'memory.export', label: 'Export memory', help: 'Export authorized memory records.' },
  { value: 'usage.read', label: 'Read background usage', help: 'Reconcile broker calls without charging twice.' }
];

export const defaultCapabilities = ['gateway.invoke', 'memory.read', 'files.read'];

export function capabilitySummary(caps: string[]): string {
  return caps.map((cap) => capabilityOptions.find((item) => item.value === cap)?.label ?? cap).join(', ') || 'No operational access';
}
