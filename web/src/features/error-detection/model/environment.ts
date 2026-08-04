export function parseGlobalMaxAttempts(
  value: string | undefined,
): number | null {
  if (value === undefined || !/^(0|[1-9]\d*)$/u.test(value)) return null;
  const attempts = Number(value);
  return Number.isSafeInteger(attempts) ? attempts : null;
}
