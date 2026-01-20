export interface SectionProps {
  getValue: (key: string, defaultValue: string | number | boolean) => string;
  handleChange: (key: string, value: string) => void;
  /** Get server default value for a key */
  getDefault?: (key: string) => string | undefined;
}

export interface FormActionsProps {
  isDirty: boolean;
  saving: boolean;
  onReset: () => void;
}
