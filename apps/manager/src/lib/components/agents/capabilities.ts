export const capabilityOptions = [
  { value: 'gateway.invoke', label: 'Call models', help: 'Send requests through the AI gateway.' },
  { value: 'memory.read', label: 'Read memory', help: 'Search and inject Personal workspace memory.' },
  { value: 'memory.edit', label: 'Edit memory', help: 'Modify memory. Tool writes also require the user setting and allowed keys.' },
  { value: 'files.read', label: 'Read files', help: 'Read uploaded files and use them in gateway requests.' },
  { value: 'files.write', label: 'Manage files', help: 'Upload and delete files.' }
];

export const defaultCapabilities = ['gateway.invoke', 'memory.read', 'files.read'];

export function capabilitySummary(caps: string[]): string {
  return caps.map((cap) => capabilityOptions.find((item) => item.value === cap)?.label ?? cap).join(', ') || 'No operational access';
}
