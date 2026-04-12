const adminKeys = new Map<string, string>();

export function rememberAdminKey(targetId: string, adminKey: string | undefined): void {
  const value = adminKey?.trim();
  if (!value) {
    return;
  }
  adminKeys.set(targetId, value);
}

export function getAdminKey(targetId: string): string | undefined {
  return adminKeys.get(targetId);
}

export function forgetAdminKey(targetId: string): void {
  adminKeys.delete(targetId);
}
