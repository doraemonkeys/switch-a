/** Modified indicator badge component - shows when current value differs from default */
export function ModifiedBadge({
  currentValue,
  getDefault,
  configKey,
}: {
  currentValue: string;
  getDefault?: (key: string) => string | undefined;
  configKey: string;
}) {
  const defaultValue = getDefault?.(configKey);

  // Only show badge when value differs from default
  if (defaultValue === undefined || currentValue === defaultValue) {
    return null;
  }

  return (
    <span
      className="ml-2 inline-flex items-center px-1.5 py-0.5 rounded text-xs font-medium bg-primary/10 text-primary cursor-help"
      title={`Default: ${defaultValue}`}
    >
      Modified
    </span>
  );
}
