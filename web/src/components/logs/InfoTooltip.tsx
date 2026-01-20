interface InfoTooltipProps {
  text: string;
}

export function InfoTooltip({ text }: InfoTooltipProps) {
  return (
    <span className="relative group cursor-help text-text-muted">
      <span className="text-xs">ℹ️</span>
      <span className="invisible group-hover:visible absolute left-1/2 -translate-x-1/2 bottom-full mb-2 w-64 p-2 text-xs text-white bg-gray-900 rounded-lg shadow-lg z-50 whitespace-normal">
        {text}
        <span className="absolute left-1/2 -translate-x-1/2 top-full border-4 border-transparent border-t-gray-900" />
      </span>
    </span>
  );
}
