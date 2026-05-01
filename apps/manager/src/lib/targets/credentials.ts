const credentials = new Map<string, string>();

export function rememberTargetCredential(targetId: string, credential: string | undefined): void {
  const normalizedId = targetId.trim();
  const normalizedCredential = credential?.trim();
  if (!normalizedId) {
    return;
  }
  if (!normalizedCredential) {
    credentials.delete(normalizedId);
    return;
  }
  credentials.set(normalizedId, normalizedCredential);
}

export function readTargetCredential(targetId: string): string {
  return credentials.get(targetId.trim()) ?? '';
}

export function forgetTargetCredential(targetId: string): void {
  credentials.delete(targetId.trim());
}
