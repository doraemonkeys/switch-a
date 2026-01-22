import { useId } from "react";

interface FormFieldProps {
  label: string;
  children: (id: string) => React.ReactNode;
}

/**
 * FormField component that manages label-input association via render props.
 * Uses render props pattern exclusively to ensure type safety and proper id binding.
 */
export function FormField({ label, children }: FormFieldProps) {
  const id = useId();

  return (
    <div>
      <label
        htmlFor={id}
        className="block text-sm font-medium text-text-secondary mb-1"
      >
        {label}
      </label>
      {children(id)}
    </div>
  );
}
