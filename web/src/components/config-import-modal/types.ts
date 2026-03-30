import type {
  ImportConfigRequest,
  ImportPreviewResponse,
  ImportResult,
} from "../../api/types";

export type ImportStep = "select" | "preview" | "result";
export type SummarySectionKey = keyof ImportPreviewResponse["changes"];

export interface ConfigImportModalProps {
  isOpen: boolean;
  onClose: () => void;
  onPreview: (data: ImportConfigRequest) => Promise<ImportPreviewResponse>;
  onImport: (data: ImportConfigRequest) => Promise<ImportResult>;
  importing: boolean;
}

export interface GroupOption {
  id: string;
  name: string;
  providerCount: number;
}

export interface ProviderOption {
  id: string;
  name: string;
  groupId: string | null;
  groupName: string | null;
}

export interface SelectionCatalog {
  groups: GroupOption[];
  providers: ProviderOption[];
  providersById: Map<string, ProviderOption>;
}
