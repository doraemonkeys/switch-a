// Storage abstraction for testability
export interface Storage {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

// HTTP Client abstraction for testability
export interface HttpClient {
  fetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response>;
}

// API Client dependencies
export interface ApiClientDeps {
  storage: Storage;
  httpClient: HttpClient;
  baseUrl: string;
}

// Default implementations using browser APIs
export const browserStorage: Storage = {
  getItem: (key) => localStorage.getItem(key),
  setItem: (key, value) => localStorage.setItem(key, value),
  removeItem: (key) => localStorage.removeItem(key),
};

export const browserHttpClient: HttpClient = {
  fetch: (input, init) => fetch(input, init),
};
